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
	"creatorinsight/backend-go/internal/contentgen"
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

var capacityProfile = profileConfig{
	users:        10000,
	creators:     1000,
	notes:        50000,
	comments:     500000,
	likes:        2000000,
	collects:     600000,
	commentLikes: 1000000,
}

var millionCommentsProfile = profileConfig{
	users:        20000,
	creators:     2000,
	notes:        100000,
	comments:     1000000,
	likes:        5000000,
	collects:     1500000,
	commentLikes: 3000000,
}

func main() {
	var (
		profile      = flag.String("profile", "dev", "seed profile: dev, capacity, or million-comments")
		seed         = flag.Int64("seed", 20260706, "random seed")
		truncate     = flag.Bool("truncate", false, "truncate generated data before seeding")
		withTokens   = flag.Bool("with-tokens", false, "generate user_auth_tokens and token csv")
		dryRun       = flag.Bool("dry-run", false, "print planned data volume without writing")
		tokenOut     = flag.String("token-out", "tmp/dev_tokens.csv", "token csv output path")
		users        = flag.Int("users", 0, "override profile user count")
		creators     = flag.Int("creators", 0, "override profile creator count")
		notes        = flag.Int("notes", 0, "override profile note count")
		comments     = flag.Int("comments", 0, "override profile comment count")
		likes        = flag.Int("likes", 0, "override profile note like count")
		collects     = flag.Int("collects", 0, "override profile note collect count")
		commentLikes = flag.Int("comment-likes", 0, "override profile comment like count")
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
	selected = selected.withOverrides(profileConfig{
		users:        *users,
		creators:     *creators,
		notes:        *notes,
		comments:     *comments,
		likes:        *likes,
		collects:     *collects,
		commentLikes: *commentLikes,
	})
	if err := selected.validate(); err != nil {
		logger.Error("invalid seed profile", "error", err)
		os.Exit(1)
	}
	if *dryRun {
		logger.Info("seedgen dry run", "profile", *profile, "users", selected.users, "creators", selected.creators, "notes", selected.notes, "comments", selected.comments, "likes", selected.likes, "collects", selected.collects, "comment_likes", selected.commentLikes, "estimated_rows", selected.estimatedRows())
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

	if err := seedNotes(ctx, db, rng, selected, ids, *seed); err != nil {
		logger.Error("seed notes failed", "error", err)
		os.Exit(1)
	}
	if err := seedComments(ctx, db, rng, selected, ids, *seed); err != nil {
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
	case "capacity":
		return capacityProfile, nil
	case "million-comments":
		return millionCommentsProfile, nil
	default:
		return profileConfig{}, fmt.Errorf("unsupported profile %q", profile)
	}
}

func (p profileConfig) withOverrides(overrides profileConfig) profileConfig {
	if overrides.users > 0 {
		p.users = overrides.users
	}
	if overrides.creators > 0 {
		p.creators = overrides.creators
	}
	if overrides.notes > 0 {
		p.notes = overrides.notes
	}
	if overrides.comments > 0 {
		p.comments = overrides.comments
	}
	if overrides.likes > 0 {
		p.likes = overrides.likes
	}
	if overrides.collects > 0 {
		p.collects = overrides.collects
	}
	if overrides.commentLikes > 0 {
		p.commentLikes = overrides.commentLikes
	}
	return p
}

func (p profileConfig) validate() error {
	if p.users <= 0 || p.creators <= 0 || p.notes <= 0 || p.comments <= 0 {
		return fmt.Errorf("users, creators, notes, and comments must be positive")
	}
	if p.creators > p.users {
		return fmt.Errorf("creators %d exceed users %d", p.creators, p.users)
	}
	for name, value := range map[string]int{"likes": p.likes, "collects": p.collects, "comment_likes": p.commentLikes} {
		if value < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	if int64(p.likes) > int64(p.notes)*int64(p.users) {
		return fmt.Errorf("likes exceed unique note/user pairs")
	}
	if int64(p.collects) > int64(p.notes)*int64(p.users) {
		return fmt.Errorf("collects exceed unique note/user pairs")
	}
	if int64(p.commentLikes) > int64(p.comments)*int64(p.users) {
		return fmt.Errorf("comment likes exceed unique comment/user pairs")
	}
	return nil
}

func (p profileConfig) estimatedRows() int64 {
	// GenerateDocument currently emits one media row per note.
	return int64(p.users) + int64(p.notes)*2 + int64(p.comments) + int64(p.likes) + int64(p.collects) + int64(p.commentLikes)
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
	 content_corpus_runs,
	 simulation_runs,
	 processed_events,
	 behavior_events,
	 outbox_events,
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
		if err := users.add(ctx, userID, username, fmt.Sprintf("Seed User %d", userID), "", role, persona, "active", createdAt, createdAt); err != nil {
			return nil, err
		}

		if withTokens {
			token := fmt.Sprintf("dev_%d_%s", userID, randomString(rng, 24))
			if err := tokens.add(ctx, userID, hashToken(token), "active", createdAt); err != nil {
				return nil, err
			}
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

func seedNotes(ctx context.Context, db *sqlx.DB, rng *rand.Rand, profile profileConfig, ids idOffsets, seed int64) error {
	notes := newBatchInserter(db, `INSERT INTO notes (id, project_id, author_id, title, body, category, topics, tags, location, product_entities, note_type, quality_score, status, created_at, updated_at) VALUES `, ` ON CONFLICT (id) DO NOTHING`, 15, 500)
	media := newBatchInserter(db, `INSERT INTO note_media (note_id, media_type, url, caption, ocr_text, position, metadata, created_at) VALUES `, ``, 8, 1000)
	categories := contentgen.Categories()
	startAt := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

	for i := 0; i < profile.notes; i++ {
		noteID := ids.noteStart + int64(i)
		authorID := ids.userStart + int64(rng.Intn(profile.creators))
		category := categoryForNote(seed, noteID, categories)
		document, err := contentgen.GenerateDocument(seed, noteID, category, 1, startAt)
		if err != nil {
			return err
		}
		document.AuthorID = authorID
		topics, err := contentgen.JSON(document.Topics)
		if err != nil {
			return err
		}
		tags, err := contentgen.JSON(document.Tags)
		if err != nil {
			return err
		}
		location, err := contentgen.JSON(document.Location)
		if err != nil {
			return err
		}
		products, err := contentgen.JSON(document.ProductEntities)
		if err != nil {
			return err
		}
		if err := notes.add(ctx, noteID, 0, authorID, document.Title, document.Body, category, topics, tags, location, products, "image_text", document.QualityScore, "published", document.CreatedAt, document.CreatedAt); err != nil {
			return err
		}
		for _, asset := range document.Media {
			metadata, err := contentgen.JSON(asset.Metadata)
			if err != nil {
				return err
			}
			if err := media.add(ctx, noteID, "image", nil, asset.Caption, asset.OCRText, asset.Position, metadata, document.CreatedAt); err != nil {
				return err
			}
		}
	}
	if err := notes.flush(ctx); err != nil {
		return err
	}
	if err := media.flush(ctx); err != nil {
		return err
	}
	return nil
}

func seedComments(ctx context.Context, db *sqlx.DB, rng *rand.Rand, profile profileConfig, ids idOffsets, seed int64) error {
	comments := newBatchInserter(db, `INSERT INTO note_comments (id, note_id, user_id, parent_id, root_id, content, sentiment, intent, topic_id, status, created_at, updated_at) VALUES `, ` ON CONFLICT (id) DO NOTHING`, 12, 1000)
	categories := contentgen.Categories()
	startAt := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	commentIndex := 0

	for _, tier := range buildCommentTiers(profile.notes, profile.comments) {
		for noteOffset := 0; noteOffset < tier.noteCount; noteOffset++ {
			absoluteOffset := tier.noteStart + noteOffset
			noteID := ids.noteStart + int64(absoluteOffset)
			category := categoryForNote(seed, noteID, categories)
			document, err := contentgen.GenerateDocument(seed, noteID, category, 1, startAt)
			if err != nil {
				return err
			}
			commentCount := tier.commentCount / tier.noteCount
			if noteOffset < tier.commentCount%tier.noteCount {
				commentCount++
			}
			for localIndex := 0; localIndex < commentCount; localIndex++ {
				commentID := ids.commentStart + int64(commentIndex)
				userID := ids.userStart + int64(rng.Intn(profile.users))
				comment := contentgen.GenerateComment(seed, document, commentID, commentIndex)
				if err := comments.add(ctx, commentID, noteID, userID, 0, 0, comment.Content, comment.Sentiment, comment.Intent, comment.TopicID, 1, comment.CreatedAt, comment.CreatedAt); err != nil {
					return err
				}
				commentIndex++
			}
		}
	}
	if commentIndex != profile.comments {
		return fmt.Errorf("generated %d comments, want %d", commentIndex, profile.comments)
	}
	return comments.flush(ctx)
}

type commentTier struct {
	noteStart    int
	noteCount    int
	commentCount int
}

func buildCommentTiers(noteCount int, commentCount int) []commentTier {
	hotNotes := noteCount / 20
	if hotNotes < 1 {
		hotNotes = 1
	}
	warmNotes := noteCount / 5
	if warmNotes < 1 && noteCount > hotNotes {
		warmNotes = 1
	}
	if hotNotes+warmNotes > noteCount {
		warmNotes = noteCount - hotNotes
	}
	tailNotes := noteCount - hotNotes - warmNotes

	hotComments := commentCount * 40 / 100
	warmComments := commentCount * 40 / 100
	tailComments := commentCount - hotComments - warmComments
	if warmNotes == 0 {
		hotComments += warmComments
		warmComments = 0
	}
	if tailNotes == 0 {
		if warmNotes > 0 {
			warmComments += tailComments
		} else {
			hotComments += tailComments
		}
		tailComments = 0
	}

	tiers := []commentTier{{noteStart: 0, noteCount: hotNotes, commentCount: hotComments}}
	if warmNotes > 0 {
		tiers = append(tiers, commentTier{noteStart: hotNotes, noteCount: warmNotes, commentCount: warmComments})
	}
	if tailNotes > 0 {
		tiers = append(tiers, commentTier{noteStart: hotNotes + warmNotes, noteCount: tailNotes, commentCount: tailComments})
	}
	return tiers
}

func categoryForNote(seed int64, noteID int64, categories []string) string {
	value := uint64(noteID) ^ uint64(seed)
	value ^= value >> 33
	value *= 0xff51afd7ed558ccd
	value ^= value >> 33
	value *= 0xc4ceb9fe1a85ec53
	value ^= value >> 33
	return categories[value%uint64(len(categories))]
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
	if err := deleteRedisPattern(ctx, redisClient, "ranking:notes:*"); err != nil {
		return err
	}
	if err := deleteRedisPattern(ctx, redisClient, "note:*:hot_comments"); err != nil {
		return err
	}

	rows, err := db.QueryxContext(ctx, `SELECT id, category, hot_score FROM notes WHERE status = 'published'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	pipe := redisClient.Pipeline()
	queued := 0
	flush := func() error {
		if queued == 0 {
			return nil
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		pipe = redisClient.Pipeline()
		queued = 0
		return nil
	}
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
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: member})
		queued += 2
		if queued >= 5000 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}

	commentRows, err := db.QueryxContext(ctx, `SELECT id, note_id, like_count FROM note_comments WHERE status = 1 AND like_count > 0`)
	if err != nil {
		return err
	}
	defer commentRows.Close()
	for commentRows.Next() {
		var id, noteID, likeCount int64
		if err := commentRows.Scan(&id, &noteID, &likeCount); err != nil {
			return err
		}
		key := fmt.Sprintf("note:%d:hot_comments", noteID)
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(likeCount * 5), Member: fmt.Sprint(id)})
		queued++
		if queued >= 5000 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := commentRows.Err(); err != nil {
		return err
	}
	return flush()
}

func deleteRedisPattern(ctx context.Context, redisClient *redis.Client, pattern string) error {
	var cursor uint64
	for {
		keys, nextCursor, err := redisClient.Scan(ctx, cursor, pattern, 1000).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := redisClient.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			return nil
		}
	}
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

func (b *batchInserter) add(ctx context.Context, values ...any) error {
	if len(values) != b.cols {
		return fmt.Errorf("batch row has %d values, want %d", len(values), b.cols)
	}
	b.rows = append(b.rows, values)
	if len(b.rows) >= b.batchSize {
		return b.flush(ctx)
	}
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
	sequence, err := newUniquePairSequence(rng, target, leftCount, rightCount)
	if err != nil {
		return err
	}
	now := time.Now()
	for {
		leftOffset, rightOffset, ok := sequence.next()
		if !ok {
			break
		}
		if err := inserter.add(ctx, leftStart+int64(leftOffset), rightStart+int64(rightOffset), now.Add(-time.Duration(rng.Intn(30*24))*time.Hour)); err != nil {
			return err
		}
	}
	return inserter.flush(ctx)
}

type uniquePairSequence struct {
	remaining int
	right     uint64
	total     uint64
	current   uint64
	stride    uint64
}

func newUniquePairSequence(rng *rand.Rand, target int, leftCount int, rightCount int) (*uniquePairSequence, error) {
	if target < 0 || leftCount <= 0 || rightCount <= 0 {
		return nil, fmt.Errorf("invalid pair dimensions target=%d left=%d right=%d", target, leftCount, rightCount)
	}
	total := uint64(leftCount) * uint64(rightCount)
	if uint64(target) > total {
		return nil, fmt.Errorf("target %d exceeds %d unique pairs", target, total)
	}
	if total > uint64(^uint64(0)>>1) {
		return nil, fmt.Errorf("pair space %d exceeds supported deterministic sequence size", total)
	}
	current := uint64(0)
	stride := uint64(1)
	if total > 1 {
		current = uint64(rng.Int63n(int64(total)))
		stride = uint64(rng.Int63n(int64(total-1))) + 1
		for greatestCommonDivisor(stride, total) != 1 {
			stride++
			if stride >= total {
				stride = 1
			}
		}
	}
	return &uniquePairSequence{remaining: target, right: uint64(rightCount), total: total, current: current, stride: stride}, nil
}

func (s *uniquePairSequence) next() (int, int, bool) {
	if s.remaining == 0 {
		return 0, 0, false
	}
	flat := s.current
	s.current = (s.current + s.stride) % s.total
	s.remaining--
	return int(flat / s.right), int(flat % s.right), true
}

func greatestCommonDivisor(left uint64, right uint64) uint64 {
	for right != 0 {
		left, right = right, left%right
	}
	return left
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
