package note

import (
	"encoding/json"
	"time"
)

type Note struct {
	ID              int64           `json:"id"`
	ProjectID       int64           `json:"project_id"`
	AuthorID        int64           `json:"author_id"`
	Title           string          `json:"title"`
	Body            string          `json:"body"`
	Category        string          `json:"category"`
	Topics          json.RawMessage `json:"topics"`
	Tags            json.RawMessage `json:"tags"`
	Location        json.RawMessage `json:"location"`
	ProductEntities json.RawMessage `json:"product_entities"`
	NoteType        string          `json:"note_type"`
	ViewCount       int64           `json:"view_count"`
	LikeCount       int64           `json:"like_count"`
	CollectCount    int64           `json:"collect_count"`
	CommentCount    int64           `json:"comment_count"`
	ShareCount      int64           `json:"share_count"`
	HotScore        float64         `json:"hot_score"`
	QualityScore    float64         `json:"quality_score"`
	Status          string          `json:"status"`
	Visibility      string          `json:"visibility"`
	ContentVersion  int64           `json:"content_version"`
	DeletedAt       *time.Time      `json:"deleted_at,omitempty"`
	ViewerLiked     bool            `json:"viewer_liked"`
	ViewerCollected bool            `json:"viewer_collected"`
	Author          *AuthorSummary  `json:"author,omitempty"`
	Media           []NoteMedia     `json:"media,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AuthorSummary struct {
	ID        int64  `json:"id" db:"id"`
	Username  string `json:"username" db:"username"`
	Nickname  string `json:"nickname" db:"nickname"`
	AvatarURL string `json:"avatar_url" db:"avatar_url"`
}

type NoteMedia struct {
	ID        int64           `json:"id"`
	NoteID    int64           `json:"note_id"`
	MediaType string          `json:"media_type"`
	URL       string          `json:"url"`
	Caption   string          `json:"caption"`
	OCRText   string          `json:"ocr_text"`
	Position  int             `json:"position"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

type NoteComment struct {
	ID         int64     `json:"id"`
	NoteID     int64     `json:"note_id"`
	UserID     int64     `json:"user_id"`
	ParentID   int64     `json:"parent_id"`
	RootID     int64     `json:"root_id"`
	Content    string    `json:"content"`
	LikeCount  int64     `json:"like_count"`
	ReplyCount int64     `json:"reply_count"`
	Sentiment  string    `json:"sentiment,omitempty"`
	Intent     string    `json:"intent,omitempty"`
	TopicID    int64     `json:"topic_id,omitempty"`
	Status     int       `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateNoteInput struct {
	ProjectID       int64
	AuthorID        int64
	Title           string
	Body            string
	Category        string
	Topics          []string
	Tags            []string
	Location        map[string]any
	ProductEntities []string
	Media           []CreateNoteMediaInput
	Visibility      string
}

type UpdateNoteInput struct {
	NoteID      string
	ActorUserID int64
	Title       *string
	Body        *string
	Category    *string
}

type CreateNoteMediaInput struct {
	MediaType string
	URL       string
	Caption   string
	OCRText   string
	Position  int
	Metadata  map[string]any
}

type ListNotesInput struct {
	Category  string
	Query     string
	ProjectID int64
	ViewerID  int64
	Limit     int
	Cursor    string
}

type NotePage struct {
	Items      []Note `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type HotNoteItem struct {
	NoteID int64   `json:"note_id"`
	Score  float64 `json:"score"`
	Note   *Note   `json:"note,omitempty"`
}

type HotNotePage struct {
	Items []HotNoteItem `json:"items"`
}

type CreateCommentInput struct {
	NoteID   string
	UserID   int64
	ParentID int64
	Content  string
	Intent   string
}

type ListCommentsInput struct {
	NoteID   string
	ViewerID int64
	Limit    int
	Cursor   string
}

type CommentPage struct {
	Items      []NoteComment `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type UserActionInput struct {
	ResourceID string
	UserID     int64
}

type CollectNoteInput struct {
	NoteID         string
	UserID         int64
	CollectionName string
}

type ShareNoteInput struct {
	NoteID  string
	UserID  int64
	Channel string
}

type IdempotentActionResult struct {
	ResourceID   int64  `json:"resource_id"`
	UserID       int64  `json:"user_id"`
	Applied      bool   `json:"applied"`
	Count        int64  `json:"count"`
	CountPending bool   `json:"count_pending"`
	Action       string `json:"action"`
}

type ShareNoteResult struct {
	NoteID       int64  `json:"note_id"`
	UserID       int64  `json:"user_id"`
	ShareID      int64  `json:"share_id"`
	ShareCount   int64  `json:"share_count"`
	CountPending bool   `json:"count_pending"`
	Channel      string `json:"channel"`
}
