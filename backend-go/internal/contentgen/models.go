package contentgen

import (
	"fmt"
	"strings"
	"time"
)

var categoryOrder = []string{
	"beauty",
	"fashion",
	"food",
	"travel",
	"home",
	"fitness",
	"career",
	"digital",
	"study",
	"local_life",
}

type Config struct {
	RunID            string    `json:"run_id"`
	Profile          string    `json:"profile"`
	Seed             int64     `json:"seed"`
	ProjectID        int64     `json:"project_id"`
	Notes            int       `json:"notes"`
	CommentsPerNote  int       `json:"comments_per_note"`
	MediaPerNote     int       `json:"media_per_note"`
	EvalCasesPerNote int       `json:"eval_cases_per_note"`
	NoteIDStart      int64     `json:"note_id_start"`
	CommentIDStart   int64     `json:"comment_id_start"`
	StartAt          time.Time `json:"start_at"`
}

type Document struct {
	ID              int64            `json:"id"`
	ProjectID       int64            `json:"project_id"`
	AuthorID        int64            `json:"author_id"`
	Title           string           `json:"title"`
	Body            string           `json:"body"`
	Category        string           `json:"category"`
	Topics          []string         `json:"topics"`
	Tags            []string         `json:"tags"`
	Location        map[string]any   `json:"location"`
	ProductEntities []map[string]any `json:"product_entities"`
	QualityScore    float64          `json:"quality_score"`
	CreatedAt       time.Time        `json:"created_at"`
	Media           []Media          `json:"media"`
	Scenario        Scenario         `json:"scenario"`
}

type Media struct {
	Position int            `json:"position"`
	Caption  string         `json:"caption"`
	OCRText  string         `json:"ocr_text"`
	Metadata map[string]any `json:"metadata"`
}

type Comment struct {
	ID        int64     `json:"id"`
	NoteID    int64     `json:"note_id"`
	UserID    int64     `json:"user_id"`
	Content   string    `json:"content"`
	Sentiment string    `json:"sentiment"`
	Intent    string    `json:"intent"`
	TopicID   int64     `json:"topic_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Scenario struct {
	Subject          string   `json:"subject"`
	Audience         string   `json:"audience"`
	Context          string   `json:"context"`
	Goal             string   `json:"goal"`
	MainTopics       []string `json:"main_topics"`
	PositiveFeedback []string `json:"positive_feedback"`
	Concerns         []string `json:"concerns"`
	Steps            []string `json:"steps"`
	KeyMetric        string   `json:"key_metric"`
	Conclusion       string   `json:"conclusion"`
	NotSuitableFor   string   `json:"not_suitable_for"`
}

type GoldSource struct {
	SourceType string `json:"source_type"`
	Topic      string `json:"topic,omitempty"`
	Position   int    `json:"position,omitempty"`
}

type EvalCase struct {
	NoteID         int64          `json:"note_id"`
	TaskType       string         `json:"task_type"`
	Question       string         `json:"question"`
	ExpectedAnswer string         `json:"expected_answer"`
	GoldSources    []GoldSource   `json:"gold_sources"`
	Metadata       map[string]any `json:"metadata"`
}

type Item struct {
	Document  Document   `json:"document"`
	Comments  []Comment  `json:"comments"`
	EvalCases []EvalCase `json:"eval_cases"`
}

type Corpus struct {
	Config Config `json:"config"`
	Items  []Item `json:"items"`
}

type QualityCheck struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Target string  `json:"target"`
	Passed bool    `json:"passed"`
}

type Report struct {
	RunID                    string           `json:"run_id"`
	Profile                  string           `json:"profile"`
	Seed                     int64            `json:"seed"`
	Notes                    int              `json:"notes"`
	Media                    int              `json:"media"`
	Comments                 int              `json:"comments"`
	EvalCases                int              `json:"eval_cases"`
	UniqueTitleRatio         float64          `json:"unique_title_ratio"`
	DuplicateCommentRatio    float64          `json:"duplicate_comment_ratio"`
	SemanticAlignmentRatio   float64          `json:"semantic_alignment_ratio"`
	MinimumBodyCharacters    int              `json:"minimum_body_characters"`
	AverageBodyCharacters    float64          `json:"average_body_characters"`
	MinimumOCRCharacters     int              `json:"minimum_ocr_characters"`
	MinimumCommentCharacters int              `json:"minimum_comment_characters"`
	CategoryCounts           map[string]int64 `json:"category_counts"`
	IntentCounts             map[string]int64 `json:"intent_counts"`
	SentimentCounts          map[string]int64 `json:"sentiment_counts"`
	EvalTaskCounts           map[string]int64 `json:"eval_task_counts"`
	Checks                   []QualityCheck   `json:"quality_checks"`
}

func Categories() []string {
	return append([]string(nil), categoryOrder...)
}

func PresetFor(profile string, seed int64) (Config, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	base := Config{
		Profile:          profile,
		Seed:             seed,
		MediaPerNote:     4,
		EvalCasesPerNote: 5,
		StartAt:          time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC),
	}
	switch profile {
	case "smoke":
		base.Notes = 20
		base.CommentsPerNote = 30
		base.MediaPerNote = 3
	case "quality":
		base.Notes = 200
		base.CommentsPerNote = 200
	default:
		return Config{}, fmt.Errorf("unsupported corpus profile %q", profile)
	}
	base.Normalize()
	return base, nil
}

func (c *Config) Normalize() {
	c.Profile = strings.ToLower(strings.TrimSpace(c.Profile))
	if c.RunID == "" {
		c.RunID = fmt.Sprintf("phase5b_%s_%d", c.Profile, c.Seed)
	}
	c.StartAt = c.StartAt.UTC()
}

func (c Config) Validate() error {
	if len(c.RunID) == 0 || len(c.RunID) > 80 || !validIdentifier(c.RunID) {
		return fmt.Errorf("run_id must be at most 80 characters and contain only letters, numbers, dot, dash, or underscore")
	}
	if c.Notes <= 0 || c.CommentsPerNote <= 0 {
		return fmt.Errorf("notes and comments_per_note must be positive")
	}
	if c.MediaPerNote < 1 || c.MediaPerNote > 9 {
		return fmt.Errorf("media_per_note must be between 1 and 9")
	}
	if c.EvalCasesPerNote < 1 || c.EvalCasesPerNote > 5 {
		return fmt.Errorf("eval_cases_per_note must be between 1 and 5")
	}
	if c.NoteIDStart <= 0 || c.CommentIDStart <= 0 {
		return fmt.Errorf("ID starts must be positive")
	}
	if c.StartAt.IsZero() {
		return fmt.Errorf("start_at is required")
	}
	return nil
}

func validIdentifier(value string) bool {
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return value != ""
}
