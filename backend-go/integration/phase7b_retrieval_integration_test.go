//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/dataset"
	"creatorinsight/backend-go/internal/evidence"
	"creatorinsight/backend-go/internal/retrieval"
)

func TestRetrievalAuthorizationHistoricalSnapshotAndDeletionBoundary(t *testing.T) {
	ctx := context.Background()
	registered := registerIntegrationUser(t, integrationAuthService())
	var projectID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO projects (slug,name,visibility,status)
VALUES ($1,'Phase 7B private retrieval','private','active')
RETURNING id`, fmt.Sprintf("phase7b-%d", time.Now().UnixNano())).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO project_members (project_id,user_id,role,status)
VALUES ($1,$2,'owner','active')`, projectID, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	var datasetID int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT id FROM datasets WHERE project_id=$1 AND slug='community'`, projectID).Scan(&datasetID); err != nil {
		t.Fatal(err)
	}
	var noteID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO notes (project_id,author_id,title,body,category,visibility)
VALUES ($1,$2,'绝密星轨检索边界','第一版本只允许项目成员查看，校验词是蓝晶序列。','study','project')
RETURNING id`, projectID, registered.User.ID).Scan(&noteID); err != nil {
		t.Fatal(err)
	}

	freezer := dataset.NewService(dataset.NewRepository(integrationDB))
	snapshot, err := freezer.Freeze(ctx, datasetID, registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceService := evidence.NewService(evidence.NewRepository(integrationDB))
	ingestionRunID := fmt.Sprintf("phase7b_ingest_%d", time.Now().UnixNano())
	if _, err := evidenceService.Ingest(ctx, evidence.IngestRequest{
		RunID: ingestionRunID, DatasetVersionID: snapshot.ID, Mode: "incremental",
	}); err != nil {
		t.Fatal(err)
	}
	retrievalService := retrieval.NewService(retrieval.NewRepository(integrationDB))
	index, err := retrievalService.BuildLexicalIndex(ctx, ingestionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if index.Status != "completed" || !index.CompletedAt.After(index.StartedAt) {
		t.Fatalf("lexical index timing/status = %+v", index)
	}
	vectorBackend := &fakeVectorBackend{}
	vectorIndex, err := retrieval.NewVectorIndexer(
		retrieval.NewRepository(integrationDB), vectorBackend, vectorBackend,
		retrieval.VectorIndexOptions{
			EmbeddingModel: "integration-embedding", EmbeddingRevision: "immutable-test-revision",
			VectorDimension: 3, BatchSize: 2,
		},
	).Build(ctx, ingestionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if vectorIndex.Status != "completed" || vectorIndex.PointCount == 0 || vectorIndex.IndexChecksum == "" {
		t.Fatalf("vector index result = %+v", vectorIndex)
	}
	if err := retrievalService.EnableVector(vectorBackend, vectorBackend); err != nil {
		t.Fatal(err)
	}

	searchInput := retrieval.SearchInput{
		ProjectID: projectID, DatasetVersionID: snapshot.ID, IngestionRunID: ingestionRunID,
		Query: "绝密星轨检索边界的蓝晶序列是什么", Limit: 5,
	}
	authorized, err := retrievalService.Search(ctx, retrieval.Principal{UserID: registered.User.ID}, searchInput)
	if err != nil {
		t.Fatal(err)
	}
	assertRetrievedNoteWithVerifiedCitation(t, authorized, noteID)
	vectorInput := searchInput
	vectorInput.Mode = retrieval.ModeVector
	vectorAuthorized, err := retrievalService.Search(ctx, retrieval.Principal{UserID: registered.User.ID}, vectorInput)
	if err != nil {
		t.Fatal(err)
	}
	assertRetrievedNoteWithVerifiedCitation(t, vectorAuthorized, noteID)

	queryCallsBeforeUnauthorized := vectorBackend.queryCalls
	unauthorized, err := retrievalService.Search(ctx, retrieval.Principal{}, searchInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(unauthorized.Results) != 0 || unauthorized.CandidateCount != 0 || len(unauthorized.Query.IndexedTerms) != 0 ||
		unauthorized.Scope.IngestionOutputChecksum != "" || unauthorized.Scope.LexicalIndexChecksum != "" {
		t.Fatalf("unauthorized retrieval leaked evidence metadata: %+v", unauthorized)
	}
	vectorUnauthorized, err := retrievalService.Search(ctx, retrieval.Principal{}, vectorInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectorUnauthorized.Results) != 0 || vectorUnauthorized.Scope.VectorIndexChecksum != "" || vectorBackend.queryCalls != queryCallsBeforeUnauthorized {
		t.Fatalf("unauthorized vector retrieval reached dependency or leaked metadata: response=%+v calls=%d", vectorUnauthorized, vectorBackend.queryCalls)
	}

	if _, err := integrationDB.ExecContext(ctx, `
UPDATE notes SET body='第二版本仍为项目内容，校验词改为赤金序列。' WHERE id=$1`, noteID); err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := freezer.Freeze(ctx, datasetID, registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceService.Ingest(ctx, evidence.IngestRequest{
		RunID:            fmt.Sprintf("phase7b_ingest_v2_%d", time.Now().UnixNano()),
		DatasetVersionID: secondSnapshot.ID, Mode: "incremental",
	}); err != nil {
		t.Fatal(err)
	}
	historical, err := retrievalService.Search(ctx, retrieval.Principal{UserID: registered.User.ID}, searchInput)
	if err != nil {
		t.Fatal(err)
	}
	assertRetrievedNoteWithVerifiedCitation(t, historical, noteID)

	if _, err := integrationDB.ExecContext(ctx, `
UPDATE notes SET status='deleted', deleted_at=clock_timestamp() WHERE id=$1`, noteID); err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceService.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	deleted, err := retrievalService.Search(ctx, retrieval.Principal{UserID: registered.User.ID}, searchInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Results) != 0 {
		t.Fatalf("deleted source remained retrievable from historical run: %+v", deleted.Results)
	}
	vectorDeleted, err := retrievalService.Search(ctx, retrieval.Principal{UserID: registered.User.ID}, vectorInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectorDeleted.Results) != 0 {
		t.Fatalf("stale vector points bypassed PostgreSQL deletion boundary: %+v", vectorDeleted.Results)
	}
}

type fakeVectorBackend struct {
	points     []retrieval.VectorPoint
	queryCalls int
}

func (f *fakeVectorBackend) EmbedDocuments(_ context.Context, inputs []string) ([][]float32, error) {
	vectors := make([][]float32, len(inputs))
	for index := range vectors {
		vectors[index] = []float32{0.1, 0.2, 0.3}
	}
	return vectors, nil
}

func (f *fakeVectorBackend) EmbedQuery(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (f *fakeVectorBackend) RecreateCollection(_ context.Context, _ string, _ int, _ map[string]any) error {
	f.points = nil
	return nil
}

func (f *fakeVectorBackend) CreatePayloadIndex(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func (f *fakeVectorBackend) Upsert(_ context.Context, _ string, points []retrieval.VectorPoint) error {
	f.points = append(f.points, points...)
	return nil
}

func (f *fakeVectorBackend) Query(_ context.Context, _ string, _ []float32, _ map[string]any, limit int) ([]retrieval.VectorHit, error) {
	f.queryCalls++
	if limit > len(f.points) {
		limit = len(f.points)
	}
	hits := make([]retrieval.VectorHit, 0, limit)
	for _, point := range f.points[:limit] {
		hits = append(hits, retrieval.VectorHit{ID: point.ID, Score: 0.9, Payload: point.Payload})
	}
	return hits, nil
}

func (f *fakeVectorBackend) Count(_ context.Context, _ string) (int64, error) {
	return int64(len(f.points)), nil
}

func assertRetrievedNoteWithVerifiedCitation(t *testing.T, response retrieval.SearchResponse, noteID int64) {
	t.Helper()
	if len(response.Results) == 0 || response.Results[0].NoteID == nil || *response.Results[0].NoteID != noteID {
		t.Fatalf("top retrieval result does not reference note %d: %+v", noteID, response.Results)
	}
	if len(response.Results[0].Citations) == 0 {
		t.Fatal("retrieval result has no citation")
	}
	citation := response.Results[0].Citations[0]
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(citation.Quote)))
	if actual != citation.QuoteHash {
		t.Fatalf("citation quote hash = %s, want %s", actual, citation.QuoteHash)
	}
	if citation.DocumentEndByte <= citation.DocumentStartByte || citation.SourceEndByte <= citation.SourceStartByte {
		t.Fatalf("citation offsets are invalid: %+v", citation)
	}
}
