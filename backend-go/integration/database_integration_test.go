//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/note"
	"creatorinsight/backend-go/internal/outbox"
	"creatorinsight/backend-go/internal/platform/messaging"
	"creatorinsight/backend-go/internal/worker"
)

func TestConcurrentLikeAndDuplicateEventApplicationRemainIdempotent(t *testing.T) {
	authService := integrationAuthService()
	registered := registerIntegrationUser(t, authService)
	noteRepository := note.NewRepository(integrationDB)
	noteService := note.NewService(noteRepository)
	created, err := noteService.CreateNote(context.Background(), note.CreateNoteInput{
		AuthorID:   registered.User.ID,
		ProjectID:  1,
		Title:      "Integration idempotency note",
		Body:       "This note exists to verify PostgreSQL uniqueness and worker idempotency.",
		Category:   "study",
		Visibility: "public",
	})
	if err != nil {
		t.Fatal(err)
	}

	const callers = 24
	start := make(chan struct{})
	results := make(chan note.IdempotentActionResult, callers)
	errorsChannel := make(chan error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			<-start
			result, err := noteRepository.LikeNote(context.Background(), created.ID, registered.User.ID)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)

	applied := 0
	for result := range results {
		if result.Applied {
			applied++
		}
	}
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	if applied != 1 {
		t.Fatalf("applied likes = %d, want 1", applied)
	}
	var likeFacts, likeOutbox int
	if err := integrationDB.QueryRow(`SELECT COUNT(*) FROM note_likes WHERE note_id=$1`, created.ID).Scan(&likeFacts); err != nil {
		t.Fatal(err)
	}
	if err := integrationDB.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='note.liked'`, created.ID).Scan(&likeOutbox); err != nil {
		t.Fatal(err)
	}
	if likeFacts != 1 || likeOutbox != 1 {
		t.Fatalf("like facts=%d outbox=%d, want 1/1", likeFacts, likeOutbox)
	}

	payload, _ := json.Marshal(map[string]any{"project_id": 1, "user_id": registered.User.ID, "note_id": created.ID})
	envelope := messaging.EventEnvelope{
		EventID:       fmt.Sprintf("integration-like-%d", created.ID),
		EventType:     "note.liked",
		AggregateType: "note",
		AggregateID:   created.ID,
		SchemaVersion: 1,
		Producer:      "integration-test",
		Payload:       payload,
		OccurredAt:    time.Now().UTC(),
	}
	workerRepository := worker.NewRepository(integrationDB)
	application := worker.EventApplication{
		Envelope:  envelope,
		ProjectID: 1,
		UserID:    registered.User.ID,
		NoteID:    created.ID,
		EventType: "note_liked",
	}
	firstDuplicate, err := workerRepository.ApplyEvent(context.Background(), "integration-worker", application)
	if err != nil {
		t.Fatal(err)
	}
	secondDuplicate, err := workerRepository.ApplyEvent(context.Background(), "integration-worker", application)
	if err != nil {
		t.Fatal(err)
	}
	if firstDuplicate || !secondDuplicate {
		t.Fatalf("duplicate flags = %v/%v, want false/true", firstDuplicate, secondDuplicate)
	}
	var behaviorCount, materializedLikes int
	if err := integrationDB.QueryRow(`SELECT COUNT(*) FROM behavior_events WHERE source_event_id=$1`, envelope.EventID).Scan(&behaviorCount); err != nil {
		t.Fatal(err)
	}
	if err := integrationDB.QueryRow(`SELECT like_count FROM notes WHERE id=$1`, created.ID).Scan(&materializedLikes); err != nil {
		t.Fatal(err)
	}
	if behaviorCount != 1 || materializedLikes != 1 {
		t.Fatalf("behavior=%d materialized_likes=%d, want 1/1", behaviorCount, materializedLikes)
	}
}

func TestOutboxLeaseRecoveryAndTransactionRollback(t *testing.T) {
	ctx := context.Background()
	transaction, err := integrationDB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.EnqueueTx(ctx, transaction, outbox.EventInput{
		AggregateType: "note",
		AggregateID:   999,
		EventType:     "integration.rolled_back",
		Payload:       map[string]any{"value": "must not commit"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	var rolledBack int
	if err := integrationDB.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE event_type='integration.rolled_back'`).Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack != 0 {
		t.Fatalf("rolled back outbox rows = %d, want 0", rolledBack)
	}

	for index := 0; index < 6; index++ {
		_, err := integrationDB.Exec(`
INSERT INTO outbox_events (event_id, aggregate_type, aggregate_id, event_type, payload, status, next_retry_at, created_at, updated_at)
VALUES ($1, 'note', $2, 'integration.lease', '{}', 'pending', now(), now(), now())`,
			fmt.Sprintf("integration-lease-%d-%d", time.Now().UnixNano(), index),
			2000+index,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	repository := outbox.NewRepository(integrationDB)
	start := make(chan struct{})
	type lockResult struct {
		events []outbox.Event
		err    error
	}
	locked := make(chan lockResult, 2)
	for range 2 {
		go func() {
			<-start
			events, err := repository.LockPending(ctx, 3)
			locked <- lockResult{events: events, err: err}
		}()
	}
	close(start)
	seen := map[int64]struct{}{}
	for range 2 {
		result := <-locked
		if result.err != nil {
			t.Fatal(result.err)
		}
		for _, event := range result.events {
			if _, duplicate := seen[event.ID]; duplicate {
				t.Fatalf("outbox event %d leased twice", event.ID)
			}
			seen[event.ID] = struct{}{}
		}
	}
	if len(seen) != 6 {
		t.Fatalf("leased events = %d, want 6", len(seen))
	}
	var staleID int64
	for id := range seen {
		staleID = id
		break
	}
	if _, err := integrationDB.Exec(`UPDATE outbox_events SET updated_at=now()-interval '10 minutes' WHERE id=$1`, staleID); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.RecoverStaleProcessing(ctx, time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered leases = %d, want 1", recovered)
	}
}

func TestFrozenBenchmarkRowsRejectMutation(t *testing.T) {
	ctx := context.Background()
	runID := fmt.Sprintf("integration_corpus_%d", time.Now().UnixNano())
	benchmarkID := fmt.Sprintf("integration_benchmark_%d", time.Now().UnixNano())
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO content_corpus_runs (run_id, profile, seed, config, status, started_at)
VALUES ($1, 'smoke', 1, '{}', 'completed', now())`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO retrieval_benchmarks (benchmark_id, benchmark_version, source_run_id, generator_version, seed, split_policy, status)
VALUES ($1, $1, $2, 'integration', 1, '{}', 'building')`, benchmarkID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO retrieval_benchmark_cases (
  benchmark_id, split, task_type, query, expected_answer, provenance,
  review_status, case_checksum
) VALUES ($1, 'holdout', 'no_answer', 'question', 'answer', 'integration', 'machine_validated', repeat('a',64))`, benchmarkID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE retrieval_benchmarks
SET status='frozen', case_count=1, manifest_checksum=repeat('b',64), frozen_at=now()
WHERE benchmark_id=$1`, benchmarkID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `UPDATE retrieval_benchmark_cases SET query='tampered' WHERE benchmark_id=$1`, benchmarkID); err == nil {
		t.Fatal("frozen benchmark case update unexpectedly succeeded")
	}
}
