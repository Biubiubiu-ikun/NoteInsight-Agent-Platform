package contentgen

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

var ErrRunExists = errors.New("content corpus run already exists")

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) LoadActorIDs(ctx context.Context) ([]int64, []int64, error) {
	var users []int64
	if err := r.db.SelectContext(ctx, &users, `
SELECT id
FROM users
WHERE status = 'active'
ORDER BY id`); err != nil {
		return nil, nil, fmt.Errorf("load corpus users: %w", err)
	}
	if len(users) == 0 {
		return nil, nil, fmt.Errorf("quality corpus requires active users; run seedgen first")
	}
	var creators []int64
	if err := r.db.SelectContext(ctx, &creators, `
SELECT id
FROM users
WHERE status = 'active' AND role IN ('creator', 'admin')
ORDER BY id`); err != nil {
		return nil, nil, fmt.Errorf("load corpus creators: %w", err)
	}
	if len(creators) == 0 {
		creators = append([]int64(nil), users...)
	}
	return users, creators, nil
}

func (r *Repository) ResolveIDs(ctx context.Context, cfg Config, replace bool) (Config, error) {
	cfg.Normalize()
	if len(cfg.RunID) == 0 || len(cfg.RunID) > 80 || !validIdentifier(cfg.RunID) {
		return Config{}, fmt.Errorf("invalid run_id")
	}
	var raw []byte
	err := r.db.GetContext(ctx, &raw, `SELECT config FROM content_corpus_runs WHERE run_id = $1`, cfg.RunID)
	if err == nil {
		if !replace {
			return Config{}, fmt.Errorf("%w: %s", ErrRunExists, cfg.RunID)
		}
		var stored Config
		if unmarshalErr := json.Unmarshal(raw, &stored); unmarshalErr != nil {
			return Config{}, fmt.Errorf("decode existing corpus config: %w", unmarshalErr)
		}
		cfg.NoteIDStart = stored.NoteIDStart
		cfg.CommentIDStart = stored.CommentIDStart
		return cfg, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Config{}, fmt.Errorf("load corpus run: %w", err)
	}
	if err := r.db.GetContext(ctx, &cfg.NoteIDStart, `SELECT COALESCE(MAX(id), 0) + 1 FROM notes`); err != nil {
		return Config{}, fmt.Errorf("resolve corpus note IDs: %w", err)
	}
	if err := r.db.GetContext(ctx, &cfg.CommentIDStart, `SELECT COALESCE(MAX(id), 0) + 1 FROM note_comments`); err != nil {
		return Config{}, fmt.Errorf("resolve corpus comment IDs: %w", err)
	}
	return cfg, nil
}

func (r *Repository) Save(ctx context.Context, corpus Corpus, report Report, replace bool) error {
	if err := corpus.Config.Validate(); err != nil {
		return err
	}
	if len(corpus.Items) != corpus.Config.Notes || report.Notes != corpus.Config.Notes {
		return fmt.Errorf("corpus note count does not match configuration")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin corpus transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing bool
	if err := tx.GetContext(ctx, &existing, `SELECT EXISTS (SELECT 1 FROM content_corpus_runs WHERE run_id = $1)`, corpus.Config.RunID); err != nil {
		return fmt.Errorf("check corpus run: %w", err)
	}
	if existing && !replace {
		return fmt.Errorf("%w: %s", ErrRunExists, corpus.Config.RunID)
	}
	if existing {
		if _, err := tx.ExecContext(ctx, `DELETE FROM notes WHERE id IN (SELECT note_id FROM content_scenarios WHERE run_id = $1)`, corpus.Config.RunID); err != nil {
			return fmt.Errorf("delete existing corpus notes: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM content_corpus_runs WHERE run_id = $1`, corpus.Config.RunID); err != nil {
			return fmt.Errorf("delete existing corpus run: %w", err)
		}
	}

	configJSON, err := json.Marshal(corpus.Config)
	if err != nil {
		return fmt.Errorf("encode corpus config: %w", err)
	}
	startedAt := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO content_corpus_runs (
    run_id, profile, seed, config, status, started_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, 'running', $5, $5, $5)`,
		corpus.Config.RunID,
		corpus.Config.Profile,
		corpus.Config.Seed,
		string(configJSON),
		startedAt,
	); err != nil {
		return fmt.Errorf("insert corpus run: %w", err)
	}

	notes := newTxBatch(tx, `INSERT INTO notes (
id, project_id, author_id, title, body, category, topics, tags, location,
product_entities, note_type, comment_count, hot_score, quality_score, status, created_at, updated_at
) VALUES `, "", 17, 250)
	media := newTxBatch(tx, `INSERT INTO note_media (
note_id, media_type, url, caption, ocr_text, position, metadata, created_at
) VALUES `, "", 8, 500)
	comments := newTxBatch(tx, `INSERT INTO note_comments (
id, note_id, user_id, parent_id, root_id, content, sentiment, intent, topic_id, status, created_at, updated_at
) VALUES `, "", 12, 500)
	scenarios := newTxBatch(tx, `INSERT INTO content_scenarios (
note_id, run_id, category, subject, scenario, scenario_version, created_at
) VALUES `, "", 7, 250)
	evalCases := newTxBatch(tx, `INSERT INTO content_eval_cases (
run_id, note_id, task_type, question, expected_answer, gold_sources, metadata, created_at
) VALUES `, "", 8, 250)

	for _, item := range corpus.Items {
		document := item.Document
		topics, err := json.Marshal(document.Topics)
		if err != nil {
			return fmt.Errorf("encode note topics: %w", err)
		}
		tags, err := json.Marshal(document.Tags)
		if err != nil {
			return fmt.Errorf("encode note tags: %w", err)
		}
		location, err := json.Marshal(document.Location)
		if err != nil {
			return fmt.Errorf("encode note location: %w", err)
		}
		products, err := json.Marshal(document.ProductEntities)
		if err != nil {
			return fmt.Errorf("encode product entities: %w", err)
		}
		commentCount := int64(len(item.Comments))
		notes.add(document.ID, document.ProjectID, document.AuthorID, document.Title, document.Body, document.Category, string(topics), string(tags), string(location), string(products), "image_text", commentCount, float64(commentCount*6), document.QualityScore, "published", document.CreatedAt, document.CreatedAt)

		for _, asset := range document.Media {
			metadata, err := json.Marshal(asset.Metadata)
			if err != nil {
				return fmt.Errorf("encode media metadata: %w", err)
			}
			media.add(document.ID, "image", nil, asset.Caption, asset.OCRText, asset.Position, string(metadata), document.CreatedAt)
		}
		for _, comment := range item.Comments {
			comments.add(comment.ID, comment.NoteID, comment.UserID, 0, 0, comment.Content, comment.Sentiment, comment.Intent, comment.TopicID, 1, comment.CreatedAt, comment.CreatedAt)
		}
		scenarioJSON, err := json.Marshal(document.Scenario)
		if err != nil {
			return fmt.Errorf("encode content scenario: %w", err)
		}
		scenarios.add(document.ID, corpus.Config.RunID, document.Category, document.Scenario.Subject, string(scenarioJSON), "phase5b_v1", document.CreatedAt)
		for _, evalCase := range item.EvalCases {
			goldSources, err := json.Marshal(evalCase.GoldSources)
			if err != nil {
				return fmt.Errorf("encode eval gold sources: %w", err)
			}
			metadata, err := json.Marshal(evalCase.Metadata)
			if err != nil {
				return fmt.Errorf("encode eval metadata: %w", err)
			}
			evalCases.add(corpus.Config.RunID, document.ID, evalCase.TaskType, evalCase.Question, evalCase.ExpectedAnswer, string(goldSources), string(metadata), document.CreatedAt)
		}
	}
	orderedBatches := []struct {
		label string
		batch *txBatch
	}{
		{"notes", notes},
		{"media", media},
		{"comments", comments},
		{"scenarios", scenarios},
		{"eval cases", evalCases},
	}
	for _, current := range orderedBatches {
		if err := current.batch.flush(ctx); err != nil {
			return fmt.Errorf("insert corpus %s: %w", current.label, err)
		}
	}
	var inconsistentNotes int64
	if err := tx.GetContext(ctx, &inconsistentNotes, `
SELECT COUNT(*)
FROM notes n
JOIN content_scenarios s ON s.note_id = n.id
WHERE s.run_id = $1
  AND n.comment_count <> (
      SELECT COUNT(*) FROM note_comments c WHERE c.note_id = n.id AND c.status = 1
  )`, corpus.Config.RunID); err != nil {
		return fmt.Errorf("validate corpus note counters: %w", err)
	}
	if inconsistentNotes != 0 {
		return fmt.Errorf("validate corpus note counters: %d notes are inconsistent", inconsistentNotes)
	}

	reportJSON, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode corpus report: %w", err)
	}
	completedAt := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE content_corpus_runs
SET report = $2,
    status = 'completed',
    note_count = $3,
    media_count = $4,
    comment_count = $5,
    eval_case_count = $6,
    completed_at = $7,
    updated_at = $7
WHERE run_id = $1`,
		corpus.Config.RunID,
		string(reportJSON),
		report.Notes,
		report.Media,
		report.Comments,
		report.EvalCases,
		completedAt,
	); err != nil {
		return fmt.Errorf("complete corpus run: %w", err)
	}
	sequenceQueries := []string{
		`SELECT setval('notes_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM notes), 1), true)`,
		`SELECT setval('note_comments_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM note_comments), 1), true)`,
	}
	for _, query := range sequenceQueries {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("advance corpus sequence: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit corpus transaction: %w", err)
	}
	return nil
}

type txBatch struct {
	tx        *sqlx.Tx
	prefix    string
	suffix    string
	columns   int
	batchSize int
	rows      [][]any
}

func newTxBatch(tx *sqlx.Tx, prefix string, suffix string, columns int, batchSize int) *txBatch {
	return &txBatch{tx: tx, prefix: prefix, suffix: suffix, columns: columns, batchSize: batchSize}
}

func (b *txBatch) add(values ...any) {
	b.rows = append(b.rows, values)
}

func (b *txBatch) flush(ctx context.Context) error {
	for len(b.rows) > 0 {
		count := min(b.batchSize, len(b.rows))
		if err := b.exec(ctx, b.rows[:count]); err != nil {
			return err
		}
		b.rows = b.rows[count:]
	}
	return nil
}

func (b *txBatch) exec(ctx context.Context, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	var builder strings.Builder
	args := make([]any, 0, len(rows)*b.columns)
	builder.WriteString(b.prefix)
	argument := 1
	for rowIndex, row := range rows {
		if len(row) != b.columns {
			return fmt.Errorf("batch row has %d values, want %d", len(row), b.columns)
		}
		if rowIndex > 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('(')
		for column := 0; column < b.columns; column++ {
			if column > 0 {
				builder.WriteByte(',')
			}
			builder.WriteString(fmt.Sprintf("$%d", argument))
			argument++
		}
		builder.WriteByte(')')
		args = append(args, row...)
	}
	builder.WriteString(b.suffix)
	_, err := b.tx.ExecContext(ctx, builder.String(), args...)
	return err
}
