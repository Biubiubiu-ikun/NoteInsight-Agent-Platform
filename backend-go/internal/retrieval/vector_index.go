package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"log/slog"
	"strings"
	"time"
)

type VectorIndexOptions struct {
	EmbeddingModel    string
	EmbeddingRevision string
	VectorDimension   int
	BatchSize         int
	Logger            *slog.Logger
}

type VectorIndexer struct {
	repository *Repository
	embedder   Embedder
	store      VectorStore
	options    VectorIndexOptions
}

func NewVectorIndexer(repository *Repository, embedder Embedder, store VectorStore, options VectorIndexOptions) *VectorIndexer {
	return &VectorIndexer{repository: repository, embedder: embedder, store: store, options: options}
}

func (i *VectorIndexer) Build(ctx context.Context, ingestionRunID string) (index VectorIndex, err error) {
	ingestionRunID = strings.TrimSpace(ingestionRunID)
	if ingestionRunID == "" || i.embedder == nil || i.store == nil || i.options.BatchSize <= 0 || i.options.VectorDimension <= 0 {
		return VectorIndex{}, fmt.Errorf("%w: incomplete vector index configuration", ErrInvalidInput)
	}
	if existing, loadErr := i.repository.LoadVectorIndex(ctx, ingestionRunID); loadErr == nil && existing.Status == "completed" {
		if existing.IndexVersion != VectorIndexVersion || existing.EmbeddingModel != i.options.EmbeddingModel || existing.EmbeddingRevision != i.options.EmbeddingRevision || existing.VectorDimension != i.options.VectorDimension {
			return VectorIndex{}, ErrIndexVersionMismatch
		}
		return existing, nil
	}
	collection := vectorCollectionName(ingestionRunID)
	logger := i.options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	startedAt := time.Now()
	logger.Info("vector index build started", "ingestion_run_id", ingestionRunID, "collection", collection,
		"embedding_model", i.options.EmbeddingModel, "batch_size", i.options.BatchSize)
	index = VectorIndex{
		IngestionRunID: ingestionRunID, IndexVersion: VectorIndexVersion,
		EmbeddingModel: i.options.EmbeddingModel, EmbeddingRevision: i.options.EmbeddingRevision,
		VectorDimension: i.options.VectorDimension, DistanceMetric: "Cosine", CollectionName: collection,
	}
	if err = i.repository.StartVectorIndex(ctx, index); err != nil {
		return VectorIndex{}, err
	}
	defer func() {
		if err != nil {
			i.repository.MarkVectorIndexFailed(ingestionRunID, err)
		}
	}()
	metadata := map[string]any{
		"ingestion_run_id": ingestionRunID, "index_version": VectorIndexVersion,
		"embedding_model": i.options.EmbeddingModel, "embedding_revision": i.options.EmbeddingRevision,
	}
	if err = i.store.RecreateCollection(ctx, collection, i.options.VectorDimension, metadata); err != nil {
		return VectorIndex{}, err
	}
	for field, schema := range map[string]string{
		"project_id": "integer", "project_visibility": "keyword", "document_visibility": "keyword",
		"document_type": "keyword", "source_type": "keyword", "document_lifecycle": "keyword",
		"note_id": "integer",
	} {
		if err = i.store.CreatePayloadIndex(ctx, collection, field, schema); err != nil {
			return VectorIndex{}, err
		}
	}
	hasher := sha256.New()
	writeVectorIndexIdentity(hasher, index)
	var pointCount int64
	var afterChunkID int64
	var nextProgressLog int64 = 1000
	for {
		var chunks []VectorChunk
		chunks, err = i.repository.ListVectorChunks(ctx, ingestionRunID, afterChunkID, i.options.BatchSize)
		if err != nil {
			return VectorIndex{}, err
		}
		if len(chunks) == 0 {
			break
		}
		inputs := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			inputs = append(inputs, chunk.Content)
		}
		var vectors [][]float32
		vectors, err = i.embedder.EmbedDocuments(ctx, inputs)
		if err != nil {
			return VectorIndex{}, err
		}
		if len(vectors) != len(chunks) {
			return VectorIndex{}, fmt.Errorf("embedding count mismatch: vectors=%d chunks=%d", len(vectors), len(chunks))
		}
		points := make([]VectorPoint, 0, len(chunks))
		for offset, chunk := range chunks {
			points = append(points, VectorPoint{ID: chunk.ChunkID, Vector: vectors[offset], Payload: vectorPayload(chunk)})
			_, _ = fmt.Fprintf(hasher, "%d\x1f%s\n", chunk.ChunkID, chunk.ContentHash)
			afterChunkID = chunk.ChunkID
			pointCount++
		}
		if err = i.store.Upsert(ctx, collection, points); err != nil {
			return VectorIndex{}, err
		}
		if pointCount >= nextProgressLog {
			elapsed := time.Since(startedAt)
			logger.Info("vector index build progress", "ingestion_run_id", ingestionRunID,
				"points", pointCount, "last_chunk_id", afterChunkID, "elapsed", elapsed.Round(time.Second),
				"points_per_second", float64(pointCount)/elapsed.Seconds())
			nextProgressLog = ((pointCount / 1000) + 1) * 1000
		}
	}
	storedCount, err := i.store.Count(ctx, collection)
	if err != nil {
		return VectorIndex{}, err
	}
	if storedCount != pointCount {
		return VectorIndex{}, fmt.Errorf("vector point count mismatch: stored=%d expected=%d", storedCount, pointCount)
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	index, err = i.repository.CompleteVectorIndex(ctx, ingestionRunID, pointCount, checksum)
	if err != nil {
		return VectorIndex{}, err
	}
	logger.Info("vector index build completed", "ingestion_run_id", ingestionRunID, "points", pointCount,
		"elapsed", time.Since(startedAt).Round(time.Second), "manifest_checksum", checksum)
	return index, nil
}

func vectorCollectionName(ingestionRunID string) string {
	digest := sha256.Sum256([]byte(ingestionRunID + "\x1f" + VectorIndexVersion))
	return "noteinsight_" + hex.EncodeToString(digest[:16])
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
