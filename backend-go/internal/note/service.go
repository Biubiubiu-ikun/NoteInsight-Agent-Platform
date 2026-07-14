package note

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	defaultNoteLimit    = 20
	maxNoteLimit        = 100
	defaultCommentLimit = 20
	maxCommentLimit     = 100
	noteDetailCacheTTL  = 10 * time.Minute
	commentFirstPageTTL = 60 * time.Second
	cacheLoadTimeout    = 5 * time.Second
)

type noteRepository interface {
	CreateNote(ctx context.Context, input CreateNoteInput) (Note, error)
	GetNote(ctx context.Context, id int64) (Note, error)
	ListNotes(ctx context.Context, input ListNotesInput, cursor keysetCursor) ([]Note, bool, error)
	CreateComment(ctx context.Context, noteID int64, input CreateCommentInput) (NoteComment, error)
	ListComments(ctx context.Context, noteID int64, input ListCommentsInput, cursor keysetCursor) ([]NoteComment, bool, error)
	LikeNote(ctx context.Context, noteID int64, userID int64) (IdempotentActionResult, error)
	CollectNote(ctx context.Context, noteID int64, userID int64, collectionName string) (IdempotentActionResult, error)
	ShareNote(ctx context.Context, noteID int64, userID int64, channel string) (ShareNoteResult, error)
	LikeComment(ctx context.Context, commentID int64, userID int64) (IdempotentActionResult, error)
	UpdateNote(ctx context.Context, noteID int64, input UpdateNoteInput) (Note, error)
	SoftDeleteNote(ctx context.Context, noteID int64) error
	SoftDeleteComment(ctx context.Context, commentID int64, actorUserID int64) (int64, error)
	GetNoteOwner(ctx context.Context, noteID int64) (int64, error)
	GetCommentOwner(ctx context.Context, commentID int64) (int64, error)
	GetNoteRankingStats(ctx context.Context, noteID int64) (NoteRankingStats, error)
	UpdateNoteHotScore(ctx context.Context, noteID int64, hotScore float64) (float64, error)
	GetCommentRankingInfo(ctx context.Context, commentID int64) (CommentRankingInfo, error)
}

type Service struct {
	repo             noteRepository
	redis            *redis.Client
	noteLoads        singleflight.Group
	commentPageLoads singleflight.Group
}

func NewService(repo noteRepository, redisClient ...*redis.Client) *Service {
	service := &Service{repo: repo}
	if len(redisClient) > 0 {
		service.redis = redisClient[0]
	}
	return service
}

func (s *Service) CreateNote(ctx context.Context, input CreateNoteInput) (Note, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	input.Category = normalizeToken(input.Category)

	if input.AuthorID <= 0 {
		return Note{}, ValidationError{Field: "author_id", Message: "must be greater than 0"}
	}
	if input.Title == "" {
		return Note{}, ValidationError{Field: "title", Message: "is required"}
	}
	if len(input.Title) > 255 {
		return Note{}, ValidationError{Field: "title", Message: "must be at most 255 characters"}
	}
	if input.Body == "" {
		return Note{}, ValidationError{Field: "body", Message: "is required"}
	}
	if len(input.Body) > 20000 {
		return Note{}, ValidationError{Field: "body", Message: "must be at most 20000 characters"}
	}
	if input.Category == "" {
		return Note{}, ValidationError{Field: "category", Message: "is required"}
	}
	if len(input.Category) > 64 {
		return Note{}, ValidationError{Field: "category", Message: "must be at most 64 characters"}
	}

	for i := range input.Media {
		input.Media[i].MediaType = normalizeToken(input.Media[i].MediaType)
		input.Media[i].URL = strings.TrimSpace(input.Media[i].URL)
		input.Media[i].Caption = strings.TrimSpace(input.Media[i].Caption)
		input.Media[i].OCRText = strings.TrimSpace(input.Media[i].OCRText)
		if input.Media[i].MediaType == "" {
			input.Media[i].MediaType = "image"
		}
		if len(input.Media[i].MediaType) > 32 {
			return Note{}, ValidationError{Field: "media.media_type", Message: "must be at most 32 characters"}
		}
		if len(input.Media[i].URL) > 512 {
			return Note{}, ValidationError{Field: "media.url", Message: "must be at most 512 characters"}
		}
		if input.Media[i].Position <= 0 {
			input.Media[i].Position = i + 1
		}
	}

	created, err := s.repo.CreateNote(ctx, input)
	if err != nil {
		return Note{}, err
	}
	s.setNoteCache(ctx, created)
	_ = s.refreshNoteRanking(ctx, created.ID)
	return created, nil
}

func (s *Service) UpdateNote(ctx context.Context, current auth.CurrentUser, input UpdateNoteInput) (Note, error) {
	noteID, err := parseID("note_id", input.NoteID)
	if err != nil {
		return Note{}, err
	}
	if current.ID <= 0 {
		return Note{}, auth.ErrUnauthorized
	}
	if current.Status != "active" {
		return Note{}, auth.ErrForbidden
	}

	if input.Title != nil {
		trimmed := strings.TrimSpace(*input.Title)
		input.Title = &trimmed
		if trimmed == "" {
			return Note{}, ValidationError{Field: "title", Message: "is required"}
		}
		if len(trimmed) > 255 {
			return Note{}, ValidationError{Field: "title", Message: "must be at most 255 characters"}
		}
	}
	if input.Body != nil {
		trimmed := strings.TrimSpace(*input.Body)
		input.Body = &trimmed
		if trimmed == "" {
			return Note{}, ValidationError{Field: "body", Message: "is required"}
		}
		if len(trimmed) > 20000 {
			return Note{}, ValidationError{Field: "body", Message: "must be at most 20000 characters"}
		}
	}
	if input.Category != nil {
		trimmed := normalizeToken(*input.Category)
		input.Category = &trimmed
		if trimmed == "" {
			return Note{}, ValidationError{Field: "category", Message: "is required"}
		}
		if len(trimmed) > 64 {
			return Note{}, ValidationError{Field: "category", Message: "must be at most 64 characters"}
		}
	}

	allowed, err := s.CanModifyNote(ctx, input.NoteID, current)
	if err != nil {
		return Note{}, err
	}
	if !allowed {
		return Note{}, auth.ErrForbidden
	}

	updated, err := s.repo.UpdateNote(ctx, noteID, input)
	if err != nil {
		return Note{}, err
	}
	s.invalidateNoteCache(ctx, noteID)
	s.setNoteCache(ctx, updated)
	_ = s.refreshNoteRanking(ctx, noteID)
	return updated, nil
}

func (s *Service) DeleteNote(ctx context.Context, current auth.CurrentUser, rawID string) error {
	noteID, err := parseID("note_id", rawID)
	if err != nil {
		return err
	}
	if current.ID <= 0 {
		return auth.ErrUnauthorized
	}
	if current.Status != "active" {
		return auth.ErrForbidden
	}

	allowed, err := s.CanModifyNote(ctx, rawID, current)
	if err != nil {
		return err
	}
	if !allowed {
		return auth.ErrForbidden
	}
	if err := s.repo.SoftDeleteNote(ctx, noteID); err != nil {
		return err
	}
	s.invalidateNoteCache(ctx, noteID)
	_ = s.removeNoteRanking(ctx, noteID)
	return nil
}

func (s *Service) GetNote(ctx context.Context, rawID string) (Note, error) {
	id, err := parseID("note_id", rawID)
	if err != nil {
		return Note{}, err
	}
	if note, ok := s.getNoteCache(ctx, id); ok {
		return note, nil
	}

	resultChannel := s.noteLoads.DoChan(strconv.FormatInt(id, 10), func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheLoadTimeout)
		defer cancel()
		if note, ok := s.getNoteCache(loadCtx, id); ok {
			return note, nil
		}
		note, loadErr := s.repo.GetNote(loadCtx, id)
		if loadErr != nil {
			observability.IncCacheBackendLoad("note_detail", "error")
			return Note{}, loadErr
		}
		observability.IncCacheBackendLoad("note_detail", "success")
		s.setNoteCache(loadCtx, note)
		return note, nil
	})

	select {
	case <-ctx.Done():
		return Note{}, ctx.Err()
	case result := <-resultChannel:
		if result.Shared {
			observability.IncCacheCoalescedRequest("note_detail")
		}
		if result.Err != nil {
			return Note{}, result.Err
		}
		note, ok := result.Val.(Note)
		if !ok {
			return Note{}, fmt.Errorf("unexpected shared note result %T", result.Val)
		}
		return note, nil
	}
}

func (s *Service) ListNotes(ctx context.Context, input ListNotesInput) (NotePage, error) {
	input.Category = normalizeToken(input.Category)
	if input.Limit <= 0 {
		input.Limit = defaultNoteLimit
	}
	if input.Limit > maxNoteLimit {
		input.Limit = maxNoteLimit
	}

	cursor, err := decodeNoteCursor(input.Cursor)
	if err != nil {
		return NotePage{}, err
	}

	items, hasMore, err := s.repo.ListNotes(ctx, input, cursor)
	if err != nil {
		return NotePage{}, err
	}

	page := NotePage{Items: items}
	if hasMore && len(items) > 0 {
		nextCursor, err := encodeNoteCursor(items[len(items)-1])
		if err != nil {
			return NotePage{}, err
		}
		page.NextCursor = nextCursor
	}
	return page, nil
}

func (s *Service) ListHotNotes(ctx context.Context, category string, limit int) (HotNotePage, error) {
	category = normalizeToken(category)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if s.redis == nil {
		return HotNotePage{Items: []HotNoteItem{}}, nil
	}

	key := hotNotesKey(category)
	values, err := s.redis.ZRevRangeWithScores(ctx, key, 0, int64(limit-1)).Result()
	if err != nil {
		return HotNotePage{}, err
	}

	items := make([]HotNoteItem, 0, len(values))
	for _, value := range values {
		noteID, err := strconv.ParseInt(fmt.Sprint(value.Member), 10, 64)
		if err != nil {
			continue
		}
		items = append(items, HotNoteItem{NoteID: noteID, Score: value.Score})
	}
	return HotNotePage{Items: items}, nil
}

func (s *Service) CreateComment(ctx context.Context, input CreateCommentInput) (NoteComment, error) {
	noteID, err := parseID("note_id", input.NoteID)
	if err != nil {
		return NoteComment{}, err
	}
	input.NoteID = strconv.FormatInt(noteID, 10)
	input.Content = strings.TrimSpace(input.Content)
	input.Intent = normalizeToken(input.Intent)

	if input.UserID <= 0 {
		return NoteComment{}, ValidationError{Field: "user_id", Message: "must be greater than 0"}
	}
	if input.Content == "" {
		return NoteComment{}, ValidationError{Field: "content", Message: "is required"}
	}
	if len(input.Content) > 2000 {
		return NoteComment{}, ValidationError{Field: "content", Message: "must be at most 2000 characters"}
	}
	if input.ParentID < 0 {
		return NoteComment{}, ValidationError{Field: "parent_id", Message: "must be greater than or equal to 0"}
	}
	if len(input.Intent) > 64 {
		return NoteComment{}, ValidationError{Field: "intent", Message: "must be at most 64 characters"}
	}

	comment, err := s.repo.CreateComment(ctx, noteID, input)
	if err != nil {
		return NoteComment{}, err
	}
	s.invalidateCommentFirstPage(ctx, noteID)
	s.invalidateNoteCache(ctx, noteID)
	return comment, nil
}

func (s *Service) ListComments(ctx context.Context, input ListCommentsInput) (CommentPage, error) {
	noteID, err := parseID("note_id", input.NoteID)
	if err != nil {
		return CommentPage{}, err
	}
	if input.Limit <= 0 {
		input.Limit = defaultCommentLimit
	}
	if input.Limit > maxCommentLimit {
		input.Limit = maxCommentLimit
	}

	cursor, err := decodeCommentCursor(input.Cursor)
	if err != nil {
		return CommentPage{}, err
	}

	if input.Cursor == "" {
		if page, ok := s.getCommentFirstPageCache(ctx, noteID, input.Limit); ok {
			return limitCommentPage(page, input.Limit)
		}

		requestedLimit := input.Limit
		resultChannel := s.commentPageLoads.DoChan(strconv.FormatInt(noteID, 10), func() (any, error) {
			loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheLoadTimeout)
			defer cancel()
			if page, ok := s.getCommentFirstPageCache(loadCtx, noteID, maxCommentLimit); ok {
				return page, nil
			}

			loadInput := input
			loadInput.Limit = maxCommentLimit
			items, hasMore, loadErr := s.repo.ListComments(loadCtx, noteID, loadInput, keysetCursor{})
			if loadErr != nil {
				observability.IncCacheBackendLoad("comments_first_page", "error")
				return CommentPage{}, loadErr
			}
			observability.IncCacheBackendLoad("comments_first_page", "success")
			page, loadErr := buildCommentPage(items, hasMore)
			if loadErr != nil {
				return CommentPage{}, loadErr
			}
			s.setCommentFirstPageCache(loadCtx, noteID, page)
			return page, nil
		})

		select {
		case <-ctx.Done():
			return CommentPage{}, ctx.Err()
		case result := <-resultChannel:
			if result.Shared {
				observability.IncCacheCoalescedRequest("comments_first_page")
			}
			if result.Err != nil {
				return CommentPage{}, result.Err
			}
			page, ok := result.Val.(CommentPage)
			if !ok {
				return CommentPage{}, fmt.Errorf("unexpected shared comment page result %T", result.Val)
			}
			return limitCommentPage(page, requestedLimit)
		}
	}

	items, hasMore, err := s.repo.ListComments(ctx, noteID, input, cursor)
	if err != nil {
		return CommentPage{}, err
	}
	return buildCommentPage(items, hasMore)
}

func buildCommentPage(items []NoteComment, hasMore bool) (CommentPage, error) {
	page := CommentPage{Items: items}
	if !hasMore || len(items) == 0 {
		return page, nil
	}
	nextCursor, err := encodeCommentCursor(items[len(items)-1])
	if err != nil {
		return CommentPage{}, err
	}
	page.NextCursor = nextCursor
	return page, nil
}

func (s *Service) LikeNote(ctx context.Context, input UserActionInput) (IdempotentActionResult, error) {
	noteID, err := parseID("note_id", input.ResourceID)
	if err != nil {
		return IdempotentActionResult{}, err
	}
	if input.UserID <= 0 {
		return IdempotentActionResult{}, ValidationError{Field: "user_id", Message: "must be greater than 0"}
	}
	result, err := s.repo.LikeNote(ctx, noteID, input.UserID)
	if err != nil {
		return IdempotentActionResult{}, err
	}
	s.invalidateNoteCache(ctx, noteID)
	return result, nil
}

func (s *Service) CollectNote(ctx context.Context, input CollectNoteInput) (IdempotentActionResult, error) {
	noteID, err := parseID("note_id", input.NoteID)
	if err != nil {
		return IdempotentActionResult{}, err
	}
	input.CollectionName = strings.TrimSpace(input.CollectionName)
	if input.UserID <= 0 {
		return IdempotentActionResult{}, ValidationError{Field: "user_id", Message: "must be greater than 0"}
	}
	if len(input.CollectionName) > 128 {
		return IdempotentActionResult{}, ValidationError{Field: "collection_name", Message: "must be at most 128 characters"}
	}
	result, err := s.repo.CollectNote(ctx, noteID, input.UserID, input.CollectionName)
	if err != nil {
		return IdempotentActionResult{}, err
	}
	s.invalidateNoteCache(ctx, noteID)
	return result, nil
}

func (s *Service) ShareNote(ctx context.Context, input ShareNoteInput) (ShareNoteResult, error) {
	noteID, err := parseID("note_id", input.NoteID)
	if err != nil {
		return ShareNoteResult{}, err
	}
	input.Channel = normalizeToken(input.Channel)
	if input.UserID <= 0 {
		return ShareNoteResult{}, ValidationError{Field: "user_id", Message: "must be greater than 0"}
	}
	if len(input.Channel) > 64 {
		return ShareNoteResult{}, ValidationError{Field: "channel", Message: "must be at most 64 characters"}
	}
	if input.Channel == "" {
		input.Channel = "unknown"
	}
	result, err := s.repo.ShareNote(ctx, noteID, input.UserID, input.Channel)
	if err != nil {
		return ShareNoteResult{}, err
	}
	s.invalidateNoteCache(ctx, noteID)
	return result, nil
}

func (s *Service) LikeComment(ctx context.Context, input UserActionInput) (IdempotentActionResult, error) {
	commentID, err := parseID("comment_id", input.ResourceID)
	if err != nil {
		return IdempotentActionResult{}, err
	}
	if input.UserID <= 0 {
		return IdempotentActionResult{}, ValidationError{Field: "user_id", Message: "must be greater than 0"}
	}
	result, err := s.repo.LikeComment(ctx, commentID, input.UserID)
	if err != nil {
		return IdempotentActionResult{}, err
	}
	return result, nil
}

func (s *Service) DeleteComment(ctx context.Context, current auth.CurrentUser, rawID string) error {
	commentID, err := parseID("comment_id", rawID)
	if err != nil {
		return err
	}
	if current.ID <= 0 {
		return auth.ErrUnauthorized
	}
	if current.Status != "active" {
		return auth.ErrForbidden
	}

	allowed, err := s.CanModifyComment(ctx, rawID, current)
	if err != nil {
		return err
	}
	if !allowed {
		return auth.ErrForbidden
	}
	noteID, err := s.repo.SoftDeleteComment(ctx, commentID, current.ID)
	if err != nil {
		return err
	}
	if noteID > 0 {
		s.invalidateCommentFirstPage(ctx, noteID)
		s.invalidateNoteCache(ctx, noteID)
	}
	return nil
}

func (s *Service) CanModifyNote(ctx context.Context, rawID string, current auth.CurrentUser) (bool, error) {
	if current.Role == "admin" {
		return true, nil
	}
	noteID, err := parseID("note_id", rawID)
	if err != nil {
		return false, err
	}
	ownerID, err := s.repo.GetNoteOwner(ctx, noteID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, auth.ErrNotFound
		}
		return false, err
	}
	return ownerID == current.ID, nil
}

func (s *Service) CanModifyComment(ctx context.Context, rawID string, current auth.CurrentUser) (bool, error) {
	if current.Role == "admin" {
		return true, nil
	}
	commentID, err := parseID("comment_id", rawID)
	if err != nil {
		return false, err
	}
	ownerID, err := s.repo.GetCommentOwner(ctx, commentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, auth.ErrNotFound
		}
		return false, err
	}
	return ownerID == current.ID, nil
}

func parseID(field string, value string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, ValidationError{Field: field, Message: "must be a positive integer"}
	}
	return id, nil
}

func normalizeToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func noteCacheKey(noteID int64) string {
	return fmt.Sprintf("note:%d", noteID)
}

func commentFirstPageKey(noteID int64) string {
	return fmt.Sprintf("note:%d:comments:first_page:time", noteID)
}

func hotNotesKey(category string) string {
	if category == "" {
		return "ranking:notes:daily"
	}
	return fmt.Sprintf("ranking:notes:%s:daily", category)
}

func hotCommentsKey(noteID int64) string {
	return fmt.Sprintf("note:%d:hot_comments", noteID)
}

func (s *Service) getNoteCache(ctx context.Context, noteID int64) (Note, bool) {
	if s.redis == nil {
		return Note{}, false
	}
	raw, err := s.redis.Get(ctx, noteCacheKey(noteID)).Result()
	if err != nil {
		observability.IncCacheMiss("note_detail")
		return Note{}, false
	}
	var note Note
	if err := json.Unmarshal([]byte(raw), &note); err != nil {
		observability.IncCacheMiss("note_detail")
		return Note{}, false
	}
	observability.IncCacheHit("note_detail")
	return note, true
}

func (s *Service) setNoteCache(ctx context.Context, note Note) {
	if s.redis == nil || note.ID <= 0 {
		return
	}
	payload, err := json.Marshal(note)
	if err != nil {
		return
	}
	_ = s.redis.Set(ctx, noteCacheKey(note.ID), payload, noteDetailCacheTTL).Err()
}

func (s *Service) invalidateNoteCache(ctx context.Context, noteID int64) {
	if s.redis == nil || noteID <= 0 {
		return
	}
	_ = s.redis.Del(ctx, noteCacheKey(noteID)).Err()
}

func (s *Service) getCommentFirstPageCache(ctx context.Context, noteID int64, limit int) (CommentPage, bool) {
	if s.redis == nil {
		return CommentPage{}, false
	}
	raw, err := s.redis.Get(ctx, commentFirstPageKey(noteID)).Result()
	if err != nil {
		observability.IncCacheMiss("comments_first_page")
		return CommentPage{}, false
	}
	var page CommentPage
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		observability.IncCacheMiss("comments_first_page")
		return CommentPage{}, false
	}
	observability.IncCacheHit("comments_first_page")
	return page, true
}

func (s *Service) setCommentFirstPageCache(ctx context.Context, noteID int64, page CommentPage) {
	if s.redis == nil || noteID <= 0 {
		return
	}
	payload, err := json.Marshal(page)
	if err != nil {
		return
	}
	_ = s.redis.Set(ctx, commentFirstPageKey(noteID), payload, commentFirstPageTTL).Err()
}

func (s *Service) invalidateCommentFirstPage(ctx context.Context, noteID int64) {
	if s.redis == nil || noteID <= 0 {
		return
	}
	_ = s.redis.Del(ctx, commentFirstPageKey(noteID)).Err()
}

func (s *Service) refreshNoteRanking(ctx context.Context, noteID int64) error {
	if s.redis == nil || noteID <= 0 {
		return nil
	}
	stats, err := s.repo.GetNoteRankingStats(ctx, noteID)
	if err != nil {
		return err
	}
	if _, err := s.repo.UpdateNoteHotScore(ctx, noteID, stats.HotScore); err != nil {
		return err
	}
	member := strconv.FormatInt(noteID, 10)
	_ = s.removeNoteFromCategoryRankings(ctx, member)
	if err := s.redis.ZAdd(ctx, hotNotesKey(""), redis.Z{Score: stats.HotScore, Member: member}).Err(); err != nil {
		return err
	}
	observability.IncHotRankingUpdate("notes_daily")
	if stats.Category != "" {
		if err := s.redis.ZAdd(ctx, hotNotesKey(stats.Category), redis.Z{Score: stats.HotScore, Member: member}).Err(); err != nil {
			return err
		}
		observability.IncHotRankingUpdate("notes_category_daily")
	}
	return nil
}

func (s *Service) removeNoteRanking(ctx context.Context, noteID int64) error {
	if s.redis == nil || noteID <= 0 {
		return nil
	}
	member := strconv.FormatInt(noteID, 10)
	_ = s.redis.ZRem(ctx, hotNotesKey(""), member).Err()
	_ = s.removeNoteFromCategoryRankings(ctx, member)
	return nil
}

func (s *Service) removeNoteFromCategoryRankings(ctx context.Context, member string) error {
	iter := s.redis.Scan(ctx, 0, "ranking:notes:*:daily", 100).Iterator()
	for iter.Next(ctx) {
		if iter.Val() == hotNotesKey("") {
			continue
		}
		_ = s.redis.ZRem(ctx, iter.Val(), member).Err()
	}
	return iter.Err()
}

func (s *Service) refreshCommentRanking(ctx context.Context, commentID int64) error {
	if s.redis == nil || commentID <= 0 {
		return nil
	}
	info, err := s.repo.GetCommentRankingInfo(ctx, commentID)
	if err != nil {
		return err
	}
	score := float64(info.LikeCount * 5)
	s.invalidateCommentFirstPage(ctx, info.NoteID)
	if err := s.redis.ZAdd(ctx, hotCommentsKey(info.NoteID), redis.Z{Score: score, Member: strconv.FormatInt(commentID, 10)}).Err(); err != nil {
		return err
	}
	observability.IncHotRankingUpdate("hot_comments")
	return nil
}

func limitCommentPage(page CommentPage, limit int) (CommentPage, error) {
	if limit <= 0 || len(page.Items) <= limit {
		return page, nil
	}
	limited := CommentPage{Items: append([]NoteComment(nil), page.Items[:limit]...)}
	nextCursor, err := encodeCommentCursor(limited.Items[len(limited.Items)-1])
	if err != nil {
		return CommentPage{}, err
	}
	limited.NextCursor = nextCursor
	return limited, nil
}
