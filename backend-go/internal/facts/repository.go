package facts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type Result struct {
	RunID         string `json:"run_id"`
	NoteFactCount int64  `json:"note_fact_count"`
	UserFactCount int64  `json:"user_fact_count"`
}

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Materialize(ctx context.Context, runID string, from time.Time, to time.Time) (Result, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return Result{}, fmt.Errorf("run_id is required")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return Result{}, fmt.Errorf("materialization window must satisfy from < to")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin fact materialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO fact_materialization_runs (run_id, window_start, window_end, status, started_at, created_at)
VALUES ($1, $2, $3, 'running', now(), now())`, runID, from.UTC(), to.UTC()); err != nil {
		return Result{}, fmt.Errorf("create fact materialization run: %w", err)
	}
	noteResult, err := tx.ExecContext(ctx, `
INSERT INTO note_daily_facts (
    project_id, note_id, fact_date, view_count, like_count, collect_count,
    comment_count, share_count, unique_user_count, event_count, source_run_id, updated_at
)
SELECT project_id,
       note_id,
       occurred_at::date,
       COUNT(*) FILTER (WHERE event_type IN ('note_viewed', 'media_viewed')),
       COUNT(*) FILTER (WHERE event_type = 'note_liked'),
       COUNT(*) FILTER (WHERE event_type = 'note_collected'),
       COUNT(*) FILTER (WHERE event_type = 'comment_created'),
       COUNT(*) FILTER (WHERE event_type = 'note_shared'),
       COUNT(DISTINCT user_id),
       COUNT(*),
       $1,
       now()
FROM behavior_events
WHERE occurred_at >= $2 AND occurred_at < $3 AND note_id IS NOT NULL
GROUP BY project_id, note_id, occurred_at::date
ON CONFLICT (project_id, note_id, fact_date) DO UPDATE SET
    view_count = EXCLUDED.view_count,
    like_count = EXCLUDED.like_count,
    collect_count = EXCLUDED.collect_count,
    comment_count = EXCLUDED.comment_count,
    share_count = EXCLUDED.share_count,
    unique_user_count = EXCLUDED.unique_user_count,
    event_count = EXCLUDED.event_count,
    source_run_id = EXCLUDED.source_run_id,
    updated_at = now()`, runID, from.UTC(), to.UTC())
	if err != nil {
		return Result{}, fmt.Errorf("materialize note daily facts: %w", err)
	}
	userResult, err := tx.ExecContext(ctx, `
INSERT INTO user_daily_facts (
    project_id, user_id, fact_date, view_count, interaction_count,
    content_count, comment_count, active_note_count, event_count, source_run_id, updated_at
)
SELECT project_id,
       user_id,
       occurred_at::date,
       COUNT(*) FILTER (WHERE event_type IN ('note_viewed', 'media_viewed', 'comments_viewed')),
       COUNT(*) FILTER (WHERE event_type IN ('note_liked', 'note_unliked', 'note_collected', 'note_uncollected', 'note_shared', 'comment_liked', 'comment_unliked')),
       COUNT(*) FILTER (WHERE event_type = 'note_created'),
       COUNT(*) FILTER (WHERE event_type = 'comment_created'),
       COUNT(DISTINCT note_id) FILTER (WHERE note_id IS NOT NULL),
       COUNT(*),
       $1,
       now()
FROM behavior_events
WHERE occurred_at >= $2 AND occurred_at < $3
GROUP BY project_id, user_id, occurred_at::date
ON CONFLICT (project_id, user_id, fact_date) DO UPDATE SET
    view_count = EXCLUDED.view_count,
    interaction_count = EXCLUDED.interaction_count,
    content_count = EXCLUDED.content_count,
    comment_count = EXCLUDED.comment_count,
    active_note_count = EXCLUDED.active_note_count,
    event_count = EXCLUDED.event_count,
    source_run_id = EXCLUDED.source_run_id,
    updated_at = now()`, runID, from.UTC(), to.UTC())
	if err != nil {
		return Result{}, fmt.Errorf("materialize user daily facts: %w", err)
	}
	noteFacts, err := noteResult.RowsAffected()
	if err != nil {
		return Result{}, err
	}
	userFacts, err := userResult.RowsAffected()
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE fact_materialization_runs
SET status = 'completed', note_fact_count = $2, user_fact_count = $3, completed_at = now()
WHERE run_id = $1`, runID, noteFacts, userFacts); err != nil {
		return Result{}, fmt.Errorf("complete fact materialization run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit fact materialization: %w", err)
	}
	return Result{RunID: runID, NoteFactCount: noteFacts, UserFactCount: userFacts}, nil
}
