package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"creatorinsight/backend-go/internal/note"
	"creatorinsight/backend-go/internal/platform/messaging"
	"creatorinsight/backend-go/internal/platform/observability"

	"github.com/redis/go-redis/v9"
)

type eventRepository interface {
	ApplyEvent(ctx context.Context, consumerName string, input EventApplication) (alreadyProcessed bool, err error)
}

type rankingRepository interface {
	GetNoteRankingStats(ctx context.Context, noteID int64) (note.NoteRankingStats, error)
	GetCommentRankingInfo(ctx context.Context, commentID int64) (note.CommentRankingInfo, error)
}

type EventProcessor struct {
	eventRepo    eventRepository
	rankingRepo  rankingRepository
	redis        *redis.Client
	consumerName string
	logger       *slog.Logger
}

type EventProcessorDeps struct {
	EventRepo    eventRepository
	RankingRepo  rankingRepository
	Redis        *redis.Client
	ConsumerName string
	Logger       *slog.Logger
}

type eventPayload struct {
	ProjectID int64 `json:"project_id"`
	UserID    int64 `json:"user_id"`
	NoteID    int64 `json:"note_id"`
	CommentID int64 `json:"comment_id"`
	ParentID  int64 `json:"parent_id"`
}

func NewEventProcessor(deps EventProcessorDeps) *EventProcessor {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EventProcessor{
		eventRepo:    deps.EventRepo,
		rankingRepo:  deps.RankingRepo,
		redis:        deps.Redis,
		consumerName: deps.ConsumerName,
		logger:       logger,
	}
}

func (p *EventProcessor) Process(ctx context.Context, envelope messaging.EventEnvelope) (bool, error) {
	if envelope.EventID == "" || envelope.EventType == "" {
		return false, fmt.Errorf("event_id and event_type are required")
	}

	var payload eventPayload
	if len(envelope.Payload) > 0 {
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return false, fmt.Errorf("decode event payload: %w", err)
		}
	}
	if payload.UserID <= 0 {
		return false, fmt.Errorf("event %s missing user_id", envelope.EventID)
	}

	alreadyProcessed, err := p.eventRepo.ApplyEvent(ctx, p.consumerName, EventApplication{
		Envelope:  envelope,
		ProjectID: payload.ProjectID,
		UserID:    payload.UserID,
		NoteID:    payload.NoteID,
		CommentID: payload.CommentID,
		ParentID:  payload.ParentID,
		EventType: behaviorEventType(envelope.EventType),
	})
	if err != nil {
		return false, err
	}
	if !alreadyProcessed {
		observability.IncBehaviorRecorded(behaviorEventType(envelope.EventType))
	}

	observability.ObserveDomainEventLag(envelope.EventType, envelope.OccurredAt)
	if err := p.refreshDerivedData(ctx, envelope.EventType, payload); err != nil {
		observability.IncDerivedRefresh(envelope.EventType, "error")
		p.logger.Warn("refresh Redis derived data failed; reconcile will repair it",
			"event_id", envelope.EventID,
			"event_type", envelope.EventType,
			"error", err,
		)
	} else {
		observability.IncDerivedRefresh(envelope.EventType, "success")
	}
	return alreadyProcessed, nil
}

func (p *EventProcessor) refreshDerivedData(ctx context.Context, eventType string, payload eventPayload) error {
	if p.redis == nil {
		return nil
	}
	switch eventType {
	case "note.created", "note.liked", "note.collected", "note.shared":
		if payload.NoteID <= 0 {
			return nil
		}
		if err := p.invalidateNoteCache(ctx, payload.NoteID); err != nil {
			return err
		}
		return p.refreshNoteRanking(ctx, payload.NoteID)
	case "comment.created", "comment.deleted":
		if payload.NoteID <= 0 {
			return nil
		}
		if err := p.invalidateCommentCaches(ctx, payload.NoteID); err != nil {
			return err
		}
		if eventType == "comment.deleted" && payload.CommentID > 0 {
			if err := p.redis.ZRem(ctx, fmt.Sprintf("note:%d:hot_comments", payload.NoteID), strconv.FormatInt(payload.CommentID, 10)).Err(); err != nil {
				return err
			}
		}
		return p.refreshNoteRanking(ctx, payload.NoteID)
	case "comment.liked":
		if payload.NoteID > 0 {
			if err := p.redis.Del(ctx, commentFirstPageCacheKey(payload.NoteID)).Err(); err != nil {
				return err
			}
		}
		if payload.CommentID <= 0 {
			return nil
		}
		return p.refreshCommentRanking(ctx, payload.CommentID)
	default:
		return nil
	}
}

func (p *EventProcessor) invalidateNoteCache(ctx context.Context, noteID int64) error {
	return p.redis.Del(ctx, noteCacheKey(noteID)).Err()
}

func (p *EventProcessor) invalidateCommentCaches(ctx context.Context, noteID int64) error {
	return p.redis.Del(ctx, noteCacheKey(noteID), commentFirstPageCacheKey(noteID)).Err()
}

func (p *EventProcessor) refreshNoteRanking(ctx context.Context, noteID int64) error {
	stats, err := p.rankingRepo.GetNoteRankingStats(ctx, noteID)
	if err != nil {
		return err
	}
	member := strconv.FormatInt(noteID, 10)
	if err := p.redis.ZAdd(ctx, "ranking:notes:daily", redis.Z{Score: stats.HotScore, Member: member}).Err(); err != nil {
		return err
	}
	observability.IncHotRankingUpdate("notes_daily_jetstream")
	if stats.Category != "" {
		if err := p.redis.ZAdd(ctx, fmt.Sprintf("ranking:notes:%s:daily", stats.Category), redis.Z{Score: stats.HotScore, Member: member}).Err(); err != nil {
			return err
		}
		observability.IncHotRankingUpdate("notes_category_daily_jetstream")
	}
	return nil
}

func (p *EventProcessor) refreshCommentRanking(ctx context.Context, commentID int64) error {
	info, err := p.rankingRepo.GetCommentRankingInfo(ctx, commentID)
	if err != nil {
		return err
	}
	score := float64(info.LikeCount * 5)
	if err := p.redis.ZAdd(ctx, fmt.Sprintf("note:%d:hot_comments", info.NoteID), redis.Z{Score: score, Member: strconv.FormatInt(commentID, 10)}).Err(); err != nil {
		return err
	}
	observability.IncHotRankingUpdate("hot_comments_jetstream")
	return nil
}

func noteCacheKey(noteID int64) string {
	return fmt.Sprintf("note:%d", noteID)
}

func commentFirstPageCacheKey(noteID int64) string {
	return fmt.Sprintf("note:%d:comments:first_page:time", noteID)
}

func behaviorEventType(eventType string) string {
	switch eventType {
	case "note.created":
		return "note_created"
	case "comment.created":
		return "comment_created"
	case "comment.deleted":
		return "comment_deleted"
	case "note.liked":
		return "note_liked"
	case "note.collected":
		return "note_collected"
	case "note.shared":
		return "note_shared"
	case "comment.liked":
		return "comment_liked"
	default:
		return eventType
	}
}
