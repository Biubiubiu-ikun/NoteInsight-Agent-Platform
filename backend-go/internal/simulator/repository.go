package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

const simulatorEventBatchSize = 500

type Repository struct {
	db *sqlx.DB
}

type DatabaseSink struct {
	tx         *sqlx.Tx
	cfg        Config
	eventBatch []Event
	closed     bool
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) LoadDataset(ctx context.Context, userLimit int, noteLimit int, commentsPerNote int) (Dataset, error) {
	if userLimit <= 0 || noteLimit <= 0 {
		return Dataset{}, fmt.Errorf("user and note limits must be positive")
	}
	if commentsPerNote <= 0 {
		commentsPerNote = 20
	}

	var userIDs []int64
	if err := r.db.SelectContext(ctx, &userIDs, `
SELECT id
FROM users
WHERE status = 'active'
ORDER BY id
LIMIT $1`, userLimit); err != nil {
		return Dataset{}, fmt.Errorf("load simulator users: %w", err)
	}

	type noteRow struct {
		ID       int64  `db:"id"`
		Category string `db:"category"`
	}
	var noteRows []noteRow
	if err := r.db.SelectContext(ctx, &noteRows, `
SELECT id, category
FROM notes
WHERE status = 'published'
ORDER BY id
LIMIT $1`, noteLimit); err != nil {
		return Dataset{}, fmt.Errorf("load simulator notes: %w", err)
	}

	type commentRow struct {
		ID     int64 `db:"id"`
		NoteID int64 `db:"note_id"`
	}
	var commentRows []commentRow
	if err := r.db.SelectContext(ctx, &commentRows, `
WITH selected_notes AS (
    SELECT id
    FROM notes
    WHERE status = 'published'
    ORDER BY id
    LIMIT $1
), ranked_comments AS (
    SELECT c.id,
           c.note_id,
           ROW_NUMBER() OVER (PARTITION BY c.note_id ORDER BY c.like_count DESC, c.id DESC) AS row_number
    FROM note_comments c
    JOIN selected_notes n ON n.id = c.note_id
    WHERE c.status = 1
)
SELECT id, note_id
FROM ranked_comments
WHERE row_number <= $2
ORDER BY note_id, row_number`, noteLimit, commentsPerNote); err != nil {
		return Dataset{}, fmt.Errorf("load simulator comments: %w", err)
	}

	commentsByNote := make(map[int64][]int64)
	for _, row := range commentRows {
		commentsByNote[row.NoteID] = append(commentsByNote[row.NoteID], row.ID)
	}
	notes := make([]NoteRef, 0, len(noteRows))
	for _, row := range noteRows {
		notes = append(notes, NoteRef{ID: row.ID, Category: row.Category, CommentIDs: commentsByNote[row.ID]})
	}
	dataset := Dataset{UserIDs: userIDs, Notes: notes}
	if err := dataset.Validate(); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func (r *Repository) NewDatabaseSink(ctx context.Context, cfg Config, replace bool) (*DatabaseSink, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal simulation config: %w", err)
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin simulation run: %w", err)
	}
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM simulation_runs WHERE run_id = $1`, cfg.RunID); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("replace simulation run: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO simulation_runs (run_id, profile, scenario, seed, config, status, event_count, started_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, 'running', 0, $6, now(), now())`,
		cfg.RunID, cfg.Profile, cfg.Scenario, cfg.Seed, string(configJSON), cfg.StartAt,
	); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("create simulation run: %w", err)
	}
	return &DatabaseSink{tx: tx, cfg: cfg, eventBatch: make([]Event, 0, simulatorEventBatchSize)}, nil
}

func (s *DatabaseSink) WriteProfiles(ctx context.Context, profiles []UserProfile) error {
	for start := 0; start < len(profiles); start += simulatorEventBatchSize {
		end := min(start+simulatorEventBatchSize, len(profiles))
		if err := s.writeProfileBatch(ctx, profiles[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *DatabaseSink) WriteEvent(ctx context.Context, event Event) error {
	s.eventBatch = append(s.eventBatch, event)
	if len(s.eventBatch) < simulatorEventBatchSize {
		return nil
	}
	return s.flushEvents(ctx)
}

func (s *DatabaseSink) Complete(ctx context.Context, report Report) error {
	if s.closed {
		return fmt.Errorf("simulation database sink is already closed")
	}
	if err := s.flushEvents(ctx); err != nil {
		return err
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal simulation report: %w", err)
	}
	if _, err := s.tx.ExecContext(ctx, `
UPDATE simulation_runs
SET report = $2::jsonb,
    status = 'completed',
    event_count = $3,
    completed_at = now(),
    updated_at = now()
WHERE run_id = $1`, s.cfg.RunID, string(reportJSON), report.Events); err != nil {
		return fmt.Errorf("complete simulation run: %w", err)
	}
	if err := s.tx.Commit(); err != nil {
		return fmt.Errorf("commit simulation run: %w", err)
	}
	s.closed = true
	return nil
}

func (s *DatabaseSink) Abort(_ context.Context, _ error) {
	if s.closed {
		return
	}
	s.closed = true
	_ = s.tx.Rollback()
}

func (s *DatabaseSink) writeProfileBatch(ctx context.Context, profiles []UserProfile) error {
	if len(profiles) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO user_behavior_profiles (
    user_id, source_run_id, persona, activity_level, positive_ratio,
    comment_length_preference, like_probability, collect_probability,
    comment_probability, share_probability, created_at, updated_at
) VALUES `)
	args := make([]any, 0, len(profiles)*10)
	arg := 1
	for i, profile := range profiles {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,now(),now())", arg, arg+1, arg+2, arg+3, arg+4, arg+5, arg+6, arg+7, arg+8, arg+9))
		arg += 10
		args = append(args, profile.UserID, s.cfg.RunID, profile.Persona, profile.ActivityLevel, profile.PositiveRatio, profile.CommentLengthPreference, profile.LikeProbability, profile.CollectProbability, profile.CommentProbability, profile.ShareProbability)
	}
	query.WriteString(` ON CONFLICT (user_id) DO UPDATE SET
    source_run_id = EXCLUDED.source_run_id,
    persona = EXCLUDED.persona,
    activity_level = EXCLUDED.activity_level,
    positive_ratio = EXCLUDED.positive_ratio,
    comment_length_preference = EXCLUDED.comment_length_preference,
    like_probability = EXCLUDED.like_probability,
    collect_probability = EXCLUDED.collect_probability,
    comment_probability = EXCLUDED.comment_probability,
    share_probability = EXCLUDED.share_probability,
    updated_at = now()`)
	if _, err := s.tx.ExecContext(ctx, query.String(), args...); err != nil {
		return fmt.Errorf("write simulation profiles: %w", err)
	}
	return nil
}

func (s *DatabaseSink) flushEvents(ctx context.Context) error {
	if len(s.eventBatch) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO behavior_events (
    source_event_id, project_id, user_id, note_id, comment_id, event_type,
    event_payload, occurred_at, created_at, simulation_run_id, session_id, sequence_no
) VALUES `)
	args := make([]any, 0, len(s.eventBatch)*11)
	arg := 1
	for i, event := range s.eventBatch {
		if i > 0 {
			query.WriteByte(',')
		}
		query.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,NULLIF($%d,0),$%d,$%d::jsonb,$%d,now(),$%d,$%d,$%d)", arg, arg+1, arg+2, arg+3, arg+4, arg+5, arg+6, arg+7, arg+8, arg+9, arg+10))
		arg += 11
		args = append(args, event.SourceEventID, event.ProjectID, event.UserID, event.NoteID, event.CommentID, event.EventType, string(event.Payload), event.OccurredAt, event.RunID, event.SessionID, event.SequenceNo)
	}
	query.WriteString(` ON CONFLICT (source_event_id) DO NOTHING`)
	if _, err := s.tx.ExecContext(ctx, query.String(), args...); err != nil {
		return fmt.Errorf("write simulation behavior events: %w", err)
	}
	s.eventBatch = s.eventBatch[:0]
	return nil
}

func SyntheticDataset(userCount int, noteCount int, commentsPerNote int) Dataset {
	if userCount < 1 {
		userCount = 1
	}
	if noteCount < 1 {
		noteCount = 1
	}
	if commentsPerNote < 0 {
		commentsPerNote = 0
	}
	users := make([]int64, userCount)
	for i := range users {
		users[i] = int64(i + 1)
	}
	categories := []string{"beauty", "fashion", "food", "travel", "home", "fitness", "career", "digital", "study", "local_life"}
	notes := make([]NoteRef, noteCount)
	commentID := int64(1)
	for i := range notes {
		notes[i] = NoteRef{ID: int64(i + 1), Category: categories[i%len(categories)]}
		for range commentsPerNote {
			notes[i].CommentIDs = append(notes[i].CommentIDs, commentID)
			commentID++
		}
	}
	return Dataset{UserIDs: users, Notes: notes}
}

func FailedRunReport(cause error) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{"failure": cause.Error(), "failed_at": time.Now().UTC()})
	return payload
}
