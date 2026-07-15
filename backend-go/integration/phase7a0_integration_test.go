//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/dataset"
)

func TestEvidenceVersionsAndDatasetSnapshotsAreImmutable(t *testing.T) {
	ctx := context.Background()
	registered := registerIntegrationUser(t, integrationAuthService())

	var projectID int64
	projectSlug := fmt.Sprintf("phase7a0-%d", time.Now().UnixNano())
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO projects (slug, name, visibility, status)
VALUES ($1, 'Phase 7A-0 integration', 'private', 'active')
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
VALUES ($1, $2, 'Versioned note', 'first body', 'study', 'project')
RETURNING id`, projectID, registered.User.ID).Scan(&noteID); err != nil {
		t.Fatal(err)
	}
	var mediaID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO note_media (note_id, media_type, url, caption, ocr_text, position)
VALUES ($1, 'image', 'memory://phase7a0', 'first caption', 'first ocr', 0)
RETURNING id`, noteID).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	var commentID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO note_comments (note_id, user_id, content)
VALUES ($1, $2, 'first comment')
RETURNING id`, noteID, registered.User.ID).Scan(&commentID); err != nil {
		t.Fatal(err)
	}

	var mediaVersion int64
	if err := integrationDB.QueryRowContext(ctx, `
UPDATE note_media SET caption='second caption' WHERE id=$1 RETURNING content_version`, mediaID).Scan(&mediaVersion); err != nil {
		t.Fatal(err)
	}
	if mediaVersion != 2 {
		t.Fatalf("media content version = %d, want 2", mediaVersion)
	}
	if err := integrationDB.QueryRowContext(ctx, `
UPDATE note_media SET caption='second caption', content_version=999 WHERE id=$1 RETURNING content_version`, mediaID).Scan(&mediaVersion); err != nil {
		t.Fatal(err)
	}
	if mediaVersion != 2 {
		t.Fatalf("manual media version override produced %d, want 2", mediaVersion)
	}
	assertEvidenceVersions(t, "note_media", mediaID, 2)
	assertPayloadTextVersions(t, "note_media", mediaID, "first caption", "second caption")

	var commentVersion int64
	if err := integrationDB.QueryRowContext(ctx, `
UPDATE note_comments SET content='second comment' WHERE id=$1 RETURNING content_version`, commentID).Scan(&commentVersion); err != nil {
		t.Fatal(err)
	}
	if commentVersion != 2 {
		t.Fatalf("comment content version = %d, want 2", commentVersion)
	}
	assertEvidenceVersions(t, "note_comment", commentID, 2)
	assertPayloadTextVersions(t, "note_comment", commentID, "first comment", "second comment")

	freezer := dataset.NewService(dataset.NewRepository(integrationDB))
	type freezeResult struct {
		snapshot dataset.Snapshot
		err      error
	}
	start := make(chan struct{})
	results := make(chan freezeResult, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			<-start
			snapshot, err := freezer.Freeze(ctx, datasetID, registered.User.ID)
			results <- freezeResult{snapshot: snapshot, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	firstPair := make([]dataset.Snapshot, 0, 2)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		firstPair = append(firstPair, result.snapshot)
	}
	if firstPair[0].ID != firstPair[1].ID {
		t.Fatalf("concurrent freezes created snapshots %d and %d", firstPair[0].ID, firstPair[1].ID)
	}
	if firstPair[0].Reused == firstPair[1].Reused {
		t.Fatalf("concurrent freeze reused flags = %v/%v, want one publisher and one reuse", firstPair[0].Reused, firstPair[1].Reused)
	}
	firstSnapshot := firstPair[0]
	if firstSnapshot.SourceCount != 3 || len(firstSnapshot.ManifestChecksum) != 64 {
		t.Fatalf("first snapshot source_count=%d checksum=%q", firstSnapshot.SourceCount, firstSnapshot.ManifestChecksum)
	}
	var evidenceXminBefore, evidenceXminAfter string
	if err := integrationDB.QueryRowContext(ctx, `
SELECT xmin::text FROM evidence_sources
WHERE source_type='note' AND source_id=$1 AND index_status<>'deleted'`, noteID).Scan(&evidenceXminBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `UPDATE notes SET view_count=view_count+1 WHERE id=$1`, noteID); err != nil {
		t.Fatal(err)
	}
	if err := integrationDB.QueryRowContext(ctx, `
SELECT xmin::text FROM evidence_sources
WHERE source_type='note' AND source_id=$1 AND index_status<>'deleted'`, noteID).Scan(&evidenceXminAfter); err != nil {
		t.Fatal(err)
	}
	if evidenceXminBefore != evidenceXminAfter {
		t.Fatalf("counter-only note update touched evidence source xmin %s -> %s", evidenceXminBefore, evidenceXminAfter)
	}

	var noteVersion int64
	if err := integrationDB.QueryRowContext(ctx, `
UPDATE notes SET category='career' WHERE id=$1 RETURNING content_version`, noteID).Scan(&noteVersion); err != nil {
		t.Fatal(err)
	}
	if noteVersion != 2 {
		t.Fatalf("note content version = %d, want 2", noteVersion)
	}
	assertEvidenceVersions(t, "note", noteID, 2)
	var oldCategory, newCategory string
	if err := integrationDB.QueryRowContext(ctx, `
SELECT old_payload.source_payload->>'category', new_payload.source_payload->>'category'
FROM evidence_sources old_source
JOIN evidence_source_payloads old_payload ON old_payload.evidence_source_id=old_source.id
JOIN evidence_sources new_source
  ON new_source.source_type=old_source.source_type
 AND new_source.source_id=old_source.source_id
 AND new_source.source_version=2
JOIN evidence_source_payloads new_payload ON new_payload.evidence_source_id=new_source.id
WHERE old_source.source_type='note' AND old_source.source_id=$1 AND old_source.source_version=1`, noteID).Scan(&oldCategory, &newCategory); err != nil {
		t.Fatal(err)
	}
	if oldCategory != "study" || newCategory != "career" {
		t.Fatalf("immutable note categories = %q/%q, want study/career", oldCategory, newCategory)
	}
	secondSnapshot, err := freezer.Freeze(ctx, datasetID, registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondSnapshot.ID == firstSnapshot.ID || secondSnapshot.Version != 2 || secondSnapshot.Reused {
		t.Fatalf("second snapshot = %+v, want a new version", secondSnapshot)
	}
	if secondSnapshot.ManifestChecksum == firstSnapshot.ManifestChecksum {
		t.Fatal("changed evidence produced the same dataset manifest checksum")
	}

	if _, err := integrationDB.ExecContext(ctx, `UPDATE dataset_versions SET source_count=0 WHERE id=$1`, firstSnapshot.ID); err == nil {
		t.Fatal("frozen dataset version update unexpectedly succeeded")
	}
	if _, err := integrationDB.ExecContext(ctx, `DELETE FROM dataset_version_sources WHERE dataset_version_id=$1`, firstSnapshot.ID); err == nil {
		t.Fatal("frozen dataset version source delete unexpectedly succeeded")
	}
	if _, err := integrationDB.ExecContext(ctx, `UPDATE datasets SET status='frozen' WHERE id=$1`, datasetID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `UPDATE dataset_notes SET note_version=999 WHERE dataset_id=$1`, datasetID); err == nil {
		t.Fatal("frozen dataset membership update unexpectedly succeeded")
	}
}

func assertPayloadTextVersions(t *testing.T, sourceType string, sourceID int64, oldText string, newText string) {
	t.Helper()
	rows, err := integrationDB.Query(`
SELECT esp.evidence_source_id, esp.canonical_text
FROM evidence_sources es
JOIN evidence_source_payloads esp ON esp.evidence_source_id=es.id
WHERE es.source_type=$1 AND es.source_id=$2
ORDER BY es.source_version`, sourceType, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	texts := make([]string, 0, 2)
	var payloadID int64
	for rows.Next() {
		var current string
		if err := rows.Scan(&payloadID, &current); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, current)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 || !strings.Contains(texts[0], oldText) || !strings.Contains(texts[1], newText) {
		t.Fatalf("%s/%d immutable payloads = %#v", sourceType, sourceID, texts)
	}
	if _, err := integrationDB.Exec(`UPDATE evidence_source_payloads SET canonical_text='tampered' WHERE evidence_source_id=$1`, payloadID); err == nil {
		t.Fatalf("%s/%d payload update unexpectedly succeeded", sourceType, sourceID)
	}
}

func assertEvidenceVersions(t *testing.T, sourceType string, sourceID int64, activeVersion int64) {
	t.Helper()
	rows, err := integrationDB.Query(`
SELECT source_version, index_status, deleted_at IS NOT NULL
FROM evidence_sources
WHERE source_type=$1 AND source_id=$2
ORDER BY source_version`, sourceType, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type state struct {
		version int64
		status  string
		deleted bool
	}
	states := make([]state, 0, 2)
	for rows.Next() {
		var current state
		if err := rows.Scan(&current.version, &current.status, &current.deleted); err != nil {
			t.Fatal(err)
		}
		states = append(states, current)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("%s/%d evidence states = %+v, want two versions", sourceType, sourceID, states)
	}
	if states[0].version != 1 || states[0].status != "deleted" || !states[0].deleted {
		t.Fatalf("old %s evidence state = %+v", sourceType, states[0])
	}
	if states[1].version != activeVersion || states[1].status != "pending" || states[1].deleted {
		t.Fatalf("active %s evidence state = %+v", sourceType, states[1])
	}
}
