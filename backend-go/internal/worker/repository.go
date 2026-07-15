package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"creatorinsight/backend-go/internal/platform/messaging"
	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/jmoiron/sqlx"
)

type EventApplication struct {
	Envelope  messaging.EventEnvelope
	ProjectID int64
	UserID    int64
	NoteID    int64
	CommentID int64
	ParentID  int64
	EventType string
}

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// ApplyEvent commits the idempotency marker, behavior fact, and derived counters
// together. Counter values are rebuilt from fact tables so delivery order is harmless.
func (r *Repository) ApplyEvent(ctx context.Context, consumerName string, input EventApplication) (bool, error) {
	defer observability.ObserveDB("worker_event_apply", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin event application: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inserted, err := claimEvent(ctx, tx, input.Envelope.EventID, consumerName)
	if err != nil {
		return false, err
	}
	if !inserted {
		return true, nil
	}

	if err := recordBehavior(ctx, tx, input); err != nil {
		return false, err
	}
	if err := rebuildDerivedCounters(ctx, tx, input); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit event application: %w", err)
	}
	return false, nil
}

func claimEvent(ctx context.Context, tx *sqlx.Tx, eventID string, consumerName string) (bool, error) {
	var inserted bool
	err := tx.QueryRowContext(ctx, `
INSERT INTO processed_events (event_id, consumer_name, processed_at)
VALUES ($1, $2, now())
ON CONFLICT (event_id, consumer_name) DO NOTHING
RETURNING true`, eventID, consumerName).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim processed event: %w", err)
	}
	return inserted, nil
}

func recordBehavior(ctx context.Context, tx *sqlx.Tx, input EventApplication) error {
	occurredAt := input.Envelope.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	payload := input.Envelope.Payload
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO behavior_events (
    source_event_id, project_id, user_id, note_id, comment_id,
    event_type, event_payload, occurred_at, created_at
)
VALUES ($1, $2, $3, NULLIF($4, 0), NULLIF($5, 0), $6, $7::jsonb, $8, now())
ON CONFLICT (source_event_id) DO NOTHING`,
		input.Envelope.EventID,
		input.ProjectID,
		input.UserID,
		input.NoteID,
		input.CommentID,
		input.EventType,
		string(payload),
		occurredAt,
	)
	if err != nil {
		return fmt.Errorf("record behavior event: %w", err)
	}
	return nil
}

func rebuildDerivedCounters(ctx context.Context, tx *sqlx.Tx, input EventApplication) error {
	switch input.Envelope.EventType {
	case "note.created", "note.liked", "note.unliked", "note.collected", "note.uncollected", "note.shared":
		return rebuildNoteCounters(ctx, tx, input.NoteID)
	case "note.viewed":
		return incrementNoteView(ctx, tx, input.NoteID)
	case "note.updated", "note.deleted":
		return nil
	case "comment.created", "comment.deleted":
		if err := rebuildCommentCounters(ctx, tx, input.CommentID); err != nil {
			return err
		}
		if input.ParentID > 0 {
			if err := rebuildCommentCounters(ctx, tx, input.ParentID); err != nil {
				return err
			}
		}
		return rebuildNoteCounters(ctx, tx, input.NoteID)
	case "comment.liked", "comment.unliked":
		return rebuildCommentCounters(ctx, tx, input.CommentID)
	default:
		return nil
	}
}

func incrementNoteView(ctx context.Context, tx *sqlx.Tx, noteID int64) error {
	if noteID <= 0 {
		return errors.New("note_id must be positive for note view")
	}
	_, err := tx.ExecContext(ctx, `
UPDATE notes
SET view_count = view_count + 1,
    hot_score = hot_score + 1,
    updated_at = now()
WHERE id = $1 AND status = 'published'`, noteID)
	if err != nil {
		return fmt.Errorf("increment note view: %w", err)
	}
	// A view may arrive after its note was deleted. The event remains a valid
	// behavior fact, while the derived counter update becomes a no-op.
	return nil
}

func rebuildNoteCounters(ctx context.Context, tx *sqlx.Tx, noteID int64) error {
	if noteID <= 0 {
		return errors.New("note_id must be positive for note counter rebuild")
	}
	var updatedID int64
	err := tx.QueryRowContext(ctx, `
WITH counts AS (
    SELECT
        (SELECT COUNT(*) FROM note_likes WHERE note_id = $1)::bigint AS like_count,
        (SELECT COUNT(*) FROM note_collects WHERE note_id = $1)::bigint AS collect_count,
        (SELECT COUNT(*) FROM note_comments WHERE note_id = $1 AND status = 1)::bigint AS comment_count,
        (SELECT COUNT(*) FROM note_shares WHERE note_id = $1)::bigint AS share_count
)
UPDATE notes n
SET like_count = counts.like_count,
    collect_count = counts.collect_count,
    comment_count = counts.comment_count,
    share_count = counts.share_count,
    hot_score = n.view_count::double precision
      + counts.like_count::double precision * 3
      + counts.collect_count::double precision * 8
      + counts.comment_count::double precision * 6
      + counts.share_count::double precision * 5,
    updated_at = now()
FROM counts
WHERE n.id = $1
RETURNING n.id`, noteID).Scan(&updatedID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("note %d not found during counter rebuild", noteID)
	}
	if err != nil {
		return fmt.Errorf("rebuild note counters: %w", err)
	}
	return nil
}

func rebuildCommentCounters(ctx context.Context, tx *sqlx.Tx, commentID int64) error {
	if commentID <= 0 {
		return errors.New("comment_id must be positive for comment counter rebuild")
	}
	var updatedID int64
	err := tx.QueryRowContext(ctx, `
UPDATE note_comments c
SET like_count = (SELECT COUNT(*) FROM note_comment_likes WHERE comment_id = $1),
    reply_count = (SELECT COUNT(*) FROM note_comments WHERE parent_id = $1 AND status = 1),
    updated_at = now()
WHERE c.id = $1
RETURNING c.id`, commentID).Scan(&updatedID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("comment %d not found during counter rebuild", commentID)
	}
	if err != nil {
		return fmt.Errorf("rebuild comment counters: %w", err)
	}
	return nil
}
