package reconcile

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/redis/go-redis/v9"
)

const (
	globalNoteRankingKey = "ranking:notes:daily"
	commentCachePattern  = "note:*:comments:first_page:time"
)

type Reconciler struct {
	repo         *Repository
	redis        *redis.Client
	logger       *slog.Logger
	enabled      bool
	startupDelay time.Duration
	interval     time.Duration
	timeout      time.Duration
	rankingLimit int64
}

type Deps struct {
	Repository   *Repository
	Redis        *redis.Client
	Logger       *slog.Logger
	Enabled      bool
	StartupDelay time.Duration
	Interval     time.Duration
	Timeout      time.Duration
	RankingLimit int64
}

type Result struct {
	NotesRepaired        int64
	CommentsRepaired     int64
	NoteRankingKeys      int
	CommentRankingKeys   int
	InvalidatedCacheKeys int
}

func New(deps Deps) *Reconciler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		repo:         deps.Repository,
		redis:        deps.Redis,
		logger:       logger,
		enabled:      deps.Enabled,
		startupDelay: deps.StartupDelay,
		interval:     deps.Interval,
		timeout:      deps.Timeout,
		rankingLimit: deps.RankingLimit,
	}
}

func (r *Reconciler) Start(ctx context.Context) {
	if !r.enabled || r.repo == nil {
		r.logger.Info("reconciler disabled")
		return
	}
	go r.loop(ctx)
}

func (r *Reconciler) loop(ctx context.Context) {
	if !waitOrDone(ctx, r.startupDelay) {
		return
	}
	r.runLogged(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runLogged(ctx)
		}
	}
}

func (r *Reconciler) runLogged(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	result, err := r.RunOnce(ctx)
	if err != nil {
		r.logger.Warn("derived data reconcile failed", "error", err)
		return
	}
	r.logger.Info("derived data reconcile completed",
		"notes_repaired", result.NotesRepaired,
		"comments_repaired", result.CommentsRepaired,
		"note_ranking_keys", result.NoteRankingKeys,
		"comment_ranking_keys", result.CommentRankingKeys,
		"cache_keys_invalidated", result.InvalidatedCacheKeys,
	)
}

func (r *Reconciler) RunOnce(ctx context.Context) (result Result, err error) {
	startedAt := time.Now()
	defer func() {
		observability.ObserveReconcile(startedAt, err, result.NotesRepaired, result.CommentsRepaired)
	}()

	repaired, err := r.repo.ReconcileCounters(ctx)
	if err != nil {
		return Result{}, err
	}
	result.NotesRepaired = repaired.NotesRepaired
	result.CommentsRepaired = repaired.CommentsRepaired
	if r.redis == nil {
		return result, nil
	}

	noteEntries, err := r.repo.ListNoteRankings(ctx, r.rankingLimit)
	if err != nil {
		return result, fmt.Errorf("load note rankings: %w", err)
	}
	commentEntries, err := r.repo.ListCommentRankings(ctx, r.rankingLimit)
	if err != nil {
		return result, fmt.Errorf("load comment rankings: %w", err)
	}

	noteSets := buildNoteSets(noteEntries, r.rankingLimit)
	commentSets := buildCommentSets(commentEntries, r.rankingLimit)
	invalidated, err := r.replaceRedisDerivedData(ctx, noteSets, commentSets)
	if err != nil {
		return result, err
	}
	result.NoteRankingKeys = len(noteSets)
	result.CommentRankingKeys = len(commentSets)
	result.InvalidatedCacheKeys = invalidated
	return result, nil
}

func buildNoteSets(entries []NoteRankingEntry, limit int64) map[string][]redis.Z {
	sets := map[string][]redis.Z{globalNoteRankingKey: {}}
	categoryCounts := make(map[string]int64)
	for index, entry := range entries {
		member := strconv.FormatInt(entry.ID, 10)
		if entry.Scope == "global" || (entry.Scope == "" && int64(index) < limit) {
			sets[globalNoteRankingKey] = append(sets[globalNoteRankingKey], redis.Z{Score: entry.Score, Member: member})
		}
		if entry.Scope == "global" {
			continue
		}
		category := strings.TrimSpace(entry.Category)
		if category == "" || categoryCounts[category] >= limit {
			continue
		}
		key := fmt.Sprintf("ranking:notes:%s:daily", category)
		sets[key] = append(sets[key], redis.Z{Score: entry.Score, Member: member})
		categoryCounts[category]++
	}
	return sets
}

func buildCommentSets(entries []CommentRankingEntry, limit int64) map[string][]redis.Z {
	sets := make(map[string][]redis.Z)
	counts := make(map[int64]int64)
	for _, entry := range entries {
		if counts[entry.NoteID] >= limit {
			continue
		}
		key := fmt.Sprintf("note:%d:hot_comments", entry.NoteID)
		sets[key] = append(sets[key], redis.Z{Score: entry.Score, Member: strconv.FormatInt(entry.ID, 10)})
		counts[entry.NoteID]++
	}
	return sets
}

func (r *Reconciler) replaceRedisDerivedData(ctx context.Context, noteSets map[string][]redis.Z, commentSets map[string][]redis.Z) (int, error) {
	keysToDelete := map[string]struct{}{globalNoteRankingKey: {}}
	for _, pattern := range []string{"ranking:notes:*:daily", "note:*:hot_comments"} {
		keys, err := scanKeys(ctx, r.redis, pattern)
		if err != nil {
			return 0, fmt.Errorf("scan ranking keys: %w", err)
		}
		for _, key := range keys {
			keysToDelete[key] = struct{}{}
		}
	}

	cacheKeys, err := scanKeys(ctx, r.redis, commentCachePattern)
	if err != nil {
		return 0, fmt.Errorf("scan comment cache keys: %w", err)
	}
	detailCandidates, err := scanKeys(ctx, r.redis, "note:*")
	if err != nil {
		return 0, fmt.Errorf("scan note cache keys: %w", err)
	}
	for _, key := range detailCandidates {
		parts := strings.Split(key, ":")
		if len(parts) == 2 {
			if _, parseErr := strconv.ParseInt(parts[1], 10, 64); parseErr == nil {
				cacheKeys = append(cacheKeys, key)
			}
		}
	}

	_, err = r.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		for key := range keysToDelete {
			pipe.Del(ctx, key)
		}
		for _, key := range cacheKeys {
			pipe.Del(ctx, key)
		}
		for key, members := range noteSets {
			if len(members) > 0 {
				pipe.ZAdd(ctx, key, members...)
			}
		}
		for key, members := range commentSets {
			if len(members) > 0 {
				pipe.ZAdd(ctx, key, members...)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("replace redis derived data: %w", err)
	}
	for range noteSets {
		observability.IncHotRankingUpdate("notes_reconcile")
	}
	for range commentSets {
		observability.IncHotRankingUpdate("hot_comments_reconcile")
	}
	return len(cacheKeys), nil
}

func scanKeys(ctx context.Context, client *redis.Client, pattern string) ([]string, error) {
	keys := make([]string, 0)
	iter := client.Scan(ctx, 0, pattern, 250).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func waitOrDone(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return true
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
