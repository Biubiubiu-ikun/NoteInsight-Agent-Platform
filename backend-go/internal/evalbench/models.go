package evalbench

import (
	"fmt"
	"strings"
)

const (
	DefaultGeneratorVersion = "independent_eval_v3"
	DefaultProvenance       = "codex_assisted_independent_v3"
)

type Config struct {
	BenchmarkID      string `json:"benchmark_id"`
	BenchmarkVersion string `json:"benchmark_version"`
	SourceRunID      string `json:"source_run_id"`
	GeneratorVersion string `json:"generator_version"`
	Seed             int64  `json:"seed"`
	CaseCount        int    `json:"case_count"`
	DevelopmentCases int    `json:"development_cases"`
}

func (c *Config) Normalize() {
	c.BenchmarkID = strings.TrimSpace(c.BenchmarkID)
	c.BenchmarkVersion = strings.TrimSpace(c.BenchmarkVersion)
	c.SourceRunID = strings.TrimSpace(c.SourceRunID)
	c.GeneratorVersion = strings.TrimSpace(c.GeneratorVersion)
	if c.GeneratorVersion == "" {
		c.GeneratorVersion = DefaultGeneratorVersion
	}
}

func (c Config) Validate() error {
	if !validIdentifier(c.BenchmarkID) || len(c.BenchmarkID) > 128 {
		return fmt.Errorf("benchmark_id must contain only letters, numbers, dot, dash, or underscore")
	}
	if !validIdentifier(c.BenchmarkVersion) || len(c.BenchmarkVersion) > 64 {
		return fmt.Errorf("benchmark_version must contain only letters, numbers, dot, dash, or underscore")
	}
	if !validIdentifier(c.SourceRunID) || len(c.SourceRunID) > 128 {
		return fmt.Errorf("source_run_id must contain only letters, numbers, dot, dash, or underscore")
	}
	if c.GeneratorVersion == "" || len(c.GeneratorVersion) > 64 {
		return fmt.Errorf("generator_version is required and must be at most 64 characters")
	}
	if c.CaseCount < 6 || c.CaseCount > 10000 {
		return fmt.Errorf("case_count must be between 6 and 10000")
	}
	if c.DevelopmentCases < 1 || c.DevelopmentCases >= c.CaseCount {
		return fmt.Errorf("development_cases must be between 1 and case_count-1")
	}
	return nil
}

type Scenario struct {
	Subject        string   `json:"subject"`
	Audience       string   `json:"audience"`
	Context        string   `json:"context"`
	Goal           string   `json:"goal"`
	Concerns       []string `json:"concerns"`
	Steps          []string `json:"steps"`
	KeyMetric      string   `json:"key_metric"`
	Conclusion     string   `json:"conclusion"`
	NotSuitableFor string   `json:"not_suitable_for"`
}

type SourceDocument struct {
	NoteID    int64    `json:"note_id"`
	ProjectID int64    `json:"project_id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Scenario  Scenario `json:"scenario"`
}

type GoldSource struct {
	SourceType string `json:"source_type"`
	NoteID     int64  `json:"note_id"`
	Topic      string `json:"topic,omitempty"`
}

type Case struct {
	Split           string         `json:"split"`
	TaskType        string         `json:"task_type"`
	Query           string         `json:"query"`
	ExpectedAnswer  string         `json:"expected_answer"`
	GoldSources     []GoldSource   `json:"gold_sources"`
	AdversarialTags []string       `json:"adversarial_tags"`
	Provenance      string         `json:"provenance"`
	ReviewStatus    string         `json:"review_status"`
	CaseChecksum    string         `json:"case_checksum"`
	Metadata        map[string]any `json:"metadata"`
}

type Manifest struct {
	BenchmarkID      string         `json:"benchmark_id"`
	BenchmarkVersion string         `json:"benchmark_version"`
	SourceRunID      string         `json:"source_run_id"`
	GeneratorVersion string         `json:"generator_version"`
	Seed             int64          `json:"seed"`
	Status           string         `json:"status"`
	CaseCount        int            `json:"case_count"`
	SplitCounts      map[string]int `json:"split_counts"`
	TaskCounts       map[string]int `json:"task_counts"`
	ReviewCounts     map[string]int `json:"review_counts"`
	ManifestChecksum string         `json:"manifest_checksum"`
	ArtifactScope    string         `json:"artifact_scope,omitempty"`
	CasesFile        string         `json:"cases_file,omitempty"`
	DevelopmentFile  string         `json:"development_file,omitempty"`
	CommitmentsFile  string         `json:"commitments_file,omitempty"`
}

type CaseCommitment struct {
	Ordinal      int    `json:"ordinal"`
	Split        string `json:"split"`
	TaskType     string `json:"task_type"`
	ReviewStatus string `json:"review_status"`
	CaseChecksum string `json:"case_checksum"`
}

type Benchmark struct {
	Config   Config   `json:"config"`
	Cases    []Case   `json:"cases"`
	Manifest Manifest `json:"manifest"`
}

func validIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
