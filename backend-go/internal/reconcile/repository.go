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
	Scope    string  `db:"scope"`
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

	commentIDs, err := nextRepairBatch(ctx, tx, "comments", "note_comments", 1000)
	if err != nil {
		return CounterRepairResult{}, err
	}
	noteIDs, err := nextRepairBatch(ctx, tx, "notes", "notes", 1000)
	if err != nil {
		return CounterRepairResult{}, err
	}

	commentResult, err := tx.ExecContext(ctx, `
WITH counts AS (
    SELECT c.id,
           COALESCE(l.like_count, 0)::bigint AS like_count,
           COALESCE(r.reply_count, 0)::bigint AS reply_count
    FROM note_comments c
    LEFT JOIN (
        SELECT comment_id, COUNT(*) AS like_count
        FROM note_comment_likes
        WHERE comment_id = ANY($1::bigint[])
        GROUP BY comment_id
    ) l ON l.comment_id = c.id
    LEFT JOIN (
        SELECT parent_id, COUNT(*) AS reply_count
        FROM note_comments
        WHERE status = 1 AND parent_id = ANY($1::bigint[])
        GROUP BY parent_id
    ) r ON r.parent_id = c.id
    WHERE c.id = ANY($1::bigint[])
)
UPDATE note_comments c
SET like_count = counts.like_count,
    reply_count = counts.reply_count,
    updated_at = now()
FROM counts
WHERE c.id = counts.id
  AND (c.like_count IS DISTINCT FROM counts.like_count
       OR c.reply_count IS DISTINCT FROM counts.reply_count)`, commentIDs)
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
        WHERE note_id = ANY($1::bigint[])
        GROUP BY note_id
    ) l ON l.note_id = n.id
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS collect_count
        FROM note_collects
        WHERE note_id = ANY($1::bigint[])
        GROUP BY note_id
    ) c ON c.note_id = n.id
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS comment_count
        FROM note_comments
        WHERE status = 1 AND note_id = ANY($1::bigint[])
        GROUP BY note_id
    ) m ON m.note_id = n.id
    LEFT JOIN (
        SELECT note_id, COUNT(*) AS share_count
        FROM note_shares
        WHERE note_id = ANY($1::bigint[])
        GROUP BY note_id
    ) s ON s.note_id = n.id
    WHERE n.id = ANY($1::bigint[])
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
       OR n.hot_score IS DISTINCT FROM desired.hot_score)`, noteIDs)
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

// ReconcileAllCounters is an explicit maintenance audit. Scheduled reconciliation
// uses bounded batches; this method intentionally scans all fact tables.
func (r *Repository) ReconcileAllCounters(ctx context.Context) (CounterRepairResult, error) {
	defer observability.ObserveDB("reconcile_all_counters", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("begin full counter reconcile: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	commentResult, err := tx.ExecContext(ctx, `
WITH like_counts AS (
    SELECT comment_id, COUNT(*)::bigint AS like_count
    FROM note_comment_likes
    GROUP BY comment_id
), reply_counts AS (
    SELECT parent_id, COUNT(*)::bigint AS reply_count
    FROM note_comments
    WHERE status = 1 AND parent_id > 0
    GROUP BY parent_id
), desired AS (
    SELECT c.id,
           COALESCE(l.like_count, 0)::bigint AS like_count,
           COALESCE(r.reply_count, 0)::bigint AS reply_count
    FROM note_comments c
    LEFT JOIN like_counts l ON l.comment_id = c.id
    LEFT JOIN reply_counts r ON r.parent_id = c.id
)
UPDATE note_comments c
SET like_count = desired.like_count,
    reply_count = desired.reply_count,
    updated_at = now()
FROM desired
WHERE c.id = desired.id
  AND (c.like_count IS DISTINCT FROM desired.like_count
       OR c.reply_count IS DISTINCT FROM desired.reply_count)`)
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("full reconcile comment counters: %w", err)
	}

	noteResult, err := tx.ExecContext(ctx, `
WITH like_counts AS (
    SELECT note_id, COUNT(*)::bigint AS like_count FROM note_likes GROUP BY note_id
), collect_counts AS (
    SELECT note_id, COUNT(*)::bigint AS collect_count FROM note_collects GROUP BY note_id
), comment_counts AS (
    SELECT note_id, COUNT(*)::bigint AS comment_count
    FROM note_comments WHERE status = 1 GROUP BY note_id
), share_counts AS (
    SELECT note_id, COUNT(*)::bigint AS share_count FROM note_shares GROUP BY note_id
), desired AS (
    SELECT n.id,
           COALESCE(l.like_count, 0)::bigint AS like_count,
           COALESCE(c.collect_count, 0)::bigint AS collect_count,
           COALESCE(m.comment_count, 0)::bigint AS comment_count,
           COALESCE(s.share_count, 0)::bigint AS share_count
    FROM notes n
    LEFT JOIN like_counts l ON l.note_id = n.id
    LEFT JOIN collect_counts c ON c.note_id = n.id
    LEFT JOIN comment_counts m ON m.note_id = n.id
    LEFT JOIN share_counts s ON s.note_id = n.id
)
UPDATE notes n
SET like_count = desired.like_count,
    collect_count = desired.collect_count,
    comment_count = desired.comment_count,
    share_count = desired.share_count,
    hot_score = n.view_count::double precision
      + desired.like_count::double precision * 3
      + desired.collect_count::double precision * 8
      + desired.comment_count::double precision * 6
      + desired.share_count::double precision * 5,
    updated_at = now()
FROM desired
WHERE n.id = desired.id
  AND (n.like_count IS DISTINCT FROM desired.like_count
       OR n.collect_count IS DISTINCT FROM desired.collect_count
       OR n.comment_count IS DISTINCT FROM desired.comment_count
       OR n.share_count IS DISTINCT FROM desired.share_count
       OR n.hot_score IS DISTINCT FROM (
           n.view_count::double precision
           + desired.like_count::double precision * 3
           + desired.collect_count::double precision * 8
           + desired.comment_count::double precision * 6
           + desired.share_count::double precision * 5
       ))`)
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("full reconcile note counters: %w", err)
	}

	commentsRepaired, err := commentResult.RowsAffected()
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("read full repaired comment rows: %w", err)
	}
	notesRepaired, err := noteResult.RowsAffected()
	if err != nil {
		return CounterRepairResult{}, fmt.Errorf("read full repaired note rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CounterRepairResult{}, fmt.Errorf("commit full counter reconcile: %w", err)
	}
	return CounterRepairResult{NotesRepaired: notesRepaired, CommentsRepaired: commentsRepaired}, nil
}

func nextRepairBatch(ctx context.Context, tx *sqlx.Tx, stateKey string, table string, limit int) ([]int64, error) {
	var lastID int64
	if err := tx.QueryRowContext(ctx, `
SELECT last_id
FROM reconcile_state
WHERE state_key = $1
FOR UPDATE`, stateKey).Scan(&lastID); err != nil {
		return nil, fmt.Errorf("lock %s reconcile state: %w", stateKey, err)
	}
	query := fmt.Sprintf("SELECT id FROM %s WHERE id > $1 ORDER BY id ASC LIMIT $2", table)
	var ids []int64
	if err := tx.SelectContext(ctx, &ids, query, lastID, limit); err != nil {
		return nil, fmt.Errorf("load %s reconcile batch: %w", stateKey, err)
	}
	if len(ids) == 0 && lastID > 0 {
		if err := tx.SelectContext(ctx, &ids, query, 0, limit); err != nil {
			return nil, fmt.Errorf("wrap %s reconcile batch: %w", stateKey, err)
		}
	}
	nextID := int64(0)
	if len(ids) > 0 {
		nextID = ids[len(ids)-1]
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO reconcile_state (state_key, last_id, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (state_key) DO UPDATE SET last_id = EXCLUDED.last_id, updated_at = now()`, stateKey, nextID); err != nil {
		return nil, fmt.Errorf("advance %s reconcile state: %w", stateKey, err)
	}
	return ids, nil
}

func (r *Repository) ListNoteRankings(ctx context.Context, limit int64) ([]NoteRankingEntry, error) {
	defer observability.ObserveDB("reconcile_note_rankings", time.Now())
	var entries []NoteRankingEntry
	if err := r.db.SelectContext(ctx, &entries, `
WITH ranked AS (
    SELECT id, category, hot_score AS score,
           ROW_NUMBER() OVER (PARTITION BY category ORDER BY hot_score DESC, id DESC) AS category_rank
    FROM notes
    WHERE status = 'published'
), global_top AS (
    SELECT id, category, score, 'global'::text AS scope
    FROM ranked
    ORDER BY score DESC, id DESC
    LIMIT $1
), category_top AS (
    SELECT id, category, score, 'category'::text AS scope
    FROM ranked
    WHERE category_rank <= $1
)
SELECT id, category, score, scope FROM global_top
UNION ALL
SELECT id, category, score, scope FROM category_top
ORDER BY scope DESC, score DESC, id DESC`, limit); err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *Repository) ListCommentRankings(ctx context.Context, limit int64) ([]CommentRankingEntry, error) {
	defer observability.ObserveDB("reconcile_comment_rankings", time.Now())
	var entries []CommentRankingEntry
	if err := r.db.SelectContext(ctx, &entries, `
SELECT id, note_id, score
FROM (
    SELECT c.id, c.note_id, (c.like_count * 5)::double precision AS score,
           ROW_NUMBER() OVER (PARTITION BY c.note_id ORDER BY c.like_count DESC, c.id DESC) AS note_rank
    FROM note_comments c
    JOIN notes n ON n.id = c.note_id AND n.status = 'published'
    WHERE c.status = 1
) ranked
WHERE note_rank <= $1
ORDER BY note_id ASC, score DESC, id DESC`, limit); err != nil {
		return nil, err
	}
	return entries, nil
}
