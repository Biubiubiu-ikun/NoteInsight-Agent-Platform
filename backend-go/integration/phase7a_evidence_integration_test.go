//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/dataset"
	"creatorinsight/backend-go/internal/evidence"
)

func TestEvidenceIngestionIsDeterministicVersionedAndDeletionAware(t *testing.T) {
	ctx := context.Background()
	registered := registerIntegrationUser(t, integrationAuthService())
	projectSlug := fmt.Sprintf("phase7a-%d", time.Now().UnixNano())
	var projectID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO projects (slug, name, visibility, status)
VALUES ($1, 'Phase 7A evidence integration', 'private', 'active')
RETURNING id`, projectSlug).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	var datasetID int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT id FROM datasets WHERE project_id=$1 AND slug='community'`, projectID).Scan(&datasetID); err != nil {
		t.Fatal(err)
	}
	var noteID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO notes (project_id, author_id, title, body, category, visibility)
VALUES ($1,$2,'Evidence note','first evidence body','study','project')
RETURNING id`, projectID, registered.User.ID).Scan(&noteID); err != nil {
		t.Fatal(err)
	}
	var mediaID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO note_media (note_id, media_type, url, caption, ocr_text, position)
VALUES ($1,'image','memory://phase7a','media caption','OCR 精确文本',0)
RETURNING id`, noteID).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	commentIDs := make([]int64, 0, 2)
	for index := 0; index < 2; index++ {
		var commentID int64
		if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO note_comments (note_id, user_id, content, sentiment, intent, topic_id)
VALUES ($1,$2,$3,'positive','question',1)
RETURNING id`, noteID, registered.User.ID, fmt.Sprintf("comment evidence %d", index)).Scan(&commentID); err != nil {
			t.Fatal(err)
		}
		commentIDs = append(commentIDs, commentID)
	}

	factRunID := fmt.Sprintf("phase7a_facts_%d", time.Now().UnixNano())
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO fact_materialization_runs (
  run_id, window_start, window_end, status, note_fact_count,
  user_fact_count, started_at, completed_at
) VALUES ($1,'2026-07-01T00:00:00Z','2026-07-02T00:00:00Z','completed',1,1,now(),now())`, factRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO note_daily_facts (
  project_id,note_id,fact_date,view_count,like_count,collect_count,
  comment_count,share_count,unique_user_count,event_count,source_run_id
) VALUES ($1,$2,'2026-07-01',10,3,2,2,1,8,18,$3)`, projectID, noteID, factRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO user_daily_facts (
  project_id,user_id,fact_date,view_count,interaction_count,content_count,
  comment_count,active_note_count,event_count,source_run_id
) VALUES ($1,$2,'2026-07-01',10,8,1,2,1,22,$3)`, projectID, registered.User.ID, factRunID); err != nil {
		t.Fatal(err)
	}

	freezer := dataset.NewService(dataset.NewRepository(integrationDB))
	firstSnapshot, err := freezer.Freeze(ctx, datasetID, registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := evidence.NewService(evidence.NewRepository(integrationDB))
	firstRunID := fmt.Sprintf("phase7a_ingest_%d", time.Now().UnixNano())
	firstRun, err := service.Ingest(ctx, evidence.IngestRequest{
		RunID: firstRunID, DatasetVersionID: firstSnapshot.ID, Mode: "incremental",
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstRun.Status != "completed" || firstRun.DocumentCount != 5 || firstRun.ReusedDocumentCount != 0 {
		t.Fatalf("first ingestion = %+v, want five newly created documents", firstRun)
	}
	assertEvidenceCitationSlices(t, firstRun.RunID)
	audit, err := service.Audit(ctx, firstRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Healthy {
		t.Fatalf("first ingestion audit = %+v", audit)
	}

	secondRunID := fmt.Sprintf("phase7a_rebuild_%d", time.Now().UnixNano())
	secondRun, err := service.Ingest(ctx, evidence.IngestRequest{
		RunID: secondRunID, DatasetVersionID: firstSnapshot.ID, Mode: "rebuild",
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondRun.OutputChecksum != firstRun.OutputChecksum || secondRun.DocumentCount != firstRun.DocumentCount ||
		secondRun.ReusedDocumentCount != secondRun.DocumentCount {
		t.Fatalf("deterministic rebuild mismatch: first=%+v second=%+v", firstRun, secondRun)
	}
	var uniqueDocuments int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*) FROM evidence_documents
WHERE dataset_version_id=$1 AND parser_version=$2`, firstSnapshot.ID, evidence.ParserVersion).Scan(&uniqueDocuments); err != nil {
		t.Fatal(err)
	}
	if uniqueDocuments != firstRun.DocumentCount {
		t.Fatalf("rebuild created duplicate documents: got %d want %d", uniqueDocuments, firstRun.DocumentCount)
	}

	retryRunID := fmt.Sprintf("phase7a_retry_%d", time.Now().UnixNano())
	repository := evidence.NewRepository(integrationDB)
	if _, err := repository.BeginRun(ctx, evidence.IngestRequest{
		RunID: retryRunID, DatasetVersionID: firstSnapshot.ID, Mode: "incremental",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkFailed(ctx, retryRunID, fmt.Errorf("injected failure")); err != nil {
		t.Fatal(err)
	}
	retried, err := service.Ingest(ctx, evidence.IngestRequest{
		RunID: retryRunID, DatasetVersionID: firstSnapshot.ID, Mode: "incremental",
	})
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != "completed" || retried.OutputChecksum != firstRun.OutputChecksum {
		t.Fatalf("retried run = %+v", retried)
	}
	if _, err := integrationDB.ExecContext(ctx, `DELETE FROM ingestion_run_documents WHERE run_id=$1`, firstRunID); err == nil {
		t.Fatal("completed ingestion run document lineage was mutable")
	}

	if _, err := integrationDB.ExecContext(ctx, `UPDATE notes SET body='second evidence body' WHERE id=$1`, noteID); err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := freezer.Freeze(ctx, datasetID, registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondSnapshot.ID == firstSnapshot.ID {
		t.Fatal("note update did not create a new dataset snapshot")
	}
	versionedRun, err := service.Ingest(ctx, evidence.IngestRequest{
		RunID:            fmt.Sprintf("phase7a_version_%d", time.Now().UnixNano()),
		DatasetVersionID: secondSnapshot.ID,
		Mode:             "incremental",
	})
	if err != nil {
		t.Fatal(err)
	}
	var noteVersions, readyNoteVersions int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*), COUNT(*) FILTER (WHERE lifecycle_status='ready')
FROM evidence_documents
WHERE source_type='note' AND source_id=$1`, noteID).Scan(&noteVersions, &readyNoteVersions); err != nil {
		t.Fatal(err)
	}
	if noteVersions != 2 || readyNoteVersions != 1 {
		t.Fatalf("note evidence versions total/ready = %d/%d, want 2/1", noteVersions, readyNoteVersions)
	}
	assertEvidenceCitationSlices(t, versionedRun.RunID)

	if _, err := integrationDB.ExecContext(ctx, `
UPDATE note_comments SET status=0, deleted_at=now() WHERE id=$1`, commentIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var activeDeletedCommentCitations int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM source_citations sc
JOIN evidence_documents d ON d.id=sc.document_id
WHERE sc.source_type='note_comment' AND sc.source_id=$1
  AND d.lifecycle_status='ready'`, commentIDs[0]).Scan(&activeDeletedCommentCitations); err != nil {
		t.Fatal(err)
	}
	if activeDeletedCommentCitations != 0 {
		t.Fatalf("deleted comment has %d active citations", activeDeletedCommentCitations)
	}

	var factVersion int64
	if err := integrationDB.QueryRowContext(ctx, `
UPDATE note_daily_facts SET view_count=view_count+1
WHERE project_id=$1 AND note_id=$2 AND fact_date='2026-07-01'
RETURNING content_version`, projectID, noteID).Scan(&factVersion); err != nil {
		t.Fatal(err)
	}
	if factVersion != 2 {
		t.Fatalf("daily fact content_version = %d, want 2", factVersion)
	}
	var factPayloads int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*) FROM daily_fact_payloads
WHERE project_id=$1 AND fact_type='note_daily_fact' AND subject_id=$2`, projectID, noteID).Scan(&factPayloads); err != nil {
		t.Fatal(err)
	}
	if factPayloads != 2 {
		t.Fatalf("daily fact payload versions = %d, want 2", factPayloads)
	}
}

func assertEvidenceCitationSlices(t *testing.T, runID string) {
	t.Helper()
	var mismatches int64
	if err := integrationDB.QueryRow(`
SELECT COUNT(*)
FROM ingestion_run_documents rd
JOIN evidence_documents d ON d.id=rd.document_id
JOIN source_citations sc ON sc.document_id=d.id
JOIN evidence_source_payloads esp ON esp.evidence_source_id=sc.evidence_source_id
WHERE rd.run_id=$1 AND (
  substring(convert_to(d.canonical_text,'UTF8')
    FROM sc.document_start_byte+1 FOR sc.document_end_byte-sc.document_start_byte)
  <>
  substring(convert_to(replace(replace(esp.canonical_text,E'\r\n',E'\n'),E'\r',E'\n'),'UTF8')
    FROM sc.source_start_byte+1 FOR sc.source_end_byte-sc.source_start_byte)
)`, runID).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Fatalf("run %s has %d citation source slice mismatches", runID, mismatches)
	}
}
