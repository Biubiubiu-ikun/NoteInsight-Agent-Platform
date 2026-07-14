package note

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"creatorinsight/backend-go/internal/outbox"
	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

type NoteRankingStats struct {
	Category string
	HotScore float64
}

type CommentRankingInfo struct {
	NoteID    int64 `db:"note_id"`
	LikeCount int64 `db:"like_count"`
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateNote(ctx context.Context, input CreateNoteInput) (Note, error) {
	defer observability.ObserveDB("note_create", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return Note{}, fmt.Errorf("begin create note: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	topics, err := jsonText(input.Topics, "[]")
	if err != nil {
		return Note{}, err
	}
	tags, err := jsonText(input.Tags, "[]")
	if err != nil {
		return Note{}, err
	}
	location, err := jsonText(input.Location, "{}")
	if err != nil {
		return Note{}, err
	}
	productEntities, err := jsonText(input.ProductEntities, "[]")
	if err != nil {
		return Note{}, err
	}

	note, err := scanNote(tx.QueryRowxContext(ctx, `
INSERT INTO notes (
    project_id, author_id, title, body, category, topics, tags, location, product_entities
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb)
RETURNING `+noteSelectColumns(),
		input.ProjectID,
		input.AuthorID,
		input.Title,
		input.Body,
		input.Category,
		topics,
		tags,
		location,
		productEntities,
	))
	if err != nil {
		return Note{}, mapDBError(err)
	}

	for _, mediaInput := range input.Media {
		metadata, err := jsonText(mediaInput.Metadata, "{}")
		if err != nil {
			return Note{}, err
		}

		media, err := scanMedia(tx.QueryRowxContext(ctx, `
INSERT INTO note_media (note_id, media_type, url, caption, ocr_text, position, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
RETURNING id, note_id, media_type, COALESCE(url, ''), COALESCE(caption, ''), COALESCE(ocr_text, ''), position, COALESCE(metadata, '{}'::jsonb)::text, created_at`,
			note.ID,
			mediaInput.MediaType,
			mediaInput.URL,
			mediaInput.Caption,
			mediaInput.OCRText,
			mediaInput.Position,
			metadata,
		))
		if err != nil {
			return Note{}, mapDBError(err)
		}
		note.Media = append(note.Media, media)
	}

	if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
		AggregateType: "note",
		AggregateID:   note.ID,
		EventType:     "note.created",
		Payload: map[string]any{
			"project_id": input.ProjectID,
			"user_id":    input.AuthorID,
			"note_id":    note.ID,
			"category":   note.Category,
		},
	}); err != nil {
		return Note{}, mapDBError(err)
	}

	if err := tx.Commit(); err != nil {
		return Note{}, fmt.Errorf("commit create note: %w", err)
	}
	return note, nil
}

func (r *Repository) GetNote(ctx context.Context, id int64) (Note, error) {
	defer observability.ObserveDB("note_get", time.Now())
	note, err := scanNote(r.db.QueryRowxContext(ctx, `
SELECT `+noteSelectColumns()+`
FROM notes
WHERE id = $1 AND status = 'published'`,
		id,
	))
	if err != nil {
		return Note{}, mapDBError(err)
	}

	media, err := r.listMedia(ctx, id)
	if err != nil {
		return Note{}, err
	}
	note.Media = media
	return note, nil
}

func (r *Repository) ListNotes(ctx context.Context, input ListNotesInput, cursor keysetCursor) ([]Note, bool, error) {
	defer observability.ObserveDB("note_list", time.Now())
	limitWithLookahead := input.Limit + 1

	var (
		rows *sqlx.Rows
		err  error
	)
	if input.Cursor == "" {
		rows, err = r.db.QueryxContext(ctx, `
SELECT `+noteSelectColumns()+`
FROM notes
WHERE status = 'published'
  AND ($1 = '' OR category = $1)
ORDER BY created_at DESC, id DESC
LIMIT $2`,
			input.Category,
			limitWithLookahead,
		)
	} else {
		rows, err = r.db.QueryxContext(ctx, `
SELECT `+noteSelectColumns()+`
FROM notes
WHERE status = 'published'
  AND ($1 = '' OR category = $1)
  AND (created_at, id) < ($2, $3)
ORDER BY created_at DESC, id DESC
LIMIT $4`,
			input.Category,
			cursor.CreatedAt,
			cursor.ID,
			limitWithLookahead,
		)
	}
	if err != nil {
		return nil, false, mapDBError(err)
	}
	defer rows.Close()

	notes := make([]Note, 0, limitWithLookahead)
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, false, mapDBError(err)
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, false, mapDBError(err)
	}

	hasMore := len(notes) > input.Limit
	if hasMore {
		notes = notes[:input.Limit]
	}
	return notes, hasMore, nil
}

func (r *Repository) CreateComment(ctx context.Context, noteID int64, input CreateCommentInput) (NoteComment, error) {
	defer observability.ObserveDB("comment_create", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return NoteComment{}, fmt.Errorf("begin create comment: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	if err := ensurePublishedNote(ctx, tx, noteID); err != nil {
		return NoteComment{}, err
	}

	rootID := int64(0)
	if input.ParentID > 0 {
		var parentRootID int64
		if err := tx.QueryRowContext(ctx, `
SELECT CASE WHEN root_id = 0 THEN id ELSE root_id END
FROM note_comments
WHERE id = $1 AND note_id = $2 AND status = 1`,
			input.ParentID,
			noteID,
		).Scan(&parentRootID); err != nil {
			return NoteComment{}, mapDBError(err)
		}
		rootID = parentRootID
	}

	comment, err := scanComment(tx.QueryRowxContext(ctx, `
INSERT INTO note_comments (note_id, user_id, parent_id, root_id, content, intent)
VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
RETURNING `+commentSelectColumns(),
		noteID,
		input.UserID,
		input.ParentID,
		rootID,
		input.Content,
		input.Intent,
	))
	if err != nil {
		return NoteComment{}, mapDBError(err)
	}

	if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
		AggregateType: "comment",
		AggregateID:   comment.ID,
		EventType:     "comment.created",
		Payload: map[string]any{
			"user_id":    input.UserID,
			"note_id":    noteID,
			"comment_id": comment.ID,
			"parent_id":  input.ParentID,
			"intent":     input.Intent,
		},
	}); err != nil {
		return NoteComment{}, mapDBError(err)
	}

	if err := tx.Commit(); err != nil {
		return NoteComment{}, fmt.Errorf("commit create comment: %w", err)
	}
	return comment, nil
}

func (r *Repository) ListComments(ctx context.Context, noteID int64, input ListCommentsInput, cursor keysetCursor) ([]NoteComment, bool, error) {
	defer observability.ObserveDB("comment_list", time.Now())
	limitWithLookahead := input.Limit + 1
	var (
		rows *sqlx.Rows
		err  error
	)
	if input.Cursor == "" {
		rows, err = r.db.QueryxContext(ctx, `
SELECT `+commentSelectColumns()+`
FROM note_comments
WHERE note_id = $1 AND status = 1
ORDER BY created_at DESC, id DESC
LIMIT $2`,
			noteID,
			limitWithLookahead,
		)
	} else {
		rows, err = r.db.QueryxContext(ctx, `
SELECT `+commentSelectColumns()+`
FROM note_comments
WHERE note_id = $1
  AND status = 1
  AND (created_at, id) < ($2, $3)
ORDER BY created_at DESC, id DESC
LIMIT $4`,
			noteID,
			cursor.CreatedAt,
			cursor.ID,
			limitWithLookahead,
		)
	}
	if err != nil {
		return nil, false, mapDBError(err)
	}
	defer rows.Close()

	comments := make([]NoteComment, 0, limitWithLookahead)
	for rows.Next() {
		comment, err := scanComment(rows)
		if err != nil {
			return nil, false, mapDBError(err)
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, false, mapDBError(err)
	}

	hasMore := len(comments) > input.Limit
	if hasMore {
		comments = comments[:input.Limit]
	}
	return comments, hasMore, nil
}

func (r *Repository) LikeNote(ctx context.Context, noteID int64, userID int64) (IdempotentActionResult, error) {
	defer observability.ObserveDB("note_like", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return IdempotentActionResult{}, fmt.Errorf("begin like note: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	if err := ensurePublishedNote(ctx, tx, noteID); err != nil {
		return IdempotentActionResult{}, err
	}

	applied, err := insertIdempotent(ctx, tx, `
INSERT INTO note_likes (note_id, user_id)
VALUES ($1, $2)
ON CONFLICT (note_id, user_id) DO NOTHING
RETURNING true`,
		noteID,
		userID,
	)
	if err != nil {
		return IdempotentActionResult{}, mapDBError(err)
	}

	count, err := readMaterializedCount(ctx, tx, `SELECT like_count FROM notes WHERE id = $1`, noteID)
	if err != nil {
		return IdempotentActionResult{}, mapDBError(err)
	}

	if applied {
		if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
			AggregateType: "note",
			AggregateID:   noteID,
			EventType:     "note.liked",
			Payload: map[string]any{
				"user_id": userID,
				"note_id": noteID,
			},
		}); err != nil {
			return IdempotentActionResult{}, mapDBError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return IdempotentActionResult{}, fmt.Errorf("commit like note: %w", err)
	}
	return IdempotentActionResult{ResourceID: noteID, UserID: userID, Applied: applied, Count: count, CountPending: applied, Action: "like_note"}, nil
}

func (r *Repository) CollectNote(ctx context.Context, noteID int64, userID int64, collectionName string) (IdempotentActionResult, error) {
	defer observability.ObserveDB("note_collect", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return IdempotentActionResult{}, fmt.Errorf("begin collect note: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	if err := ensurePublishedNote(ctx, tx, noteID); err != nil {
		return IdempotentActionResult{}, err
	}

	applied, err := insertIdempotent(ctx, tx, `
INSERT INTO note_collects (note_id, user_id, collection_name)
VALUES ($1, $2, NULLIF($3, ''))
ON CONFLICT (note_id, user_id) DO NOTHING
RETURNING true`,
		noteID,
		userID,
		collectionName,
	)
	if err != nil {
		return IdempotentActionResult{}, mapDBError(err)
	}

	count, err := readMaterializedCount(ctx, tx, `SELECT collect_count FROM notes WHERE id = $1`, noteID)
	if err != nil {
		return IdempotentActionResult{}, mapDBError(err)
	}

	if applied {
		if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
			AggregateType: "note",
			AggregateID:   noteID,
			EventType:     "note.collected",
			Payload: map[string]any{
				"user_id":         userID,
				"note_id":         noteID,
				"collection_name": collectionName,
			},
		}); err != nil {
			return IdempotentActionResult{}, mapDBError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return IdempotentActionResult{}, fmt.Errorf("commit collect note: %w", err)
	}
	return IdempotentActionResult{ResourceID: noteID, UserID: userID, Applied: applied, Count: count, CountPending: applied, Action: "collect_note"}, nil
}

func (r *Repository) ShareNote(ctx context.Context, noteID int64, userID int64, channel string) (ShareNoteResult, error) {
	defer observability.ObserveDB("note_share", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return ShareNoteResult{}, fmt.Errorf("begin share note: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	if err := ensurePublishedNote(ctx, tx, noteID); err != nil {
		return ShareNoteResult{}, err
	}

	var shareID int64
	if err := tx.QueryRowContext(ctx, `
INSERT INTO note_shares (note_id, user_id, channel)
VALUES ($1, $2, $3)
RETURNING id`,
		noteID,
		userID,
		channel,
	).Scan(&shareID); err != nil {
		return ShareNoteResult{}, mapDBError(err)
	}

	shareCount, err := readMaterializedCount(ctx, tx, `SELECT share_count FROM notes WHERE id = $1`, noteID)
	if err != nil {
		return ShareNoteResult{}, mapDBError(err)
	}

	if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
		AggregateType: "note",
		AggregateID:   noteID,
		EventType:     "note.shared",
		Payload: map[string]any{
			"user_id":  userID,
			"note_id":  noteID,
			"share_id": shareID,
			"channel":  channel,
		},
	}); err != nil {
		return ShareNoteResult{}, mapDBError(err)
	}

	if err := tx.Commit(); err != nil {
		return ShareNoteResult{}, fmt.Errorf("commit share note: %w", err)
	}
	return ShareNoteResult{NoteID: noteID, UserID: userID, ShareID: shareID, ShareCount: shareCount, CountPending: true, Channel: channel}, nil
}

func (r *Repository) LikeComment(ctx context.Context, commentID int64, userID int64) (IdempotentActionResult, error) {
	defer observability.ObserveDB("comment_like", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return IdempotentActionResult{}, fmt.Errorf("begin like comment: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	applied, err := insertIdempotent(ctx, tx, `
INSERT INTO note_comment_likes (comment_id, user_id)
VALUES ($1, $2)
ON CONFLICT (comment_id, user_id) DO NOTHING
RETURNING true`,
		commentID,
		userID,
	)
	if err != nil {
		return IdempotentActionResult{}, mapDBError(err)
	}

	count, err := readMaterializedCount(ctx, tx, `SELECT like_count FROM note_comments WHERE id = $1 AND status = 1`, commentID)
	if err != nil {
		return IdempotentActionResult{}, mapDBError(err)
	}

	if applied {
		var noteID int64
		if err := tx.QueryRowContext(ctx, `SELECT note_id FROM note_comments WHERE id = $1`, commentID).Scan(&noteID); err != nil {
			return IdempotentActionResult{}, mapDBError(err)
		}
		if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
			AggregateType: "comment",
			AggregateID:   commentID,
			EventType:     "comment.liked",
			Payload: map[string]any{
				"user_id":    userID,
				"note_id":    noteID,
				"comment_id": commentID,
			},
		}); err != nil {
			return IdempotentActionResult{}, mapDBError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return IdempotentActionResult{}, fmt.Errorf("commit like comment: %w", err)
	}
	return IdempotentActionResult{ResourceID: commentID, UserID: userID, Applied: applied, Count: count, CountPending: applied, Action: "like_comment"}, nil
}

func (r *Repository) UpdateNote(ctx context.Context, noteID int64, input UpdateNoteInput) (Note, error) {
	defer observability.ObserveDB("note_update", time.Now())
	title := ""
	body := ""
	category := ""
	updateTitle := input.Title != nil
	updateBody := input.Body != nil
	updateCategory := input.Category != nil
	if input.Title != nil {
		title = *input.Title
	}
	if input.Body != nil {
		body = *input.Body
	}
	if input.Category != nil {
		category = *input.Category
	}

	note, err := scanNote(r.db.QueryRowxContext(ctx, `
UPDATE notes
SET title = CASE WHEN $2 THEN $3 ELSE title END,
    body = CASE WHEN $4 THEN $5 ELSE body END,
    category = CASE WHEN $6 THEN $7 ELSE category END,
    updated_at = now()
WHERE id = $1 AND status = 'published'
RETURNING `+noteSelectColumns(),
		noteID,
		updateTitle,
		title,
		updateBody,
		body,
		updateCategory,
		category,
	))
	if err != nil {
		return Note{}, mapDBError(err)
	}

	media, err := r.listMedia(ctx, noteID)
	if err != nil {
		return Note{}, err
	}
	note.Media = media
	return note, nil
}

func (r *Repository) SoftDeleteNote(ctx context.Context, noteID int64) error {
	defer observability.ObserveDB("note_soft_delete", time.Now())
	result, err := r.db.ExecContext(ctx, `
UPDATE notes
SET status = 'deleted', updated_at = now()
WHERE id = $1 AND status <> 'deleted'`,
		noteID,
	)
	if err != nil {
		return mapDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read soft delete note rows affected: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) SoftDeleteComment(ctx context.Context, commentID int64, actorUserID int64) (int64, error) {
	defer observability.ObserveDB("comment_soft_delete", time.Now())
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin soft delete comment: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	var noteID, parentID int64
	if err := tx.QueryRowContext(ctx, `
UPDATE note_comments
SET status = 0, updated_at = now()
WHERE id = $1 AND status = 1
RETURNING note_id, parent_id`,
		commentID,
	).Scan(&noteID, &parentID); err != nil {
		return 0, mapDBError(err)
	}

	if err := outbox.EnqueueTx(ctx, tx, outbox.EventInput{
		AggregateType: "comment",
		AggregateID:   commentID,
		EventType:     "comment.deleted",
		Payload: map[string]any{
			"user_id":    actorUserID,
			"note_id":    noteID,
			"comment_id": commentID,
			"parent_id":  parentID,
		},
	}); err != nil {
		return 0, mapDBError(err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit soft delete comment: %w", err)
	}
	return noteID, nil
}

func (r *Repository) GetNoteOwner(ctx context.Context, noteID int64) (int64, error) {
	defer observability.ObserveDB("note_owner", time.Now())
	var ownerID int64
	if err := r.db.QueryRowContext(ctx, `SELECT author_id FROM notes WHERE id = $1`, noteID).Scan(&ownerID); err != nil {
		return 0, mapDBError(err)
	}
	return ownerID, nil
}

func (r *Repository) GetCommentOwner(ctx context.Context, commentID int64) (int64, error) {
	defer observability.ObserveDB("comment_owner", time.Now())
	var ownerID int64
	if err := r.db.QueryRowContext(ctx, `SELECT user_id FROM note_comments WHERE id = $1 AND status = 1`, commentID).Scan(&ownerID); err != nil {
		return 0, mapDBError(err)
	}
	return ownerID, nil
}

func (r *Repository) GetNoteRankingStats(ctx context.Context, noteID int64) (NoteRankingStats, error) {
	defer observability.ObserveDB("note_ranking_stats", time.Now())
	var stats struct {
		Category     string  `db:"category"`
		HotScore     float64 `db:"hot_score"`
		ViewCount    int64   `db:"view_count"`
		LikeCount    int64   `db:"like_count"`
		CollectCount int64   `db:"collect_count"`
		CommentCount int64   `db:"comment_count"`
		ShareCount   int64   `db:"share_count"`
	}
	if err := r.db.GetContext(ctx, &stats, `
SELECT category, hot_score, view_count, like_count, collect_count, comment_count, share_count
FROM notes
WHERE id = $1 AND status = 'published'`,
		noteID,
	); err != nil {
		return NoteRankingStats{}, mapDBError(err)
	}

	score := float64(stats.ViewCount) +
		float64(stats.LikeCount)*3 +
		float64(stats.CollectCount)*8 +
		float64(stats.CommentCount)*6 +
		float64(stats.ShareCount)*5

	return NoteRankingStats{Category: stats.Category, HotScore: score}, nil
}

func (r *Repository) UpdateNoteHotScore(ctx context.Context, noteID int64, hotScore float64) (float64, error) {
	defer observability.ObserveDB("note_hot_score_update", time.Now())
	var stored float64
	if err := r.db.QueryRowContext(ctx, `
UPDATE notes
SET hot_score = $2, updated_at = now()
WHERE id = $1 AND status = 'published'
RETURNING hot_score`,
		noteID,
		hotScore,
	).Scan(&stored); err != nil {
		return 0, mapDBError(err)
	}
	return stored, nil
}

func (r *Repository) GetCommentRankingInfo(ctx context.Context, commentID int64) (CommentRankingInfo, error) {
	defer observability.ObserveDB("comment_ranking_info", time.Now())
	var info CommentRankingInfo
	if err := r.db.GetContext(ctx, &info, `
SELECT note_id, like_count
FROM note_comments
WHERE id = $1 AND status = 1`,
		commentID,
	); err != nil {
		return CommentRankingInfo{}, mapDBError(err)
	}
	return info, nil
}

func (r *Repository) listMedia(ctx context.Context, noteID int64) ([]NoteMedia, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT id, note_id, media_type, COALESCE(url, ''), COALESCE(caption, ''), COALESCE(ocr_text, ''), position, COALESCE(metadata, '{}'::jsonb)::text, created_at
FROM note_media
WHERE note_id = $1
ORDER BY position ASC, id ASC`,
		noteID,
	)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	media := make([]NoteMedia, 0)
	for rows.Next() {
		item, err := scanMedia(rows)
		if err != nil {
			return nil, mapDBError(err)
		}
		media = append(media, item)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDBError(err)
	}
	return media, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func noteSelectColumns() string {
	return `id, project_id, author_id, title, body, category,
COALESCE(topics, '[]'::jsonb)::text,
COALESCE(tags, '[]'::jsonb)::text,
COALESCE(location, '{}'::jsonb)::text,
COALESCE(product_entities, '[]'::jsonb)::text,
note_type, view_count, like_count, collect_count, comment_count, share_count,
hot_score, quality_score, status, created_at, updated_at`
}

func commentSelectColumns() string {
	return `id, note_id, user_id, parent_id, root_id, content, like_count, reply_count,
COALESCE(sentiment, ''), COALESCE(intent, ''), COALESCE(topic_id, 0), status, created_at, updated_at`
}

func scanNote(row scanner) (Note, error) {
	var note Note
	var topics, tags, location, productEntities string
	if err := row.Scan(
		&note.ID,
		&note.ProjectID,
		&note.AuthorID,
		&note.Title,
		&note.Body,
		&note.Category,
		&topics,
		&tags,
		&location,
		&productEntities,
		&note.NoteType,
		&note.ViewCount,
		&note.LikeCount,
		&note.CollectCount,
		&note.CommentCount,
		&note.ShareCount,
		&note.HotScore,
		&note.QualityScore,
		&note.Status,
		&note.CreatedAt,
		&note.UpdatedAt,
	); err != nil {
		return Note{}, err
	}
	note.Topics = json.RawMessage(topics)
	note.Tags = json.RawMessage(tags)
	note.Location = json.RawMessage(location)
	note.ProductEntities = json.RawMessage(productEntities)
	return note, nil
}

func scanMedia(row scanner) (NoteMedia, error) {
	var media NoteMedia
	var metadata string
	if err := row.Scan(
		&media.ID,
		&media.NoteID,
		&media.MediaType,
		&media.URL,
		&media.Caption,
		&media.OCRText,
		&media.Position,
		&metadata,
		&media.CreatedAt,
	); err != nil {
		return NoteMedia{}, err
	}
	media.Metadata = json.RawMessage(metadata)
	return media, nil
}

func scanComment(row scanner) (NoteComment, error) {
	var comment NoteComment
	if err := row.Scan(
		&comment.ID,
		&comment.NoteID,
		&comment.UserID,
		&comment.ParentID,
		&comment.RootID,
		&comment.Content,
		&comment.LikeCount,
		&comment.ReplyCount,
		&comment.Sentiment,
		&comment.Intent,
		&comment.TopicID,
		&comment.Status,
		&comment.CreatedAt,
		&comment.UpdatedAt,
	); err != nil {
		return NoteComment{}, err
	}
	return comment, nil
}

func insertIdempotent(ctx context.Context, tx *sqlx.Tx, query string, args ...any) (bool, error) {
	var applied bool
	err := tx.QueryRowContext(ctx, query, args...).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func readMaterializedCount(ctx context.Context, tx *sqlx.Tx, selectQuery string, resourceID int64) (int64, error) {
	var count int64
	if err := tx.QueryRowContext(ctx, selectQuery, resourceID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func ensurePublishedNote(ctx context.Context, tx *sqlx.Tx, noteID int64) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM notes WHERE id = $1 AND status = 'published')`, noteID).Scan(&exists); err != nil {
		return mapDBError(err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func jsonText(value any, fallback string) (string, error) {
	switch typed := value.(type) {
	case nil:
		return fallback, nil
	case []string:
		if len(typed) == 0 {
			return fallback, nil
		}
	case map[string]any:
		if len(typed) == 0 {
			return fallback, nil
		}
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal json field: %w", err)
	}
	return string(payload), nil
}

func rollbackUnlessCommitted(tx *sqlx.Tx) {
	_ = tx.Rollback()
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "22P02":
			return ErrInvalidInput
		case "23503":
			return ErrNotFound
		}
	}

	return err
}
