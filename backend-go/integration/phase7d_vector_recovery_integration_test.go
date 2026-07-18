//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/dataset"
	"creatorinsight/backend-go/internal/evidence"
	"creatorinsight/backend-go/internal/retrieval"
)

func TestVectorIndexLeaseAndCheckpointPersistence(t *testing.T) {
	ctx := context.Background()
	registered := registerIntegrationUser(t, integrationAuthService())
	var projectID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO projects (slug,name,visibility,status)
VALUES ($1,'Phase 7D vector recovery','private','active')
RETURNING id`, fmt.Sprintf("phase7d-vector-%d", time.Now().UnixNano())).Scan(&projectID); err != nil {
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
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO notes (project_id,author_id,title,body,category,visibility)
VALUES ($1,$2,'断点恢复测试','第一批写入成功后，第二个进程不能抢占仍然有效的租约。','study','project')`,
		projectID, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dataset.NewService(dataset.NewRepository(integrationDB)).Freeze(ctx, datasetID, registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	ingestionRunID := fmt.Sprintf("phase7d_recovery_%d", time.Now().UnixNano())
	if _, err := evidence.NewService(evidence.NewRepository(integrationDB)).Ingest(ctx, evidence.IngestRequest{
		RunID: ingestionRunID, DatasetVersionID: snapshot.ID, Mode: "incremental",
	}); err != nil {
		t.Fatal(err)
	}

	repository := retrieval.NewRepository(integrationDB)
	manifest, err := repository.ListVectorManifest(ctx, ingestionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) == 0 {
		t.Fatal("test ingestion produced no vector chunks")
	}
	identity := retrieval.VectorIndex{
		IngestionRunID: ingestionRunID, IndexVersion: retrieval.VectorIndexVersion,
		EmbeddingModel: "integration-embedding", EmbeddingRevision: "recovery-v1",
		VectorDimension: 3, DistanceMetric: "Cosine",
		CollectionName: retrieval.VectorCollectionName(ingestionRunID),
	}
	first, err := repository.AcquireVectorIndex(ctx, identity, "worker-a", "lease-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "building" || first.BuildAttempt != 1 {
		t.Fatalf("first lease = %+v", first)
	}
	if _, err := repository.AcquireVectorIndex(ctx, identity, "worker-b", "lease-b", time.Minute); !errors.Is(err, retrieval.ErrIndexBuildLeased) {
		t.Fatalf("concurrent lease error = %v", err)
	}
	if err := repository.SaveVectorIndexCheckpoint(
		ctx, ingestionRunID, "lease-a", manifest[0].ChunkID, 1, 0, 0, time.Minute, true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE retrieval_vector_indexes
SET lease_expires_at=clock_timestamp()-interval '1 second'
WHERE ingestion_run_id=$1 AND lease_token='lease-a'`, ingestionRunID); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveVectorIndexCheckpoint(
		ctx, ingestionRunID, "lease-a", manifest[0].ChunkID, 1, 0, 0, time.Minute, false,
	); !errors.Is(err, retrieval.ErrIndexLeaseLost) {
		t.Fatalf("expired lease checkpoint error = %v", err)
	}

	resumed, err := repository.AcquireVectorIndex(ctx, identity, "worker-b", "lease-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.BuildAttempt != 2 || resumed.CheckpointPoints != 1 || resumed.CheckpointChunkID != manifest[0].ChunkID {
		t.Fatalf("resumed lease = %+v", resumed)
	}
	if err := repository.SaveVectorIndexCheckpoint(
		ctx, ingestionRunID, "lease-a", manifest[0].ChunkID, 1, 0, 0, time.Minute, false,
	); !errors.Is(err, retrieval.ErrIndexLeaseLost) {
		t.Fatalf("stale lease checkpoint error = %v", err)
	}
	repository.MarkVectorIndexFailed(ingestionRunID, "lease-b", errors.New("test cleanup"))
	failed, err := repository.LoadVectorIndex(ctx, ingestionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.LeaseOwner != "" || failed.CheckpointPoints != 1 {
		t.Fatalf("released recovery row = %+v", failed)
	}
}
