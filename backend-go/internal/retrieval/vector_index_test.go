package retrieval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestVectorIndexerResumesFromDurableCheckpoint(t *testing.T) {
	repository := newFakeVectorIndexRepository(5)
	store := newFakeVectorStore()
	store.failUpsertCall = 2
	embedder := &trackingEmbedder{}
	indexer := newVectorIndexer(repository, embedder, store, VectorIndexOptions{
		EmbeddingModel: "embedding", EmbeddingRevision: "revision", VectorDimension: 3,
		BatchSize: 2, LeaseOwner: "test", LeaseDuration: time.Minute,
	})

	if _, err := indexer.Build(context.Background(), "run-1"); err == nil {
		t.Fatal("first build unexpectedly succeeded")
	}
	if repository.index.Status != "failed" || repository.index.CheckpointPoints != 2 || repository.index.CheckpointChunkID != 2 {
		t.Fatalf("failed checkpoint = %+v", repository.index)
	}
	if store.recreateCalls != 1 || len(store.points) != 2 {
		t.Fatalf("first attempt store state: recreates=%d points=%d", store.recreateCalls, len(store.points))
	}

	store.failUpsertCall = 0
	completed, err := indexer.Build(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.PointCount != 5 || completed.BuildAttempt != 2 {
		t.Fatalf("completed index = %+v", completed)
	}
	if store.recreateCalls != 1 {
		t.Fatalf("resume recreated collection %d times", store.recreateCalls)
	}
	if embedder.documentCounts["chunk-1"] != 1 || embedder.documentCounts["chunk-2"] != 1 {
		t.Fatalf("checkpointed chunks were embedded again: %+v", embedder.documentCounts)
	}
	if len(store.points) != 5 {
		t.Fatalf("stored points = %d, want 5", len(store.points))
	}

	beforeCalls := embedder.calls
	if _, err := indexer.Build(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	if embedder.calls != beforeCalls {
		t.Fatal("completed index rebuild called the embedder")
	}
}

func TestPlanVectorResumeRewindsMissingCheckpointAndFindsOrphan(t *testing.T) {
	manifest := []VectorManifestEntry{{ChunkID: 10, ContentHash: "a"}, {ChunkID: 20, ContentHash: "b"}, {ChunkID: 30, ContentHash: "c"}}
	actual := []VectorManifestEntry{{ChunkID: 10, ContentHash: "a"}, {ChunkID: 20, ContentHash: "stale"}, {ChunkID: 30, ContentHash: "c"}, {ChunkID: 999, ContentHash: "orphan"}}
	plan, err := planVectorResume(manifest, 30, 3, actual)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CheckpointChunkID != 10 || plan.CheckpointPoints != 1 {
		t.Fatalf("rewind checkpoint = %+v", plan)
	}
	if fmt.Sprint(plan.MissingCheckpointIDs) != "[20]" || fmt.Sprint(plan.OrphanIDs) != "[999]" || fmt.Sprint(plan.MismatchedIDs) != "[20]" {
		t.Fatalf("resume drift = %+v", plan)
	}
}

func TestVectorAuditRepairsOnlyOrphans(t *testing.T) {
	repository := newFakeVectorIndexRepository(3)
	identity := repository.identity()
	repository.index = identity
	repository.index.Status = "completed"
	repository.index.PointCount = 3
	repository.index.CheckpointPoints = 3
	repository.index.CheckpointChunkID = 3
	repository.index.IndexChecksum = vectorManifestChecksum(identity, repository.manifest)
	store := newFakeVectorStore()
	store.exists = true
	for _, pointID := range []int64{1, 2, 3, 99} {
		store.points[pointID] = VectorPoint{ID: pointID, Payload: map[string]any{"content_hash": fmt.Sprintf("hash-%d", pointID)}}
	}
	indexer := newVectorIndexer(repository, &trackingEmbedder{}, store, VectorIndexOptions{
		EmbeddingModel: "embedding", EmbeddingRevision: "revision", VectorDimension: 3,
		BatchSize: 2, LeaseOwner: "test", LeaseDuration: time.Minute,
	})

	report, err := indexer.Audit(context.Background(), "run-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Exact || report.OrphanPointCount != 1 || report.OrphansDeleted != 1 {
		t.Fatalf("audit report = %+v", report)
	}
	if _, exists := store.points[99]; exists {
		t.Fatal("orphan point was not removed")
	}
}

func TestCompletedVectorAuditRejectsMismatchedContentHash(t *testing.T) {
	repository := newFakeVectorIndexRepository(2)
	identity := repository.identity()
	repository.index = identity
	repository.index.Status = "completed"
	repository.index.PointCount = 2
	repository.index.CheckpointPoints = 2
	repository.index.CheckpointChunkID = 2
	repository.index.IndexChecksum = vectorManifestChecksum(identity, repository.manifest)
	store := newFakeVectorStore()
	store.exists = true
	store.points[1] = VectorPoint{ID: 1, Payload: map[string]any{"content_hash": "hash-1"}}
	store.points[2] = VectorPoint{ID: 2, Payload: map[string]any{"content_hash": "stale-hash"}}
	indexer := newVectorIndexer(repository, &trackingEmbedder{}, store, VectorIndexOptions{
		EmbeddingModel: "embedding", EmbeddingRevision: "revision", VectorDimension: 3,
		BatchSize: 2, LeaseOwner: "test", LeaseDuration: time.Minute,
	})

	report, err := indexer.Audit(context.Background(), "run-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Exact || report.MismatchedPointCount != 1 || fmt.Sprint(report.MismatchedPointSample) != "[2]" {
		t.Fatalf("audit report = %+v", report)
	}
	if _, exists := store.points[2]; !exists {
		t.Fatal("orphan repair deleted a mismatched expected point")
	}
}

type fakeVectorIndexRepository struct {
	index    VectorIndex
	manifest []VectorManifestEntry
	chunks   []VectorChunk
	token    string
}

func newFakeVectorIndexRepository(count int) *fakeVectorIndexRepository {
	repository := &fakeVectorIndexRepository{}
	for pointID := 1; pointID <= count; pointID++ {
		content := fmt.Sprintf("chunk-%d", pointID)
		repository.manifest = append(repository.manifest, VectorManifestEntry{ChunkID: int64(pointID), ContentHash: fmt.Sprintf("hash-%d", pointID)})
		repository.chunks = append(repository.chunks, VectorChunk{ChunkID: int64(pointID), Content: content, ContentHash: fmt.Sprintf("hash-%d", pointID)})
	}
	return repository
}

func (f *fakeVectorIndexRepository) identity() VectorIndex {
	return VectorIndex{
		IngestionRunID: "run-1", IndexVersion: VectorIndexVersion,
		EmbeddingModel: "embedding", EmbeddingRevision: "revision", VectorDimension: 3,
		DistanceMetric: "Cosine", CollectionName: vectorCollectionName("run-1"),
	}
}

func (f *fakeVectorIndexRepository) LoadVectorIndex(_ context.Context, _ string) (VectorIndex, error) {
	if f.index.IngestionRunID == "" {
		return VectorIndex{}, ErrIndexNotReady
	}
	return f.index, nil
}

func (f *fakeVectorIndexRepository) AcquireVectorIndex(_ context.Context, index VectorIndex, owner string, token string, _ time.Duration) (VectorIndex, error) {
	if f.index.Status == "completed" {
		return f.index, nil
	}
	if f.index.Status == "building" && f.token != "" {
		return VectorIndex{}, ErrIndexBuildLeased
	}
	if f.index.IngestionRunID == "" {
		f.index = index
	}
	f.index.Status = "building"
	f.index.BuildAttempt++
	f.index.LeaseOwner = owner
	f.token = token
	return f.index, nil
}

func (f *fakeVectorIndexRepository) SaveVectorIndexCheckpoint(_ context.Context, _ string, token string, chunkID int64, pointCount int64, orphanCount int64, missingCount int64, _ time.Duration, _ bool) error {
	if f.token != token || f.index.Status != "building" {
		return ErrIndexLeaseLost
	}
	f.index.CheckpointChunkID = chunkID
	f.index.CheckpointPoints = pointCount
	f.index.PointCount = pointCount
	f.index.OrphanPointCount = orphanCount
	f.index.MissingPointCount = missingCount
	return nil
}

func (f *fakeVectorIndexRepository) CompleteVectorIndex(_ context.Context, _ string, token string, pointCount int64, checksum string) (VectorIndex, error) {
	if f.token != token || f.index.Status != "building" {
		return VectorIndex{}, ErrIndexLeaseLost
	}
	f.index.Status = "completed"
	f.index.PointCount = pointCount
	f.index.CheckpointPoints = pointCount
	f.index.IndexChecksum = checksum
	f.index.LeaseOwner = ""
	f.token = ""
	return f.index, nil
}

func (f *fakeVectorIndexRepository) MarkVectorIndexFailed(_ string, token string, _ error) {
	if f.token == token && f.index.Status == "building" {
		f.index.Status = "failed"
		f.index.LeaseOwner = ""
		f.token = ""
	}
}

func (f *fakeVectorIndexRepository) ListVectorManifest(_ context.Context, _ string) ([]VectorManifestEntry, error) {
	return append([]VectorManifestEntry(nil), f.manifest...), nil
}

func (f *fakeVectorIndexRepository) ListVectorChunks(_ context.Context, _ string, afterChunkID int64, limit int) ([]VectorChunk, error) {
	result := make([]VectorChunk, 0, limit)
	for _, chunk := range f.chunks {
		if chunk.ChunkID > afterChunkID && len(result) < limit {
			result = append(result, chunk)
		}
	}
	return result, nil
}

type trackingEmbedder struct {
	calls          int
	documentCounts map[string]int
}

func (f *trackingEmbedder) EmbedDocuments(_ context.Context, inputs []string) ([][]float32, error) {
	f.calls++
	if f.documentCounts == nil {
		f.documentCounts = make(map[string]int)
	}
	vectors := make([][]float32, len(inputs))
	for index, input := range inputs {
		f.documentCounts[input]++
		vectors[index] = []float32{0.1, 0.2, 0.3}
	}
	return vectors, nil
}

func (f *trackingEmbedder) EmbedQuery(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

type fakeVectorStore struct {
	points         map[int64]VectorPoint
	exists         bool
	recreateCalls  int
	upsertCalls    int
	failUpsertCall int
}

func newFakeVectorStore() *fakeVectorStore {
	return &fakeVectorStore{points: make(map[int64]VectorPoint)}
}

func (f *fakeVectorStore) RecreateCollection(_ context.Context, _ string, _ int, _ map[string]any) error {
	f.exists = true
	f.recreateCalls++
	f.points = make(map[int64]VectorPoint)
	return nil
}

func (f *fakeVectorStore) CollectionExists(_ context.Context, _ string) (bool, error) {
	return f.exists, nil
}

func (f *fakeVectorStore) CreatePayloadIndex(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (f *fakeVectorStore) Upsert(_ context.Context, _ string, points []VectorPoint) error {
	f.upsertCalls++
	if f.failUpsertCall > 0 && f.upsertCalls == f.failUpsertCall {
		return errors.New("injected upsert failure")
	}
	for _, point := range points {
		f.points[point.ID] = point
	}
	return nil
}

func (f *fakeVectorStore) Query(_ context.Context, _ string, _ []float32, _ map[string]any, _ int) ([]VectorHit, error) {
	return nil, nil
}

func (f *fakeVectorStore) Count(_ context.Context, _ string) (int64, error) {
	return int64(len(f.points)), nil
}

func (f *fakeVectorStore) ListPointManifest(_ context.Context, _ string) ([]VectorManifestEntry, error) {
	result := make([]VectorManifestEntry, 0, len(f.points))
	for pointID, point := range f.points {
		contentHash, _ := point.Payload["content_hash"].(string)
		result = append(result, VectorManifestEntry{ChunkID: pointID, ContentHash: contentHash})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ChunkID < result[right].ChunkID })
	return result, nil
}

func (f *fakeVectorStore) DeletePoints(_ context.Context, _ string, pointIDs []int64) error {
	for _, pointID := range pointIDs {
		delete(f.points, pointID)
	}
	return nil
}
