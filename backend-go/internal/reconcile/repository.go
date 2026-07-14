package reconcile

import (
	"context"
	"fmt"
	"time"

	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

type CounterRepairResult struct {
	NotesRepaired    int64
	CommentsRepaired int64
}

type NoteRankingEntry struct {
	ID       int64   `db:"id"`
	Category string  `db:"category"`
	Score    float64 `db:"score"`
}

type CommentRankingEntry struct {
	ID     int64   `db:"id"`
	NoteID int64   `db:"note_id"`
	Score  float64 `db:"score"`
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) ReconcileCounters(ctx context.Context) (CounterRepairResult, error) {
	defer observability.ObserveDB("reconcile_counters", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("begin counter reconcile: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	commentResult, err := tx.ExecContext(ctx, `
WITH counts AS (
    SELECT c.id,
           COALESCE(l.like_count, 0)::bigint AS like_count,
           COALESCE(r.reply_count, 0)::bigint AS reply_count
    FROM note_comments c
    LEFT JOIN (
        SELECT comment_id, COUNT(*) AS like_count
        FROM note_comment_likes
        GROUP BY comment_id
    ) l ON l.comment_id = c.id
    LEFT JOIN (
        SELECT parent_id, COUNT(*) AS reply_count
        FROM note_comments
        WHERE status = 1 AND parent_id > 0
        GROUP BY parent_id
    ) r ON r.parent_id = c.id
)
UPDATE note_comments c
SET like_count = counts.like_count,
    reply_count = counts.reply_count,
    updated_at = now()
FROM counts
WHERE c.id = counts.id
  AND (c.like_count IS DISTINCT FROM counts.like_count
       OR c.reply_count IS DISTINCT FROM counts.reply_count)`)
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("reconcile comment counters: %w", err)
	}

	noteResult, err := tx.ExecContext(ctx, `
WITH counts AS (
    SELECT n.id,
           COALESCE(l.like_count, 0)::bigint AS like_count,
           COALESCE(c.collect_count, 0)::bigint AS collect_count,
           COALESCE(m.comment_count, 0)::bigint AS comment_count,
           COALESCE(s.share_count, 0)::bigint AS share_count
    FROM notes n
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS like_count
        FROM note_likes
        GROUP BY note_id
    ) l ON l.note_id = n.id
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS collect_count
        FROM note_collects
        GROUP BY note_id
    ) c ON c.note_id = n.id
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS comment_count
        FROM note_comments
        WHERE status = 1
        GROUP BY note_id
    ) m ON m.note_id = n.id
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS share_count
        FROM note_shares
        GROUP BY note_id
    ) s ON s.note_id = n.id
), desired AS (
    SELECT n.id,
           counts.like_count,
           counts.collect_count,
           counts.comment_count,
           counts.share_count,
           n.view_count::double precision
             + counts.like_count::double precision * 3
             + counts.collect_count::double precision * 8
             + counts.comment_count::double precision * 6
             + counts.share_count::double precision * 5 AS hot_score
    FROM notes n
    JOIN counts ON counts.id = n.id
)
UPDATE notes n
SET like_count = desired.like_count,
    collect_count = desired.collect_count,
    comment_count = desired.comment_count,
    share_count = desired.share_count,
    hot_score = desired.hot_score,
    updated_at = now()
FROM desired
WHERE n.id = desired.id
  AND (n.like_count IS DISTINCT FROM desired.like_count
       OR n.collect_count IS DISTINCT FROM desired.collect_count
       OR n.comment_count IS DISTINCT FROM desired.comment_count
       OR n.share_count IS DISTINCT FROM desired.share_count
       OR n.hot_score IS DISTINCT FROM desired.hot_score)`)
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("reconcile note counters: %w", err)
	}

	commentsRepaired, err := commentResult.RowsAffected()
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("read repaired comment rows: %w", err)
	}
	notesRepaired, err := noteResult.RowsAffected()
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("read repaired note rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CounterRepairResult{}, fmt.Errorf("commit counter reconcile: %w", err)
	}
	return CounterRepairResult{NotesRepaired: notesRepaired, CommentsRepaired: commentsRepaired}, nil
}

func (r *Repository) ListNoteRankings(ctx context.Context) ([]NoteRankingEntry, error) {
	defer observability.ObserveDB("reconcile_note_rankings", time.Now())
	var entries []NoteRankingEntry
	if err := r.db.SelectContext(ctx, &entries, `
SELECT id, category, hot_score AS score
FROM notes
WHERE status = 'published'
ORDER BY hot_score DESC, id DESC`); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *Repository) ListCommentRankings(ctx context.Context) ([]CommentRankingEntry, error) {
	defer observability.ObserveDB("reconcile_comment_rankings", time.Now())
	var entries []CommentRankingEntry
	if err := r.db.SelectContext(ctx, &entries, `
SELECT c.id, c.note_id, (c.like_count * 5)::double precision AS score
FROM note_comments c
JOIN notes n ON n.id = c.note_id AND n.status = 'published'
WHERE c.status = 1
ORDER BY c.note_id ASC, c.like_count DESC, c.id DESC`); err != nil {
		return nil, err
	}
	return entries, nil
}
