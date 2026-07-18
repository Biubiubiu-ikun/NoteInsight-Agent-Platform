package evalaudit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"creatorinsight/backend-go/internal/evalbench"
)

const AuditVersion = "benchmark_distinguishability_v1"

type Scenario struct {
	NoteID  int64
	Subject string
	Payload map[string]any
}

type CaseResult struct {
	CaseChecksum             string   `json:"case_checksum"`
	TaskType                 string   `json:"task_type"`
	GoldNoteIDs              []int64  `json:"gold_note_ids"`
	MissingGoldNoteIDs       []int64  `json:"missing_gold_note_ids,omitempty"`
	MaxTopicCohort           int      `json:"max_topic_cohort"`
	QueryMentionsGoldNoteID  bool     `json:"query_mentions_gold_note_id"`
	MatchedScenarioAnchors   int      `json:"matched_scenario_anchors"`
	UniqueMatchedAnchors     int      `json:"unique_matched_anchors"`
	DistinguishabilityStatus string   `json:"distinguishability_status"`
	Reasons                  []string `json:"reasons,omitempty"`
}

type Summary struct {
	CaseCount       int `json:"case_count"`
	PassCount       int `json:"pass_count"`
	ReviewCount     int `json:"review_count"`
	InvalidCount    int `json:"invalid_count"`
	NotApplicable   int `json:"not_applicable_count"`
	TopicCollisions int `json:"topic_collision_count"`
}

type Report struct {
	AuditVersion     string       `json:"audit_version"`
	BenchmarkID      string       `json:"benchmark_id"`
	BenchmarkVersion string       `json:"benchmark_version"`
	ManifestChecksum string       `json:"manifest_checksum"`
	DatasetVersionID int64        `json:"dataset_version_id"`
	SourceRunID      string       `json:"source_run_id"`
	Split            string       `json:"split"`
	Summary          Summary      `json:"summary"`
	Cases            []CaseResult `json:"cases"`
	ReportChecksum   string       `json:"report_checksum"`
}

func Analyze(manifest evalbench.Manifest, cases []evalbench.Case, scenarios []Scenario) (Report, error) {
	if manifest.DatasetVersionID <= 0 || manifest.ManifestChecksum == "" {
		return Report{}, fmt.Errorf("frozen benchmark manifest is missing dataset identity")
	}
	byNote := make(map[int64]Scenario, len(scenarios))
	topicNotes := make(map[string]map[int64]struct{})
	anchorNotes := make(map[string]map[int64]struct{})
	for _, scenario := range scenarios {
		if scenario.NoteID <= 0 {
			continue
		}
		byNote[scenario.NoteID] = scenario
		if subject := normalize(scenario.Subject); subject != "" {
			addNote(topicNotes, subject, scenario.NoteID)
		}
		for anchor := range scenarioAnchors(scenario.Payload) {
			addNote(anchorNotes, anchor, scenario.NoteID)
		}
	}

	report := Report{
		AuditVersion: AuditVersion, BenchmarkID: manifest.BenchmarkID,
		BenchmarkVersion: manifest.BenchmarkVersion, ManifestChecksum: manifest.ManifestChecksum,
		DatasetVersionID: manifest.DatasetVersionID, SourceRunID: manifest.SourceRunID,
		Split: "development", Cases: make([]CaseResult, 0, len(cases)),
	}
	for _, evalCase := range cases {
		result := CaseResult{CaseChecksum: evalCase.CaseChecksum, TaskType: evalCase.TaskType}
		goldTopics := make(map[string]struct{})
		for _, source := range evalCase.GoldSources {
			if source.NoteID <= 0 {
				continue
			}
			result.GoldNoteIDs = appendUniqueInt64(result.GoldNoteIDs, source.NoteID)
			if topic := normalize(source.Topic); topic != "" {
				goldTopics[topic] = struct{}{}
			}
		}
		sort.Slice(result.GoldNoteIDs, func(left, right int) bool { return result.GoldNoteIDs[left] < result.GoldNoteIDs[right] })
		if len(result.GoldNoteIDs) == 0 {
			result.DistinguishabilityStatus = "not_applicable"
			result.Reasons = []string{"case_has_no_gold_source"}
			report.Summary.NotApplicable++
			report.Cases = append(report.Cases, result)
			continue
		}
		matched := make(map[string]struct{})
		uniqueMatched := make(map[string]struct{})
		for _, noteID := range result.GoldNoteIDs {
			scenario, exists := byNote[noteID]
			if !exists {
				result.MissingGoldNoteIDs = append(result.MissingGoldNoteIDs, noteID)
				continue
			}
			if strings.Contains(evalCase.Query, strconv.FormatInt(noteID, 10)) {
				result.QueryMentionsGoldNoteID = true
			}
			for anchor := range scenarioAnchors(scenario.Payload) {
				if strings.Contains(evalCase.Query, anchor) {
					matched[anchor] = struct{}{}
					if len(anchorNotes[anchor]) == 1 {
						uniqueMatched[anchor] = struct{}{}
					}
				}
			}
		}
		for topic := range goldTopics {
			if cohort := len(topicNotes[topic]); cohort > result.MaxTopicCohort {
				result.MaxTopicCohort = cohort
			}
		}
		result.MatchedScenarioAnchors = len(matched)
		result.UniqueMatchedAnchors = len(uniqueMatched)
		switch {
		case len(result.MissingGoldNoteIDs) > 0:
			result.DistinguishabilityStatus = "invalid"
			result.Reasons = append(result.Reasons, "gold_note_missing_from_frozen_dataset")
			report.Summary.InvalidCount++
		case result.QueryMentionsGoldNoteID || result.UniqueMatchedAnchors > 0:
			result.DistinguishabilityStatus = "pass"
			report.Summary.PassCount++
		default:
			result.DistinguishabilityStatus = "review"
			result.Reasons = append(result.Reasons, "no_unique_exact_scenario_anchor")
			report.Summary.ReviewCount++
		}
		if result.MaxTopicCohort > 1 {
			result.Reasons = append(result.Reasons, "gold_topic_has_multiple_notes")
			report.Summary.TopicCollisions++
		}
		report.Cases = append(report.Cases, result)
	}
	report.Summary.CaseCount = len(report.Cases)
	report.ReportChecksum = checksumReport(report)
	return report, nil
}

func scenarioAnchors(payload map[string]any) map[string]struct{} {
	anchors := make(map[string]struct{})
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case string:
			normalized := normalize(typed)
			if len([]rune(normalized)) >= 4 {
				anchors[normalized] = struct{}{}
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(payload)
	return anchors
}

func addNote(index map[string]map[int64]struct{}, key string, noteID int64) {
	if index[key] == nil {
		index[key] = make(map[int64]struct{})
	}
	index[key][noteID] = struct{}{}
}

func appendUniqueInt64(values []int64, value int64) []int64 {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func normalize(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func checksumReport(report Report) string {
	report.ReportChecksum = ""
	raw, _ := json.Marshal(report)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
