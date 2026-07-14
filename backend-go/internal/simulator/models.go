package simulator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Persona string

const (
	PersonaLurker        Persona = "lurker"
	PersonaCommenter     Persona = "commenter"
	PersonaCollector     Persona = "collector"
	PersonaFan           Persona = "fan"
	PersonaCritic        Persona = "critic"
	PersonaKnowledgeUser Persona = "knowledge_user"
	PersonaCreator       Persona = "creator"
	PersonaSpammer       Persona = "spammer"
)

type State string

const (
	StateFeedImpression State = "feed_impression"
	StateNoteViewed     State = "note_viewed"
	StateMediaViewed    State = "media_viewed"
	StateCommentsViewed State = "comments_viewed"
	StateNoteLiked      State = "note_liked"
	StateNoteCollected  State = "note_collected"
	StateNoteShared     State = "note_shared"
	StateCommentCreated State = "comment_created"
	StateCommentLiked   State = "comment_liked"
	StateSessionExited  State = "session_exited"
)

type Scenario string

const (
	ScenarioOrganic     Scenario = "organic"
	ScenarioViral       Scenario = "viral"
	ScenarioControversy Scenario = "controversy"
	ScenarioMixed       Scenario = "mixed"
)

type Config struct {
	RunID         string        `json:"run_id"`
	Profile       string        `json:"profile"`
	Scenario      Scenario      `json:"scenario"`
	Seed          int64         `json:"seed"`
	Sessions      int           `json:"sessions"`
	MaxSteps      int           `json:"max_steps"`
	StartAt       time.Time     `json:"start_at"`
	Duration      time.Duration `json:"duration"`
	ZipfExponent  float64       `json:"zipf_exponent"`
	BurstFraction float64       `json:"burst_fraction"`
}

type Preset struct {
	Config    Config
	UserLimit int
	NoteLimit int
}

type NoteRef struct {
	ID         int64
	Category   string
	CommentIDs []int64
}

type Dataset struct {
	UserIDs []int64
	Notes   []NoteRef
}

type UserProfile struct {
	UserID                  int64   `json:"user_id"`
	Persona                 Persona `json:"persona"`
	ActivityLevel           float64 `json:"activity_level"`
	PositiveRatio           float64 `json:"positive_ratio"`
	CommentLengthPreference string  `json:"comment_length_preference"`
	LikeProbability         float64 `json:"like_probability"`
	CollectProbability      float64 `json:"collect_probability"`
	CommentProbability      float64 `json:"comment_probability"`
	ShareProbability        float64 `json:"share_probability"`
}

type Event struct {
	SourceEventID string          `json:"source_event_id"`
	RunID         string          `json:"simulation_run_id"`
	SessionID     string          `json:"session_id"`
	SequenceNo    int             `json:"sequence_no"`
	ProjectID     int64           `json:"project_id"`
	UserID        int64           `json:"user_id"`
	NoteID        int64           `json:"note_id"`
	CommentID     int64           `json:"comment_id,omitempty"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"event_payload"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

type ActivityRank struct {
	ID     int64 `json:"id"`
	Events int64 `json:"events"`
}

type DistributionCheck struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Target string  `json:"target"`
	Passed bool    `json:"passed"`
}

type Report struct {
	RunID                   string              `json:"run_id"`
	Profile                 string              `json:"profile"`
	Scenario                Scenario            `json:"scenario"`
	Seed                    int64               `json:"seed"`
	Users                   int                 `json:"users"`
	Notes                   int                 `json:"notes"`
	Sessions                int                 `json:"sessions"`
	Events                  int64               `json:"events"`
	SimulatedStartAt        time.Time           `json:"simulated_start_at"`
	SimulatedEndAt          time.Time           `json:"simulated_end_at"`
	AverageEventsPerSession float64             `json:"average_events_per_session"`
	P50EventsPerSession     int                 `json:"p50_events_per_session"`
	P95EventsPerSession     int                 `json:"p95_events_per_session"`
	BurstEventRatio         float64             `json:"burst_event_ratio"`
	TopOnePercentNoteShare  float64             `json:"top_one_percent_note_share"`
	TopTenPercentUserShare  float64             `json:"top_ten_percent_user_share"`
	PersonaCounts           map[string]int64    `json:"persona_counts"`
	EventTypeCounts         map[string]int64    `json:"event_type_counts"`
	TransitionCounts        map[string]int64    `json:"transition_counts"`
	CategoryCounts          map[string]int64    `json:"category_counts"`
	TopNotes                []ActivityRank      `json:"top_notes"`
	TopUsers                []ActivityRank      `json:"top_users"`
	Checks                  []DistributionCheck `json:"distribution_checks"`
}

type Sink interface {
	WriteProfiles(ctx context.Context, profiles []UserProfile) error
	WriteEvent(ctx context.Context, event Event) error
	Complete(ctx context.Context, report Report) error
	Abort(ctx context.Context, cause error)
}

func PresetFor(name string, seed int64, scenario Scenario) (Preset, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if scenario == "" {
		scenario = ScenarioMixed
	}
	base := Config{
		Profile:       name,
		Scenario:      scenario,
		Seed:          seed,
		MaxSteps:      12,
		StartAt:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Duration:      7 * 24 * time.Hour,
		ZipfExponent:  1.18,
		BurstFraction: 0.25,
	}
	switch name {
	case "smoke":
		base.Sessions = 500
		base.Duration = 24 * time.Hour
		return Preset{Config: base, UserLimit: 50, NoteLimit: 100}, nil
	case "dev":
		base.Sessions = 20_000
		return Preset{Config: base, UserLimit: 1_000, NoteLimit: 5_000}, nil
	case "scale":
		base.Sessions = 250_000
		base.Duration = 30 * 24 * time.Hour
		return Preset{Config: base, UserLimit: 100_000, NoteLimit: 10_000}, nil
	default:
		return Preset{}, fmt.Errorf("unsupported simulator profile %q", name)
	}
}

func (c *Config) Normalize() {
	c.Profile = strings.ToLower(strings.TrimSpace(c.Profile))
	if c.RunID == "" {
		c.RunID = fmt.Sprintf("phase5a_%s_%s_%d", c.Profile, c.Scenario, c.Seed)
	}
	c.StartAt = c.StartAt.UTC()
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.RunID) == "" {
		return fmt.Errorf("run_id is required")
	}
	if len(c.RunID) > 80 || !validIdentifier(c.RunID) {
		return fmt.Errorf("run_id must be at most 80 characters and contain only letters, numbers, dot, dash, or underscore")
	}
	if c.Scenario != ScenarioOrganic && c.Scenario != ScenarioViral && c.Scenario != ScenarioControversy && c.Scenario != ScenarioMixed {
		return fmt.Errorf("unsupported scenario %q", c.Scenario)
	}
	if c.Sessions <= 0 || c.MaxSteps < 2 {
		return fmt.Errorf("sessions must be positive and max_steps must be at least 2")
	}
	if c.StartAt.IsZero() || c.Duration <= 0 {
		return fmt.Errorf("start_at and duration are required")
	}
	if c.ZipfExponent <= 1 {
		return fmt.Errorf("zipf_exponent must be greater than 1")
	}
	if c.BurstFraction < 0 || c.BurstFraction > 1 {
		return fmt.Errorf("burst_fraction must be between 0 and 1")
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

func (d Dataset) Validate() error {
	if len(d.UserIDs) == 0 {
		return fmt.Errorf("dataset requires at least one user")
	}
	if len(d.Notes) == 0 {
		return fmt.Errorf("dataset requires at least one note")
	}
	for _, note := range d.Notes {
		if note.ID <= 0 || strings.TrimSpace(note.Category) == "" {
			return fmt.Errorf("dataset contains invalid note")
		}
	}
	return nil
}
