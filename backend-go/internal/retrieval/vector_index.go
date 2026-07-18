package retrieval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	defaultVectorLeaseDuration = 5 * time.Minute
	vectorAuditSampleLimit     = 100
)

type VectorIndexOptions struct {
	EmbeddingModel    string
	EmbeddingRevision string
	VectorDimension   int
	BatchSize         int
	LeaseOwner        string
	LeaseDuration     time.Duration
	Logger            *slog.Logger
}

type vectorIndexRepository interface {
	LoadVectorIndex(context.Context, string) (VectorIndex, error)
	AcquireVectorIndex(context.Context, VectorIndex, string, string, time.Duration) (VectorIndex, error)
	SaveVectorIndexCheckpoint(context.Context, string, string, int64, int64, int64, int64, time.Duration, bool) error
	CompleteVectorIndex(context.Context, string, string, int64, string) (VectorIndex, error)
	MarkVectorIndexFailed(string, string, error)
	ListVectorManifest(context.Context, string) ([]VectorManifestEntry, error)
	ListVectorChunks(context.Context, string, int64, int) ([]VectorChunk, error)
}

type VectorIndexer struct {
	repository vectorIndexRepository
	embedder   Embedder
	store      VectorStore
	options    VectorIndexOptions
}

func NewVectorIndexer(repository *Repository, embedder Embedder, store VectorStore, options VectorIndexOptions) *VectorIndexer {
	return newVectorIndexer(repository, embedder, store, options)
}

func newVectorIndexer(repository vectorIndexRepository, embedder Embedder, store VectorStore, options VectorIndexOptions) *VectorIndexer {
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = defaultVectorLeaseDuration
	}
	if strings.TrimSpace(options.LeaseOwner) == "" {
		hostname, _ := os.Hostname()
		options.LeaseOwner = fmt.Sprintf("%s:%d", hostname, os.Getpid())
	}
	return &VectorIndexer{repository: repository, embedder: embedder, store: store, options: options}
}

func (i *VectorIndexer) Build(ctx context.Context, ingestionRunID string) (index VectorIndex, err error) {
	ingestionRunID = strings.TrimSpace(ingestionRunID)
	if ingestionRunID == "" || i.repository == nil || i.embedder == nil || i.store == nil ||
		i.options.BatchSize <= 0 || i.options.VectorDimension <= 0 {
		return VectorIndex{}, fmt.Errorf("%w: incomplete vector index configuration", ErrInvalidInput)
	}
	if existing, loadErr := i.repository.LoadVectorIndex(ctx, ingestionRunID); loadErr == nil && existing.Status == "completed" {
		if err := i.validateIdentity(existing); err != nil {
			return VectorIndex{}, err
		}
		audit, auditErr := i.Audit(ctx, ingestionRunID, false)
		if auditErr != nil {
			return VectorIndex{}, auditErr
		}
		if !audit.Exact {
			return VectorIndex{}, fmt.Errorf("%w: completed index audit failed", ErrVectorIndexCorrupt)
		}
		return existing, nil
	} else if loadErr != nil && !errors.Is(loadErr, ErrIndexNotReady) {
		return VectorIndex{}, loadErr
	}

	collection := vectorCollectionName(ingestionRunID)
	logger := i.options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := time.Now()
	identity := VectorIndex{
		IngestionRunID: ingestionRunID, IndexVersion: VectorIndexVersion,
		EmbeddingModel: i.options.EmbeddingModel, EmbeddingRevision: i.options.EmbeddingRevision,
		VectorDimension: i.options.VectorDimension, DistanceMetric: "Cosine", CollectionName: collection,
	}
	leaseToken, err := newVectorLeaseToken()
	if err != nil {
		return VectorIndex{}, err
	}
	index, err = i.repository.AcquireVectorIndex(
		ctx, identity, i.options.LeaseOwner, leaseToken, i.options.LeaseDuration,
	)
	if err != nil {
		return VectorIndex{}, err
	}
	if index.Status == "completed" {
		return i.Build(ctx, ingestionRunID)
	}
	defer func() {
		if err != nil {
			i.repository.MarkVectorIndexFailed(ingestionRunID, leaseToken, err)
		}
	}()

	manifest, err := i.repository.ListVectorManifest(ctx, ingestionRunID)
	if err != nil {
		return VectorIndex{}, err
	}
	afterChunkID, pointCount, err := i.prepareCollection(ctx, index, leaseToken, manifest)
	if err != nil {
		return VectorIndex{}, err
	}
	logger.Info("vector index build started", "ingestion_run_id", ingestionRunID, "collection", collection,
		"embedding_model", i.options.EmbeddingModel, "batch_size", i.options.BatchSize,
		"attempt", index.BuildAttempt, "resume_points", pointCount, "resume_chunk_id", afterChunkID)

	nextProgressLog := ((pointCount / 1000) + 1) * 1000
	for {
		chunks, listErr := i.repository.ListVectorChunks(ctx, ingestionRunID, afterChunkID, i.options.BatchSize)
		if listErr != nil {
			return VectorIndex{}, listErr
		}
		if len(chunks) == 0 {
			break
		}
		inputs := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			inputs = append(inputs, chunk.Content)
		}
		vectors, embedErr := i.embedder.EmbedDocuments(ctx, inputs)
		if embedErr != nil {
			return VectorIndex{}, embedErr
		}
		if len(vectors) != len(chunks) {
			return VectorIndex{}, fmt.Errorf("embedding count mismatch: vectors=%d chunks=%d", len(vectors), len(chunks))
		}
		points := make([]VectorPoint, 0, len(chunks))
		for offset, chunk := range chunks {
			points = append(points, VectorPoint{ID: chunk.ChunkID, Vector: vectors[offset], Payload: vectorPayload(chunk)})
		}
		if err = i.store.Upsert(ctx, collection, points); err != nil {
			return VectorIndex{}, err
		}
		afterChunkID = chunks[len(chunks)-1].ChunkID
		pointCount += int64(len(chunks))
		if err = i.repository.SaveVectorIndexCheckpoint(
			ctx, ingestionRunID, leaseToken, afterChunkID, pointCount, 0, 0, i.options.LeaseDuration, false,
		); err != nil {
			return VectorIndex{}, err
		}
		if pointCount >= nextProgressLog {
			elapsed := time.Since(startedAt)
			logger.Info("vector index build progress", "ingestion_run_id", ingestionRunID,
				"points", pointCount, "last_chunk_id", afterChunkID, "elapsed", elapsed.Round(time.Second),
				"points_per_second", float64(pointCount-index.CheckpointPoints)/elapsed.Seconds())
			nextProgressLog = ((pointCount / 1000) + 1) * 1000
		}
	}

	audit, err := i.auditWithManifest(ctx, index, manifest, true)
	if err != nil {
		return VectorIndex{}, err
	}
	if !audit.Exact || audit.ExpectedPointCount != pointCount {
		return VectorIndex{}, fmt.Errorf(
			"%w: expected=%d checkpoint=%d actual=%d missing=%d orphan=%d mismatched=%d",
			ErrVectorIndexCorrupt, audit.ExpectedPointCount, pointCount, audit.ActualPointCount,
			audit.MissingPointCount, audit.OrphanPointCount-audit.OrphansDeleted, audit.MismatchedPointCount,
		)
	}
	if err = i.repository.SaveVectorIndexCheckpoint(
		ctx, ingestionRunID, leaseToken, afterChunkID, pointCount,
		audit.OrphanPointCount, audit.MissingPointCount, i.options.LeaseDuration, true,
	); err != nil {
		return VectorIndex{}, err
	}
	checksum := vectorManifestChecksum(identity, manifest)
	index, err = i.repository.CompleteVectorIndex(ctx, ingestionRunID, leaseToken, pointCount, checksum)
	if err != nil {
		return VectorIndex{}, err
	}
	logger.Info("vector index build completed", "ingestion_run_id", ingestionRunID, "points", pointCount,
		"elapsed", time.Since(startedAt).Round(time.Second), "manifest_checksum", checksum,
		"attempt", index.BuildAttempt)
	return index, nil
}

func (i *VectorIndexer) Audit(ctx context.Context, ingestionRunID string, repairOrphans bool) (VectorIndexAudit, error) {
	ingestionRunID = strings.TrimSpace(ingestionRunID)
	if ingestionRunID == "" || i.repository == nil || i.store == nil {
		return VectorIndexAudit{}, fmt.Errorf("%w: incomplete vector audit configuration", ErrInvalidInput)
	}
	index, err := i.repository.LoadVectorIndex(ctx, ingestionRunID)
	if err != nil {
		return VectorIndexAudit{}, err
	}
	if err := i.validateIdentity(index); err != nil {
		return VectorIndexAudit{}, err
	}
	manifest, err := i.repository.ListVectorManifest(ctx, ingestionRunID)
	if err != nil {
		return VectorIndexAudit{}, err
	}
	return i.auditWithManifest(ctx, index, manifest, repairOrphans)
}

func (i *VectorIndexer) prepareCollection(
	ctx context.Context,
	index VectorIndex,
	leaseToken string,
	manifest []VectorManifestEntry,
) (int64, int64, error) {
	if err := validateVectorCheckpoint(index, manifest); err != nil {
		return 0, 0, err
	}
	if index.CheckpointPoints == 0 {
		if err := i.recreateCollection(ctx, index); err != nil {
			return 0, 0, err
		}
		if err := i.repository.SaveVectorIndexCheckpoint(
			ctx, index.IngestionRunID, leaseToken, 0, 0, 0, 0, i.options.LeaseDuration, true,
		); err != nil {
			return 0, 0, err
		}
		return 0, 0, nil
	}
	exists, err := i.store.CollectionExists(ctx, index.CollectionName)
	if err != nil {
		return 0, 0, err
	}
	if !exists {
		if err := i.recreateCollection(ctx, index); err != nil {
			return 0, 0, err
		}
		if err := i.repository.SaveVectorIndexCheckpoint(
			ctx, index.IngestionRunID, leaseToken, 0, 0, 0, index.CheckpointPoints,
			i.options.LeaseDuration, true,
		); err != nil {
			return 0, 0, err
		}
		return 0, 0, nil
	}
	actualManifest, err := i.store.ListPointManifest(ctx, index.CollectionName)
	if err != nil {
		return 0, 0, err
	}
	plan, err := planVectorResume(manifest, index.CheckpointChunkID, index.CheckpointPoints, actualManifest)
	if err != nil {
		return 0, 0, err
	}
	pointsToDelete := append(append([]int64(nil), plan.OrphanIDs...), plan.MismatchedIDs...)
	if len(pointsToDelete) > 0 {
		if err := i.store.DeletePoints(ctx, index.CollectionName, pointsToDelete); err != nil {
			return 0, 0, err
		}
	}
	if err := i.repository.SaveVectorIndexCheckpoint(
		ctx, index.IngestionRunID, leaseToken, plan.CheckpointChunkID, plan.CheckpointPoints,
		int64(len(plan.OrphanIDs)), int64(len(plan.MissingCheckpointIDs)+len(plan.MismatchedIDs)),
		i.options.LeaseDuration, true,
	); err != nil {
		return 0, 0, err
	}
	return plan.CheckpointChunkID, plan.CheckpointPoints, nil
}

func (i *VectorIndexer) recreateCollection(ctx context.Context, index VectorIndex) error {
	metadata := map[string]any{
		"ingestion_run_id": index.IngestionRunID, "index_version": VectorIndexVersion,
		"embedding_model": i.options.EmbeddingModel, "embedding_revision": i.options.EmbeddingRevision,
	}
	if err := i.store.RecreateCollection(ctx, index.CollectionName, i.options.VectorDimension, metadata); err != nil {
		return err
	}
	payloadIndexes := []struct {
		field  string
		schema string
	}{
		{"document_lifecycle", "keyword"}, {"document_type", "keyword"},
		{"document_visibility", "keyword"}, {"note_id", "integer"},
		{"project_id", "integer"}, {"project_visibility", "keyword"}, {"source_type", "keyword"},
	}
	for _, payloadIndex := range payloadIndexes {
		if err := i.store.CreatePayloadIndex(ctx, index.CollectionName, payloadIndex.field, payloadIndex.schema); err != nil {
			return err
		}
	}
	return nil
}

func (i *VectorIndexer) auditWithManifest(
	ctx context.Context,
	index VectorIndex,
	manifest []VectorManifestEntry,
	repairOrphans bool,
) (VectorIndexAudit, error) {
	report := VectorIndexAudit{
		IngestionRunID: index.IngestionRunID, IndexVersion: index.IndexVersion,
		CollectionName: index.CollectionName, IndexStatus: index.Status,
		ExpectedPointCount: int64(len(manifest)), DatabasePointCount: index.PointCount,
		ManifestChecksum: vectorManifestChecksum(index, manifest), StoredIndexChecksum: index.IndexChecksum,
	}
	exists, err := i.store.CollectionExists(ctx, index.CollectionName)
	if err != nil {
		return VectorIndexAudit{}, err
	}
	report.CollectionExists = exists
	if !exists {
		report.MissingPointCount = int64(len(manifest))
		report.MissingPointSample = manifestIDSamples(manifest, vectorAuditSampleLimit)
		return report, nil
	}
	actualManifest, err := i.store.ListPointManifest(ctx, index.CollectionName)
	if err != nil {
		return VectorIndexAudit{}, err
	}
	report.ActualPointCount = int64(len(actualManifest))
	missing, orphans, mismatched := compareVectorPoints(manifest, actualManifest)
	report.MissingPointCount = int64(len(missing))
	report.OrphanPointCount = int64(len(orphans))
	report.MismatchedPointCount = int64(len(mismatched))
	report.MissingPointSample = int64Sample(missing, vectorAuditSampleLimit)
	report.OrphanPointSample = int64Sample(orphans, vectorAuditSampleLimit)
	report.MismatchedPointSample = int64Sample(mismatched, vectorAuditSampleLimit)
	if repairOrphans && len(orphans) > 0 {
		if err := i.store.DeletePoints(ctx, index.CollectionName, orphans); err != nil {
			return VectorIndexAudit{}, err
		}
		report.OrphansDeleted = int64(len(orphans))
		report.ActualPointCount -= int64(len(orphans))
	}
	remainingOrphans := report.OrphanPointCount - report.OrphansDeleted
	checksumMatches := index.Status != "completed" || index.IndexChecksum == report.ManifestChecksum
	report.Exact = report.MissingPointCount == 0 && remainingOrphans == 0 && report.MismatchedPointCount == 0 &&
		report.ActualPointCount == report.ExpectedPointCount &&
		(index.Status != "completed" || index.PointCount == report.ExpectedPointCount) && checksumMatches
	return report, nil
}

func (i *VectorIndexer) validateIdentity(index VectorIndex) error {
	if index.IndexVersion != VectorIndexVersion || index.EmbeddingModel != i.options.EmbeddingModel ||
		index.EmbeddingRevision != i.options.EmbeddingRevision || index.VectorDimension != i.options.VectorDimension {
		return ErrIndexVersionMismatch
	}
	return nil
}

type vectorResumePlan struct {
	CheckpointChunkID    int64
	CheckpointPoints     int64
	MissingCheckpointIDs []int64
	OrphanIDs            []int64
	MismatchedIDs        []int64
}

func planVectorResume(
	manifest []VectorManifestEntry,
	checkpointChunkID int64,
	checkpointPoints int64,
	actualManifest []VectorManifestEntry,
) (vectorResumePlan, error) {
	index := VectorIndex{CheckpointChunkID: checkpointChunkID, CheckpointPoints: checkpointPoints}
	if err := validateVectorCheckpoint(index, manifest); err != nil {
		return vectorResumePlan{}, err
	}
	expected := make(map[int64]string, len(manifest))
	for _, entry := range manifest {
		expected[entry.ChunkID] = entry.ContentHash
	}
	actual := make(map[int64]string, len(actualManifest))
	orphans := make([]int64, 0)
	mismatched := make([]int64, 0)
	for _, point := range actualManifest {
		if _, duplicate := actual[point.ChunkID]; duplicate {
			return vectorResumePlan{}, fmt.Errorf("%w: duplicate Qdrant point id %d", ErrVectorIndexCorrupt, point.ChunkID)
		}
		actual[point.ChunkID] = point.ContentHash
		expectedHash, ok := expected[point.ChunkID]
		if !ok {
			orphans = append(orphans, point.ChunkID)
		} else if point.ContentHash != expectedHash {
			mismatched = append(mismatched, point.ChunkID)
		}
	}
	missingCheckpointIDs := make([]int64, 0)
	rewindTo := int(checkpointPoints)
	for position := 0; position < int(checkpointPoints); position++ {
		actualHash, ok := actual[manifest[position].ChunkID]
		if !ok || actualHash != manifest[position].ContentHash {
			missingCheckpointIDs = append(missingCheckpointIDs, manifest[position].ChunkID)
			if position < rewindTo {
				rewindTo = position
			}
		}
	}
	checkpointChunkID = 0
	if rewindTo > 0 {
		checkpointChunkID = manifest[rewindTo-1].ChunkID
	}
	sort.Slice(orphans, func(left, right int) bool { return orphans[left] < orphans[right] })
	sort.Slice(mismatched, func(left, right int) bool { return mismatched[left] < mismatched[right] })
	return vectorResumePlan{
		CheckpointChunkID: checkpointChunkID, CheckpointPoints: int64(rewindTo),
		MissingCheckpointIDs: missingCheckpointIDs, OrphanIDs: orphans, MismatchedIDs: mismatched,
	}, nil
}

func validateVectorCheckpoint(index VectorIndex, manifest []VectorManifestEntry) error {
	if index.CheckpointPoints < 0 || index.CheckpointPoints > int64(len(manifest)) {
		return fmt.Errorf("%w: checkpoint point count %d outside manifest size %d",
			ErrVectorIndexCorrupt, index.CheckpointPoints, len(manifest))
	}
	if index.CheckpointPoints == 0 {
		if index.CheckpointChunkID != 0 {
			return fmt.Errorf("%w: zero-point checkpoint has chunk id %d", ErrVectorIndexCorrupt, index.CheckpointChunkID)
		}
		return nil
	}
	expectedChunkID := manifest[index.CheckpointPoints-1].ChunkID
	if index.CheckpointChunkID != expectedChunkID {
		return fmt.Errorf("%w: checkpoint chunk id %d, want %d",
			ErrVectorIndexCorrupt, index.CheckpointChunkID, expectedChunkID)
	}
	return nil
}

func compareVectorPoints(manifest []VectorManifestEntry, actualManifest []VectorManifestEntry) ([]int64, []int64, []int64) {
	expected := make(map[int64]string, len(manifest))
	for _, entry := range manifest {
		expected[entry.ChunkID] = entry.ContentHash
	}
	actual := make(map[int64]string, len(actualManifest))
	orphans := make([]int64, 0)
	mismatched := make([]int64, 0)
	for _, point := range actualManifest {
		actual[point.ChunkID] = point.ContentHash
		expectedHash, ok := expected[point.ChunkID]
		if !ok {
			orphans = append(orphans, point.ChunkID)
		} else if point.ContentHash != expectedHash {
			mismatched = append(mismatched, point.ChunkID)
		}
	}
	missing := make([]int64, 0)
	for _, entry := range manifest {
		if _, ok := actual[entry.ChunkID]; !ok {
			missing = append(missing, entry.ChunkID)
		}
	}
	sort.Slice(orphans, func(left, right int) bool { return orphans[left] < orphans[right] })
	sort.Slice(mismatched, func(left, right int) bool { return mismatched[left] < mismatched[right] })
	return missing, orphans, mismatched
}

func vectorCollectionName(ingestionRunID string) string {
	digest := sha256.Sum256([]byte(ingestionRunID + "\x1f" + VectorIndexVersion))
	return "noteinsight_" + hex.EncodeToString(digest[:16])
}

// VectorCollectionName returns the deterministic Qdrant collection for an ingestion run.
func VectorCollectionName(ingestionRunID string) string {
	return vectorCollectionName(strings.TrimSpace(ingestionRunID))
}

func vectorManifestChecksum(index VectorIndex, manifest []VectorManifestEntry) string {
	hasher := sha256.New()
	writeVectorIndexIdentity(hasher, index)
	for _, entry := range manifest {
		_, _ = fmt.Fprintf(hasher, "%d\x1f%s\n", entry.ChunkID, entry.ContentHash)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeVectorIndexIdentity(hasher hash.Hash, index VectorIndex) {
	_, _ = fmt.Fprintf(hasher, "%s\x1f%s\x1f%s\x1f%s\x1f%d\n",
		index.IngestionRunID, index.IndexVersion, index.EmbeddingModel,
		index.EmbeddingRevision, index.VectorDimension)
}

func vectorPayload(chunk VectorChunk) map[string]any {
	payload := map[string]any{
		"chunk_id": chunk.ChunkID, "document_id": chunk.DocumentID,
		"project_id": chunk.ProjectID, "project_visibility": chunk.ProjectVisibility,
		"document_visibility": chunk.DocumentVisibility, "document_lifecycle": chunk.DocumentLifecycle,
		"document_type": chunk.DocumentType, "source_type": chunk.SourceType,
		"source_version": chunk.SourceVersion, "content_hash": chunk.ContentHash,
	}
	if chunk.SourceID != nil {
		payload["source_id"] = *chunk.SourceID
	}
	if chunk.NoteID != nil {
		payload["note_id"] = *chunk.NoteID
	}
	if chunk.MediaPosition != nil {
		payload["media_position"] = *chunk.MediaPosition
	}
	return payload
}

func newVectorLeaseToken() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate vector index lease token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func manifestIDSamples(manifest []VectorManifestEntry, limit int) []int64 {
	if limit > len(manifest) {
		limit = len(manifest)
	}
	result := make([]int64, 0, limit)
	for _, entry := range manifest[:limit] {
		result = append(result, entry.ChunkID)
	}
	return result
}

func int64Sample(values []int64, limit int) []int64 {
	if limit > len(values) {
		limit = len(values)
	}
	return append([]int64(nil), values[:limit]...)
}
