package evalreview

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	RubricVersion           = "retrieval_relevance_v1"
	MatrixVersion           = "retrieval_v5_matrix_v1"
	DraftGeneratorVersion   = "retrieval_v5_draft_v1"
	TargetCasesPerTask      = 32
	TargetCasesPerTaskSplit = 16
)

var TaskOrder = []string{
	"semantic_paraphrase",
	"typo_robustness",
	"temporal_conflict",
	"cross_note_compare",
	"no_relevant_document",
	"insufficient_evidence",
	"ocr_detail",
	"authorization_boundary",
	"out_of_domain_noise",
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type MatrixSlot struct {
	CaseID        string `json:"case_id"`
	TaskType      string `json:"task_type"`
	Split         string `json:"split"`
	RubricVersion string `json:"rubric_version"`
	Status        string `json:"status"`
}

type MatrixManifest struct {
	MatrixVersion  string         `json:"matrix_version"`
	RubricVersion  string         `json:"rubric_version"`
	Status         string         `json:"status"`
	CaseCount      int            `json:"case_count"`
	SplitCounts    map[string]int `json:"split_counts"`
	TaskCounts     map[string]int `json:"task_counts"`
	MatrixChecksum string         `json:"matrix_checksum"`
}

type CandidateRef struct {
	SourceType    string `json:"source_type"`
	SourceID      int64  `json:"source_id"`
	SourceVersion int64  `json:"source_version"`
}

type CandidateSource struct {
	SourceType       string `json:"source_type"`
	SourceID         int64  `json:"source_id"`
	SourceVersion    int64  `json:"source_version"`
	NoteID           int64  `json:"note_id"`
	Position         int    `json:"position,omitempty"`
	Visibility       string `json:"visibility"`
	ContentHash      string `json:"content_hash"`
	CanonicalText    string `json:"canonical_text"`
	DatasetVersionID int64  `json:"dataset_version_id"`
	IngestionRunID   string `json:"ingestion_run_id"`
}

func (s CandidateSource) Ref() CandidateRef {
	return CandidateRef{SourceType: s.SourceType, SourceID: s.SourceID, SourceVersion: s.SourceVersion}
}

type AuthoredCase struct {
	CaseID          string         `json:"case_id"`
	AuthorID        string         `json:"author_id"`
	TaskType        string         `json:"task_type"`
	Split           string         `json:"split"`
	RubricVersion   string         `json:"rubric_version"`
	DraftAssistance string         `json:"draft_assistance"`
	Query           string         `json:"query"`
	ExpectedAnswer  string         `json:"expected_answer"`
	CandidateRefs   []CandidateRef `json:"candidate_refs"`
	AdversarialTags []string       `json:"adversarial_tags,omitempty"`
	CommitmentNonce string         `json:"commitment_nonce,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type DraftReport struct {
	GeneratorVersion      string         `json:"generator_version"`
	Status                string         `json:"status"`
	DatasetVersionID      int64          `json:"dataset_version_id"`
	IngestionRunID        string         `json:"ingestion_run_id"`
	AuthorID              string         `json:"author_id"`
	CaseCount             int            `json:"case_count"`
	SplitCounts           map[string]int `json:"split_counts"`
	TaskCounts            map[string]int `json:"task_counts"`
	CandidateRefCount     int            `json:"candidate_ref_count"`
	UniqueCandidateCount  int            `json:"unique_candidate_count"`
	CandidateCountMinimum int            `json:"candidate_count_minimum"`
	CandidateCountMaximum int            `json:"candidate_count_maximum"`
	AuthoredChecksum      string         `json:"authored_cases_checksum"`
	MatrixChecksum        string         `json:"matrix_checksum"`
	SourcePolicy          string         `json:"source_policy"`
	ReviewRequired        bool           `json:"review_required"`
	KnownReviewRisks      []string       `json:"known_review_risks"`
}

type Assignment struct {
	CaseID        string            `json:"case_id"`
	ReviewerID    string            `json:"reviewer_id"`
	TaskType      string            `json:"task_type"`
	Split         string            `json:"split"`
	RubricVersion string            `json:"rubric_version"`
	Query         string            `json:"query"`
	ReviewContext map[string]any    `json:"review_context,omitempty"`
	CandidatePool []CandidateSource `json:"candidate_pool"`
	ReviewBlind   bool              `json:"review_blind"`
}

type Judgment struct {
	SourceType     string `json:"source_type"`
	SourceID       int64  `json:"source_id"`
	SourceVersion  int64  `json:"source_version"`
	RelevanceGrade int    `json:"relevance_grade"`
}

func (j Judgment) Ref() CandidateRef {
	return CandidateRef{SourceType: j.SourceType, SourceID: j.SourceID, SourceVersion: j.SourceVersion}
}

type ReviewSubmission struct {
	CaseID        string     `json:"case_id"`
	ReviewerID    string     `json:"reviewer_id"`
	Answerability string     `json:"answerability"`
	Judgments     []Judgment `json:"judgments"`
	ReviewedAt    time.Time  `json:"reviewed_at"`
}

type Adjudication struct {
	CaseID        string     `json:"case_id"`
	Status        string     `json:"status"`
	AdjudicatorID string     `json:"adjudicator_id"`
	Answerability string     `json:"answerability"`
	Judgments     []Judgment `json:"judgments"`
	Rationale     string     `json:"rationale"`
	AdjudicatedAt time.Time  `json:"adjudicated_at"`
}

type ReviewLedgerRecord struct {
	CaseID          string             `json:"case_id"`
	AuthorID        string             `json:"author_id"`
	TaskType        string             `json:"task_type"`
	Split           string             `json:"split"`
	RubricVersion   string             `json:"rubric_version"`
	DraftAssistance string             `json:"draft_assistance"`
	Reviews         []ReviewSubmission `json:"reviews"`
	Adjudication    Adjudication       `json:"adjudication"`
}

type Agreement struct {
	CaseCount                   int     `json:"case_count"`
	JudgmentCount               int     `json:"judgment_count"`
	AnswerabilityExactAgreement float64 `json:"answerability_exact_agreement"`
	AnswerabilityBinaryKappa    float64 `json:"answerability_binary_cohen_kappa"`
	RelevanceExactAgreement     float64 `json:"relevance_exact_agreement"`
	RelevanceQuadraticKappa     float64 `json:"relevance_quadratic_weighted_kappa"`
}

type ReviewSummary struct {
	BenchmarkVersion      string               `json:"benchmark_version"`
	RubricVersion         string               `json:"rubric_version"`
	Status                string               `json:"status"`
	CaseCount             int                  `json:"case_count"`
	SplitCounts           map[string]int       `json:"split_counts"`
	TaskCounts            map[string]int       `json:"task_counts"`
	ReviewedCaseCount     int                  `json:"reviewed_case_count"`
	AdjudicatedCaseCount  int                  `json:"adjudicated_case_count"`
	DisagreementCaseCount int                  `json:"disagreement_case_count"`
	Agreement             Agreement            `json:"agreement"`
	TaskAgreement         map[string]Agreement `json:"task_agreement"`
	Gates                 map[string]bool      `json:"gates"`
	ArtifactChecksums     map[string]string    `json:"artifact_checksums,omitempty"`
	DecisionChecksum      string               `json:"decision_checksum"`
	SummaryChecksum       string               `json:"summary_checksum"`
}

type AuditResult struct {
	Summary           ReviewSummary
	Ledger            []ReviewLedgerRecord
	AdjudicationQueue []MatrixSlot
}

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(strings.TrimSpace(value))
}

func validRoleID(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) <= 64 && validIdentifier(value)
}

func validSplit(value string) bool {
	return value == "development" || value == "holdout"
}

func validTask(value string) bool {
	for _, task := range TaskOrder {
		if value == task {
			return true
		}
	}
	return false
}

func validAnswerability(value string) bool {
	switch value {
	case "answerable", "insufficient_evidence", "no_relevant_document", "authorization_denied":
		return true
	default:
		return false
	}
}

func refKey(ref CandidateRef) string {
	return fmt.Sprintf("%s:%d:%d", ref.SourceType, ref.SourceID, ref.SourceVersion)
}
