package evalreview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildMatrixIsBalancedAndDeterministic(t *testing.T) {
	first, firstManifest, err := BuildMatrix()
	if err != nil {
		t.Fatal(err)
	}
	second, secondManifest, err := BuildMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 288 || firstManifest.SplitCounts["development"] != 144 || firstManifest.SplitCounts["holdout"] != 144 {
		t.Fatalf("matrix manifest = %+v", firstManifest)
	}
	for _, task := range TaskOrder {
		if firstManifest.TaskCounts[task] != 32 {
			t.Fatalf("task %s count = %d", task, firstManifest.TaskCounts[task])
		}
	}
	if firstManifest.MatrixChecksum == "" || firstManifest.MatrixChecksum != secondManifest.MatrixChecksum || !reflect.DeepEqual(first, second) {
		t.Fatal("review matrix is not deterministic")
	}
}

func TestInitializeWorkspaceRefusesOverwrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "retrieval_v5")
	manifest, err := InitializeWorkspace(root)
	if err != nil || manifest.CaseCount != 288 {
		t.Fatalf("InitializeWorkspace() manifest=%+v error=%v", manifest, err)
	}
	if _, err := InitializeWorkspace(root); err == nil {
		t.Fatal("InitializeWorkspace() overwrote an existing review workspace")
	}
	verified, err := VerifyWorkspace(root)
	if err != nil || verified.MatrixChecksum != manifest.MatrixChecksum {
		t.Fatalf("VerifyWorkspace() manifest=%+v error=%v", verified, err)
	}
	matrixPath := filepath.Join(root, "authoring_matrix.jsonl")
	raw, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[20] ^= 1
	if err := os.WriteFile(matrixPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWorkspace(root); err == nil {
		t.Fatal("VerifyWorkspace() accepted a tampered matrix")
	}
}

func TestPrepareResolvesFrozenSourcesAndBlindsExpectedAnswer(t *testing.T) {
	authored, sources, _, _, _ := completeReviewFixture()
	resolver := &fakeResolver{sources: sources}
	prepared, err := Prepare(context.Background(), authored, "reviewer-a", "reviewer-b", 2, "phase7a_run", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.ReviewerA) != 288 || len(prepared.ReviewerB) != 288 || !resolver.called {
		t.Fatalf("prepared assignments = %d/%d", len(prepared.ReviewerA), len(prepared.ReviewerB))
	}
	if prepared.ReviewerA[0].ReviewerID != "reviewer-a" || !prepared.ReviewerA[0].ReviewBlind {
		t.Fatalf("assignment = %+v", prepared.ReviewerA[0])
	}
	if strings.Contains(fmt.Sprintf("%+v", prepared.ReviewerA[0]), authored[0].ExpectedAnswer) {
		t.Fatal("review assignment exposed the author's expected answer")
	}
}

func TestValidateAuthoredMatrixRejectsPunctuationOnlyQueryDuplicate(t *testing.T) {
	authored, _, _, _, _ := completeReviewFixture()
	authored[1].Query = authored[0].Query + "，。"
	if err := ValidateAuthoredMatrix(authored); err == nil || !strings.Contains(err.Error(), "normalized query") {
		t.Fatalf("ValidateAuthoredMatrix() error = %v", err)
	}
}

func TestAuditAndFreezeRequireCompleteIndependentHumanEvidence(t *testing.T) {
	authored, sources, reviewA, reviewB, adjudications := completeReviewFixture()
	result, err := Audit(authored, sources, reviewA, reviewB, adjudications)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Status != "ready_to_freeze" || result.Summary.Agreement.AnswerabilityBinaryKappa != 1 || result.Summary.Agreement.RelevanceQuadraticKappa != 1 {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if len(result.Ledger) != 288 || len(result.AdjudicationQueue) != 0 {
		t.Fatalf("ledger/queue = %d/%d", len(result.Ledger), len(result.AdjudicationQueue))
	}

	root := t.TempDir()
	publicRoot := filepath.Join(root, "public")
	summary, approved, err := FreezeApprovedCases(filepath.Join(root, "private"), publicRoot, authored, sources, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "review_frozen" || len(approved) != 288 {
		t.Fatalf("freeze summary/cases = %+v/%d", summary, len(approved))
	}
	if _, _, err := FreezeApprovedCases(filepath.Join(root, "private"), publicRoot, authored, sources, result, nil); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("second freeze error = %v, want immutable artifact rejection", err)
	}
	for index, current := range authored {
		switch current.TaskType {
		case "no_relevant_document", "out_of_domain_noise":
			if len(approved[index].GoldSources) != 0 {
				t.Fatalf("case %s exposed no-answer gold sources", current.CaseID)
			}
		case "insufficient_evidence":
			if len(approved[index].GoldSources) == 0 || approved[index].Metadata["answerable"] != false {
				t.Fatalf("case %s lost insufficient evidence semantics", current.CaseID)
			}
		default:
			if len(approved[index].GoldSources) == 0 || approved[index].ReviewStatus != "human_approved" {
				t.Fatalf("case %s was not promoted with human gold", current.CaseID)
			}
		}
	}
}

func TestAuditRejectsReviewerIdentityReuse(t *testing.T) {
	authored, sources, reviewA, reviewB, adjudications := completeReviewFixture()
	for index := range reviewB {
		reviewB[index].ReviewerID = "reviewer-a"
	}
	if _, err := Audit(authored, sources, reviewA, reviewB, adjudications); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("Audit() error = %v", err)
	}
}

func TestAuditKeepsIncompleteReviewOutOfFreeze(t *testing.T) {
	authored, sources, reviewA, reviewB, adjudications := completeReviewFixture()
	result, err := Audit(authored, sources, reviewA, reviewB, adjudications[:100])
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Status != "review_in_progress" || len(result.AdjudicationQueue) != 188 || result.Summary.Gates["all_cases_adjudicated"] {
		t.Fatalf("summary/queue = %+v/%d", result.Summary, len(result.AdjudicationQueue))
	}
	if _, _, err := FreezeApprovedCases(t.TempDir(), t.TempDir(), authored, sources, result, nil); err == nil {
		t.Fatal("FreezeApprovedCases() accepted incomplete adjudication")
	}
}

func TestAuditRejectsTaskStratumWithLowAgreement(t *testing.T) {
	authored, sources, reviewA, reviewB, adjudications := completeReviewFixture()
	for index := range reviewB {
		if authored[index].TaskType != "semantic_paraphrase" {
			continue
		}
		reviewB[index].Answerability = "no_relevant_document"
		reviewB[index].Judgments[0].RelevanceGrade = 0
	}
	result, err := Audit(authored, sources, reviewA, reviewB, adjudications)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Status != "review_in_progress" || result.Summary.Gates["per_task_agreement_gates_pass"] {
		t.Fatalf("low-agreement task passed: %+v", result.Summary.TaskAgreement["semantic_paraphrase"])
	}
}

type fakeResolver struct {
	sources []CandidateSource
	called  bool
}

func (r *fakeResolver) ResolveSources(_ context.Context, _ int64, _ string, _ []CandidateRef) ([]CandidateSource, error) {
	r.called = true
	return append([]CandidateSource(nil), r.sources...), nil
}

func completeReviewFixture() ([]AuthoredCase, []CandidateSource, []ReviewSubmission, []ReviewSubmission, []Adjudication) {
	slots, _, _ := BuildMatrix()
	authored := make([]AuthoredCase, 0, len(slots))
	sources := make([]CandidateSource, 0, len(slots))
	reviewA := make([]ReviewSubmission, 0, len(slots))
	reviewB := make([]ReviewSubmission, 0, len(slots))
	adjudications := make([]Adjudication, 0, len(slots))
	timestamp := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	for index, slot := range slots {
		ref := CandidateRef{SourceType: "note", SourceID: int64(index + 1), SourceVersion: 1}
		authored = append(authored, AuthoredCase{
			CaseID: slot.CaseID, AuthorID: "author-1", TaskType: slot.TaskType, Split: slot.Split,
			RubricVersion: RubricVersion, DraftAssistance: "model_assisted",
			Query: fmt.Sprintf("独立审校问题 %03d", index+1), ExpectedAnswer: fmt.Sprintf("独立答案 %03d", index+1),
			CandidateRefs: []CandidateRef{ref}, Metadata: authorizationMetadata(slot.TaskType),
		})
		sources = append(sources, CandidateSource{
			SourceType: "note", SourceID: int64(index + 1), SourceVersion: 1, NoteID: int64(index + 1),
			Visibility: "public", ContentHash: strings.Repeat("a", 64), CanonicalText: fmt.Sprintf("冻结证据 %03d", index+1),
			DatasetVersionID: 2, IngestionRunID: "phase7a_run",
		})
		answerability, grade := expectedDecision(slot.TaskType)
		judgment := Judgment{SourceType: "note", SourceID: int64(index + 1), SourceVersion: 1, RelevanceGrade: grade}
		reviewA = append(reviewA, ReviewSubmission{CaseID: slot.CaseID, ReviewerID: "reviewer-a", Answerability: answerability, Judgments: []Judgment{judgment}, ReviewedAt: timestamp})
		reviewB = append(reviewB, ReviewSubmission{CaseID: slot.CaseID, ReviewerID: "reviewer-b", Answerability: answerability, Judgments: []Judgment{judgment}, ReviewedAt: timestamp.Add(time.Minute)})
		adjudications = append(adjudications, Adjudication{
			CaseID: slot.CaseID, Status: "resolved", AdjudicatorID: "adjudicator-1", Answerability: answerability,
			Judgments: []Judgment{judgment}, Rationale: "根据冻结 rubric 和 canonical evidence 独立裁决。", AdjudicatedAt: timestamp.Add(2 * time.Minute),
		})
	}
	return authored, sources, reviewA, reviewB, adjudications
}

func expectedDecision(task string) (string, int) {
	switch task {
	case "no_relevant_document", "out_of_domain_noise":
		return "no_relevant_document", 0
	case "insufficient_evidence":
		return "insufficient_evidence", 1
	default:
		return "answerable", 3
	}
}

func authorizationMetadata(task string) map[string]any {
	if task != "authorization_boundary" {
		return nil
	}
	return map[string]any{
		"required_project_id": 1, "authorized_expected_results": 1,
		"unauthorized_expected_results": 0, "allowed_user_id": 1, "denied_user_id": 2,
	}
}
