package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/cache"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"

	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

type profileConfig struct {
	users        int
	creators     int
	notes        int
	comments     int
	likes        int
	collects     int
	commentLikes int
}

var devProfile = profileConfig{
	users:        1000,
	creators:     100,
	notes:        5000,
	comments:     20000,
	likes:        100000,
	collects:     30000,
	commentLikes: 50000,
}

func main() {
	var (
		profile    = flag.String("profile", "dev", "seed profile: dev")
		seed       = flag.Int64("seed", 20260706, "random seed")
		truncate   = flag.Bool("truncate", false, "truncate generated data before seeding")
		withTokens = flag.Bool("with-tokens", false, "generate user_auth_tokens and token csv")
		dryRun     = flag.Bool("dry-run", false, "print planned data volume without writing")
		tokenOut   = flag.String("token-out", "tmp/dev_tokens.csv", "token csv output path")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.App.Env, cfg.Log.Level)
	slog.SetDefault(logger)
	if cfg.App.Env == "prod" && !*dryRun {
		logger.Error("seedgen database writes are disabled in production")
		os.Exit(1)
	}

	selected, err := selectProfile(*profile)
	if err != nil {
		logger.Error("select profile failed", "error", err)
		os.Exit(1)
	}
	if *dryRun {
		logger.Info("seedgen dry run", "profile", *profile, "users", selected.users, "creators", selected.creators, "notes", selected.notes, "comments", selected.comments, "likes", selected.likes, "collects", selected.collects, "comment_likes", selected.commentLikes)
		return
	}

	ctx := context.Background()
	db, err := database.NewPostgresDB(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	redisClient, err := cache.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		logger.Warn("connect redis failed; rankings will not be populated", "error", err)
		redisClient = nil
	} else {
		defer redisClient.Close()
	}

	startedAt := time.Now()
	rng := rand.New(rand.NewSource(*seed))

	if *truncate {
		if err := truncateGeneratedData(ctx, db); err != nil {
			logger.Error("truncate failed", "error", err)
			os.Exit(1)
		}
		if redisClient != nil {
			_ = redisClient.FlushDB(ctx).Err()
		}
	}

	ids, err := nextIDs(ctx, db, selected)
	if err != nil {
		logger.Error("load id offsets failed", "error", err)
		os.Exit(1)
	}

	tokens, err := seedUsers(ctx, db, rng, selected, ids, *withTokens)
	if err != nil {
		logger.Error("seed users failed", "error", err)
		os.Exit(1)
	}
	if *withTokens {
		if err := writeTokens(*tokenOut, tokens); err != nil {
			logger.Error("write tokens failed", "error", err)
			os.Exit(1)
		}
	}

	if err := seedNotes(ctx, db, rng, selected, ids); err != nil {
		logger.Error("seed notes failed", "error", err)
		os.Exit(1)
	}
	if err := seedComments(ctx, db, rng, selected, ids); err != nil {
		logger.Error("seed comments failed", "error", err)
		os.Exit(1)
	}
	if err := seedInteractions(ctx, db, rng, selected, ids); err != nil {
		logger.Error("seed interactions failed", "error", err)
		os.Exit(1)
	}
	if err := refreshCounters(ctx, db); err != nil {
		logger.Error("refresh counters failed", "error", err)
		os.Exit(1)
	}
	if redisClient != nil {
		if err := populateRankings(ctx, db, redisClient); err != nil {
			logger.Error("populate rankings failed", "error", err)
			os.Exit(1)
		}
	}

	logger.Info("seedgen finished", "profile", *profile, "duration", time.Since(startedAt).String(), "tokens", len(tokens))
}

type idOffsets struct {
	userStart    int64
	noteStart    int64
	commentStart int64
}

type generatedToken struct {
	UserID int64
	Token  string
}

func selectProfile(profile string) (profileConfig, error) {
	switch profile {
	case "dev":
		return devProfile, nil
	default:
		return profileConfig{}, fmt.Errorf("unsupported profile %q", profile)
	}
}

func nextIDs(ctx context.Context, db *sqlx.DB, profile profileConfig) (idOffsets, error) {
	var ids idOffsets
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 10000) + 1 FROM users`).Scan(&ids.userStart); err != nil {
		return idOffsets{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) + 1 FROM notes`).Scan(&ids.noteStart); err != nil {
		return idOffsets{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) + 1 FROM note_comments`).Scan(&ids.commentStart); err != nil {
		return idOffsets{}, err
	}
	return ids, nil
}

func truncateGeneratedData(ctx context.Context, db *sqlx.DB) error {
	if _, err := db.ExecContext(ctx, `
TRUNCATE TABLE
	 simulation_runs,
  note_comment_likes,
  note_collects,
  note_likes,
  note_shares,
  note_comments,
  note_media,
  notes,
  user_auth_tokens,
  user_sessions,
  user_credentials,
  users
RESTART IDENTITY CASCADE;
`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `SELECT setval('users_id_seq', 10000, true)`)
	return err
}

func seedUsers(ctx context.Context, db *sqlx.DB, rng *rand.Rand, profile profileConfig, ids idOffsets, withTokens bool) ([]generatedToken, error) {
	users := newBatchInserter(db, `INSERT INTO users (id, username, nickname, avatar_url, role, persona, status, created_at, updated_at) VALUES `, ` ON CONFLICT (id) DO NOTHING`, 9, 1000)
	tokens := newBatchInserter(db, `INSERT INTO user_auth_tokens (user_id, token_hash, status, created_at) VALUES `, ` ON CONFLICT (token_hash) DO NOTHING`, 4, 1000)
	generated := make([]generatedToken, 0, profile.users)
	now := time.Now().Add(-90 * 24 * time.Hour)

	for i := 0; i < profile.users; i++ {
		userID := ids.userStart + int64(i)
		role := "normal"
		persona := randomChoice(rng, []string{"lurker", "searcher", "collector", "commenter"})
		if i < profile.creators {
			role = "creator"
			persona = randomChoice(rng, []string{"creator_micro", "creator_mid", "creator_top"})
		}
		createdAt := now.Add(time.Duration(rng.Intn(90*24)) * time.Hour)
		username := fmt.Sprintf("seed_user_%d", userID)
		users.add(userID, username, fmt.Sprintf("Seed User %d", userID), "", role, persona, "active", createdAt, createdAt)

		if withTokens {
			token := fmt.Sprintf("dev_%d_%s", userID, randomString(rng, 24))
			tokens.add(userID, hashToken(token), "active", createdAt)
			generated = append(generated, generatedToken{UserID: userID, Token: token})
		}
	}
	if err := users.flush(ctx); err != nil {
		return nil, err
	}
	if withTokens {
		if err := tokens.flush(ctx); err != nil {
			return nil, err
		}
	}
	return generated, nil
}

func seedNotes(ctx context.Context, db *sqlx.DB, rng *rand.Rand, profile profileConfig, ids idOffsets) error {
	notes := newBatchInserter(db, `INSERT INTO notes (id, project_id, author_id, title, body, category, topics, tags, location, product_entities, note_type, status, created_at, updated_at) VALUES `, ` ON CONFLICT (id) DO NOTHING`, 14, 500)
	media := newBatchInserter(db, `INSERT INTO note_media (note_id, media_type, url, caption, ocr_text, position, metadata, created_at) VALUES `, ``, 8, 1000)
	categories := []string{"beauty", "fashion", "food", "travel", "home", "fitness", "career", "digital", "study", "local_life"}
	now := time.Now().Add(-120 * 24 * time.Hour)

	for i := 0; i < profile.notes; i++ {
		noteID := ids.noteStart + int64(i)
		authorID := ids.userStart + int64(rng.Intn(profile.creators))
		category := randomChoice(rng, categories)
		createdAt := now.Add(time.Duration(rng.Intn(120*24)) * time.Hour)
		title := fmt.Sprintf("%s note %d", strings.ReplaceAll(category, "_", " "), noteID)
		body := fmt.Sprintf("Seed note about %s with practical checklist, experience, and searchable OCR text.", category)
		tags := fmt.Sprintf(`["%s","seed","guide"]`, category)
		notes.add(noteID, 0, authorID, title, body, category, `["seed_topic"]`, tags, `{}`, `[]`, "image_text", "published", createdAt, createdAt)
		media.add(noteID, "image", fmt.Sprintf("https://example.com/seed/%d.jpg", noteID), "seed image caption", fmt.Sprintf("%s OCR checklist tips", category), 1, `{}`, createdAt)
	}
	if err := notes.flush(ctx); err != nil {
		return err
	}
	return media.flush(ctx)
}

func seedComments(ctx context.Context, db *sqlx.DB, rng *rand.Rand, profile profileConfig, ids idOffsets) error {
	comments := newBatchInserter(db, `INSERT INTO note_comments (id, note_id, user_id, parent_id, root_id, content, intent, status, created_at, updated_at) VALUES `, ` ON CONFLICT (id) DO NOTHING`, 10, 1000)
	now := time.Now().Add(-90 * 24 * time.Hour)
	intents := []string{"ask_link", "ask_price", "ask_usage", "ask_suitable", "positive_feedback", "experience_share"}

	for i := 0; i < profile.comments; i++ {
		commentID := ids.commentStart + int64(i)
		noteID := ids.noteStart + int64(rng.Intn(profile.notes))
		userID := ids.userStart + int64(rng.Intn(profile.users))
		createdAt := now.Add(time.Duration(rng.Intn(90*24)) * time.Hour)
		content := fmt.Sprintf("Seed comment %d: useful detail, asking for more info.", commentID)
		comments.add(commentID, noteID, userID, 0, 0, content, randomChoice(rng, intents), 1, createdAt, createdAt)
	}
	return comments.flush(ctx)
}

func seedInteractions(ctx context.Context, db *sqlx.DB, rng *rand.Rand, profile profileConfig, ids idOffsets) error {
	if err := seedUniquePairsDB(ctx, db, rng, profile.likes, profile.notes, profile.users, ids.noteStart, ids.userStart, `INSERT INTO note_likes (note_id, user_id, created_at) VALUES `, ` ON CONFLICT (note_id, user_id) DO NOTHING`); err != nil {
		return err
	}
	if err := seedUniquePairsDB(ctx, db, rng, profile.collects, profile.notes, profile.users, ids.noteStart, ids.userStart, `INSERT INTO note_collects (note_id, user_id, created_at) VALUES `, ` ON CONFLICT (note_id, user_id) DO NOTHING`); err != nil {
		return err
	}
	return seedUniquePairsDB(ctx, db, rng, profile.commentLikes, profile.comments, profile.users, ids.commentStart, ids.userStart, `INSERT INTO note_comment_likes (comment_id, user_id, created_at) VALUES `, ` ON CONFLICT (comment_id, user_id) DO NOTHING`)
}

func refreshCounters(ctx context.Context, db *sqlx.DB) error {
	queries := []string{
		`UPDATE notes n SET comment_count = c.cnt FROM (SELECT note_id, COUNT(*) cnt FROM note_comments WHERE status = 1 GROUP BY note_id) c WHERE n.id = c.note_id`,
		`UPDATE notes n SET like_count = c.cnt FROM (SELECT note_id, COUNT(*) cnt FROM note_likes GROUP BY note_id) c WHERE n.id = c.note_id`,
		`UPDATE notes n SET collect_count = c.cnt FROM (SELECT note_id, COUNT(*) cnt FROM note_collects GROUP BY note_id) c WHERE n.id = c.note_id`,
		`UPDATE note_comments c SET like_count = x.cnt FROM (SELECT comment_id, COUNT(*) cnt FROM note_comment_likes GROUP BY comment_id) x WHERE c.id = x.comment_id`,
		`UPDATE notes SET hot_score = view_count + like_count * 3 + collect_count * 8 + comment_count * 6 + share_count * 5`,
		`SELECT setval('users_id_seq', GREATEST((SELECT COALESCE(MAX(id), 10000) FROM users), 10000), true)`,
		`SELECT setval('notes_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM notes), 1), true)`,
		`SELECT setval('note_comments_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM note_comments), 1), true)`,
	}
	for _, query := range queries {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func populateRankings(ctx context.Context, db *sqlx.DB, redisClient *redis.Client) error {
	rows, err := db.QueryxContext(ctx, `SELECT id, category, hot_score FROM notes WHERE status = 'published'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	pipe := redisClient.Pipeline()
	pipe.Del(ctx, "ranking:notes:daily")
	categoryKeys := map[string]struct{}{}
	for rows.Next() {
		var id int64
		var category string
		var score float64
		if err := rows.Scan(&id, &category, &score); err != nil {
			return err
		}
		member := fmt.Sprint(id)
		pipe.ZAdd(ctx, "ranking:notes:daily", redis.Z{Score: score, Member: member})
		key := fmt.Sprintf("ranking:notes:%s:daily", category)
		if _, ok := categoryKeys[key]; !ok {
			pipe.Del(ctx, key)
			categoryKeys[key] = struct{}{}
		}
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: member})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	commentRows, err := db.QueryxContext(ctx, `SELECT id, note_id, like_count FROM note_comments WHERE status = 1 AND like_count > 0`)
	if err != nil {
		return err
	}
	defer commentRows.Close()
	commentKeys := map[string]struct{}{}
	for commentRows.Next() {
		var id, noteID, likeCount int64
		if err := commentRows.Scan(&id, &noteID, &likeCount); err != nil {
			return err
		}
		key := fmt.Sprintf("note:%d:hot_comments", noteID)
		if _, ok := commentKeys[key]; !ok {
			pipe.Del(ctx, key)
			commentKeys[key] = struct{}{}
		}
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(likeCount * 5), Member: fmt.Sprint(id)})
	}
	if err := commentRows.Err(); err != nil {
		return err
	}
	_, err = pipe.Exec(ctx)
	return err
}

type batchInserter struct {
	db        *sqlx.DB
	prefix    string
	suffix    string
	cols      int
	batchSize int
	rows      [][]any
}

func newBatchInserter(db *sqlx.DB, prefix string, suffix string, cols int, batchSize int) *batchInserter {
	return &batchInserter{db: db, prefix: prefix, suffix: suffix, cols: cols, batchSize: batchSize}
}

func (b *batchInserter) add(values ...any) error {
	if len(values) != b.cols {
		return fmt.Errorf("batch row has %d values, want %d", len(values), b.cols)
	}
	b.rows = append(b.rows, values)
	return nil
}

func (b *batchInserter) flush(ctx context.Context) error {
	for len(b.rows) > 0 {
		n := b.batchSize
		if len(b.rows) < n {
			n = len(b.rows)
		}
		if err := b.exec(ctx, b.rows[:n]); err != nil {
			return err
		}
		b.rows = b.rows[n:]
	}
	return nil
}

func (b *batchInserter) exec(ctx context.Context, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	var builder strings.Builder
	args := make([]any, 0, len(rows)*b.cols)
	builder.WriteString(b.prefix)
	argIndex := 1
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			builder.WriteString(",")
		}
		builder.WriteString("(")
		for col := 0; col < b.cols; col++ {
			if col > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(fmt.Sprintf("$%d", argIndex))
			argIndex++
		}
		builder.WriteString(")")
		args = append(args, row...)
	}
	builder.WriteString(b.suffix)
	_, err := b.db.ExecContext(ctx, builder.String(), args...)
	return err
}

func seedUniquePairsDB(ctx context.Context, db *sqlx.DB, rng *rand.Rand, target int, leftCount int, rightCount int, leftStart int64, rightStart int64, prefix string, suffix string) error {
	inserter := newBatchInserter(db, prefix, suffix, 3, 2000)
	seen := make(map[[2]int64]struct{}, target)
	now := time.Now()
	for len(seen) < target {
		leftID := leftStart + int64(rng.Intn(leftCount))
		rightID := rightStart + int64(rng.Intn(rightCount))
		key := [2]int64{leftID, rightID}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		inserter.add(leftID, rightID, now.Add(-time.Duration(rng.Intn(30*24))*time.Hour))
	}
	return inserter.flush(ctx)
}

func randomChoice(rng *rand.Rand, values []string) string {
	return values[rng.Intn(len(values))]
}

func randomString(rng *rand.Rand, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var builder strings.Builder
	for i := 0; i < n; i++ {
		builder.WriteByte(alphabet[rng.Intn(len(alphabet))])
	}
	return builder.String()
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeTokens(path string, tokens []generatedToken) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{"user_id", "token"}); err != nil {
		return err
	}
	for _, token := range tokens {
		if err := writer.Write([]string{fmt.Sprint(token.UserID), token.Token}); err != nil {
			return err
		}
	}
	return nil
}
