package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/note"
	"creatorinsight/backend-go/internal/platform/messaging"
)

type fakeEventRepository struct {
	already bool
	inputs  []EventApplication
}

func (f *fakeEventRepository) ApplyEvent(_ context.Context, _ string, input EventApplication) (bool, error) {
	f.inputs = append(f.inputs, input)
	return f.already, nil
}

type fakeRankingRepository struct{}

func (fakeRankingRepository) GetNoteRankingStats(context.Context, int64) (note.NoteRankingStats, error) {
	return note.NoteRankingStats{}, nil
}

func (fakeRankingRepository) GetCommentRankingInfo(context.Context, int64) (note.CommentRankingInfo, error) {
	return note.CommentRankingInfo{}, nil
}

func TestEventProcessorAppliesAtomicEvent(t *testing.T) {
	repo := &fakeEventRepository{}
	processor := NewEventProcessor(EventProcessorDeps{
		EventRepo:    repo,
		RankingRepo:  fakeRankingRepository{},
		ConsumerName: "worker-v1",
	})
	payload, _ := json.Marshal(map[string]any{"user_id": 42, "note_id": 9})

	already, err := processor.Process(context.Background(), messaging.EventEnvelope{
		EventID: "evt_1", EventType: "note.liked", Payload: payload, OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if already || len(repo.inputs) != 1 {
		t.Fatalf("already=%v applications=%d", already, len(repo.inputs))
	}
	if repo.inputs[0].EventType != "note_liked" || repo.inputs[0].NoteID != 9 {
		t.Fatalf("application = %+v", repo.inputs[0])
	}
}

func TestEventProcessorReportsDuplicate(t *testing.T) {
	repo := &fakeEventRepository{already: true}
	processor := NewEventProcessor(EventProcessorDeps{
		EventRepo:    repo,
		RankingRepo:  fakeRankingRepository{},
		ConsumerName: "worker-v1",
	})
	payload, _ := json.Marshal(map[string]any{"user_id": 42, "note_id": 9})

	already, err := processor.Process(context.Background(), messaging.EventEnvelope{
		EventID: "evt_1", EventType: "note.liked", Payload: payload,
	})
	if err != nil || !already || len(repo.inputs) != 1 {
		t.Fatalf("already=%v error=%v applications=%d", already, err, len(repo.inputs))
	}
}

func TestEventProcessorRejectsMissingUser(t *testing.T) {
	repo := &fakeEventRepository{}
	processor := NewEventProcessor(EventProcessorDeps{
		EventRepo:    repo,
		RankingRepo:  fakeRankingRepository{},
		ConsumerName: "worker-v1",
	})

	_, err := processor.Process(context.Background(), messaging.EventEnvelope{
		EventID: "evt_1", EventType: "note.liked", Payload: json.RawMessage(`{"note_id":9}`),
	})
	if err == nil {
		t.Fatal("Process() expected missing user error")
	}
	if len(repo.inputs) != 0 {
		t.Fatalf("applications=%d, want 0", len(repo.inputs))
	}
}

func TestEventProcessorRejectsUnsupportedSchemaVersion(t *testing.T) {
	repo := &fakeEventRepository{}
	processor := NewEventProcessor(EventProcessorDeps{EventRepo: repo, RankingRepo: fakeRankingRepository{}, ConsumerName: "worker-v1"})
	payload, _ := json.Marshal(map[string]any{"project_id": 1, "user_id": 42, "note_id": 9})
	_, err := processor.Process(context.Background(), messaging.EventEnvelope{
		EventID: "evt_v2", EventType: "note.updated", SchemaVersion: 2, Payload: payload,
	})
	if err == nil {
		t.Fatal("Process() expected unsupported schema version error")
	}
	if len(repo.inputs) != 0 {
		t.Fatalf("applications=%d, want 0", len(repo.inputs))
	}
}

func TestBehaviorEventTypeIncludesCommentDeletion(t *testing.T) {
	if got := behaviorEventType("comment.deleted"); got != "comment_deleted" {
		t.Fatalf("behaviorEventType() = %q", got)
	}
}
