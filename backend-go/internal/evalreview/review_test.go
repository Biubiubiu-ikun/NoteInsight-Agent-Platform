package evalreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestGenerateDraftsBuildsDeterministicUnapprovedMatrix(t *testing.T) {
	slots, manifest, err := BuildMatrix()
	if err != nil {
		t.Fatal(err)
	}
	corpus := draftFixtureCorpus()
	first, firstReport, err := GenerateDrafts(slots, "codex-draft-author", 2, "phase7a_run", corpus)
	if err != nil {
		t.Fatal(err)
	}
	second, secondReport, err := GenerateDrafts(slots, "codex-draft-author", 2, "phase7a_run", corpus)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstReport, secondReport) {
		t.Fatal("model-assisted draft output is not deterministic")
	}
	if len(first) != 288 || firstReport.Status != "model_draft_awaiting_human_review" || !firstReport.ReviewRequired {
		t.Fatalf("draft report = %+v", firstReport)
	}
	if firstReport.MatrixChecksum != manifest.MatrixChecksum || firstReport.CandidateCountMinimum < 5 || firstReport.CandidateCountMaximum > 6 {
		t.Fatalf("draft candidate report = %+v", firstReport)
	}
	seenAuthorizationTargets := map[string]struct{}{}
	for _, current := range first {
		if current.DraftAssistance != "model_assisted" || current.Metadata["draft_status"] != "awaiting_independent_human_review" {
			t.Fatalf("case %s overstates review status", current.CaseID)
		}
		if current.TaskType == "authorization_boundary" {
			seenAuthorizationTargets[refKey(current.CandidateRefs[0])] = struct{}{}
		}
	}
	if len(seenAuthorizationTargets) < 8 {
		t.Fatalf("authorization candidate pools have too little source variety: %d", len(seenAuthorizationTargets))
	}
}

func TestVerifyDraftArtifactsRejectsAuthoredCaseTampering(t *testing.T) {
	root := t.TempDir()
	slots, _, err := BuildMatrix()
	if err != nil {
		t.Fatal(err)
	}
	cases, report, err := GenerateDrafts(slots, "codex-draft-author", 2, "phase7a_run", draftFixtureCorpus())
	if err != nil {
		t.Fatal(err)
	}
	authoredPath := filepath.Join(root, "authored_cases.jsonl")
	if err := WriteJSONLines(authoredPath, cases); err != nil {
		t.Fatal(err)
	}
	report.AuthoredChecksum, err = FileChecksum(authoredPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(filepath.Join(root, "draft_report.json"), report); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDraftArtifacts(root, 2, "phase7a_run"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(authoredPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authoredPath, append(raw, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDraftArtifacts(root, 2, "phase7a_run"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("VerifyDraftArtifacts() error = %v", err)
	}
}

func TestReviewServerPersistsProgressAndFinalizesImmutably(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "reviewer_a")
	assignments := []Assignment{
		reviewServerAssignment("review-case-1", "reviewer-a", 1),
		reviewServerAssignment("review-case-2", "reviewer-a", 2),
	}
	if err := WriteJSONLines(filepath.Join(directory, "assignments.jsonl"), assignments); err != nil {
		t.Fatal(err)
	}
	server, err := NewReviewServer(workspace, "reviewer_a")
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()

	state := httptest.NewRecorder()
	handler.ServeHTTP(state, httptest.NewRequest(http.MethodGet, "/api/review-state", nil))
	if state.Code != http.StatusOK || bytes.Contains(state.Body.Bytes(), []byte("expected_answer")) {
		t.Fatalf("blind state status/body = %d/%s", state.Code, state.Body.String())
	}

	for index, assignment := range assignments {
		submission := ReviewSubmission{
			CaseID: assignment.CaseID, ReviewerID: assignment.ReviewerID, Answerability: "answerable",
			Judgments: []Judgment{{SourceType: "note", SourceID: int64(index + 1), SourceVersion: 1, RelevanceGrade: 3}},
		}
		raw, _ := json.Marshal(submission)
		request := httptest.NewRequest(http.MethodPut, "/api/reviews/"+assignment.CaseID, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("save case %s status/body = %d/%s", assignment.CaseID, response.Code, response.Body.String())
		}
		if index == 0 {
			finalize := httptest.NewRecorder()
			handler.ServeHTTP(finalize, httptest.NewRequest(http.MethodPost, "/api/finalize", nil))
			if finalize.Code != http.StatusConflict {
				t.Fatalf("incomplete finalize status = %d", finalize.Code)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "submissions.in_progress.jsonl")); err != nil {
		t.Fatal(err)
	}
	finalize := httptest.NewRecorder()
	handler.ServeHTTP(finalize, httptest.NewRequest(http.MethodPost, "/api/finalize", nil))
	if finalize.Code != http.StatusOK {
		t.Fatalf("finalize status/body = %d/%s", finalize.Code, finalize.Body.String())
	}
	final, err := ReadJSONLines[ReviewSubmission](filepath.Join(directory, "submissions.jsonl"))
	if err != nil || len(final) != 2 {
		t.Fatalf("final submissions = %d, error = %v", len(final), err)
	}
	raw, _ := json.Marshal(final[0])
	edit := httptest.NewRecorder()
	handler.ServeHTTP(edit, httptest.NewRequest(http.MethodPut, "/api/reviews/review-case-1", bytes.NewReader(raw)))
	if edit.Code != http.StatusConflict {
		t.Fatalf("post-final edit status = %d", edit.Code)
	}
}

func reviewServerAssignment(caseID string, reviewerID string, sourceID int64) Assignment {
	return Assignment{
		CaseID: caseID, ReviewerID: reviewerID, TaskType: "semantic_paraphrase", Split: "development",
		RubricVersion: RubricVersion, Query: "独立盲审问题 " + caseID, ReviewBlind: true,
		CandidatePool: []CandidateSource{{
			SourceType: "note", SourceID: sourceID, SourceVersion: 1, NoteID: sourceID,
			Visibility: "public", ContentHash: strings.Repeat("a", 64), CanonicalText: "候选证据 " + caseID,
			DatasetVersionID: 2, IngestionRunID: "phase7a_run",
		}},
	}
}

func draftFixtureCorpus() DraftCorpus {
	result := DraftCorpus{Sources: make([]DraftSource, 0, 1240)}
	for index := 1; index <= 600; index++ {
		noteID := int64(index)
		subject := fmt.Sprintf("测试主题%02d", index%20)
		title := fmt.Sprintf("测试记录｜样本记录 %d", index)
		body := fmt.Sprintf("我在固定条件下，围绕测试主题做了%d天记录。\n\n【先说结论】\n结论%d值得保留，但必须保留限制%d。\n\n【具体过程】\n固定变量。\n\n【记录到的变化】\n观察周期%d天，共完成%d次记录，相关预算约%d元。\n\n【争议和限制】\n限制%d。", index%30+1, index, index, index%30+1, index%9+1, index%500+1, index)
		createdAt := time.Date(2026, 1, index%8+1, 8, index%60, 0, 0, time.UTC)
		result.Sources = append(result.Sources, DraftSource{
			CandidateRef: CandidateRef{SourceType: "note", SourceID: noteID, SourceVersion: 1},
			ProjectID:    1, Visibility: "public", Canonical: title + "\n" + body,
			Title: title, Body: body, Category: "test", Tags: []string{"test", subject},
			Topics: []string{"fixed-variable"}, NoteID: noteID, CreatedAt: createdAt,
		})
		result.Sources = append(result.Sources, DraftSource{
			CandidateRef: CandidateRef{SourceType: "note_media", SourceID: 100000 + noteID, SourceVersion: 1},
			ProjectID:    1, Visibility: "public", Canonical: fmt.Sprintf("图注%d\nOCR细节%d", index, index),
			Caption: fmt.Sprintf("图注%d", index), OCRText: fmt.Sprintf("OCR细节%d", index),
			NoteID: noteID, Position: 1, CreatedAt: createdAt,
		})
	}
	for index := 1; index <= 40; index++ {
		result.Sources = append(result.Sources, DraftSource{
			CandidateRef: CandidateRef{SourceType: "note", SourceID: int64(200000 + index), SourceVersion: 1},
			ProjectID:    1, Visibility: "project", Canonical: fmt.Sprintf("ACL-EVAL-R7-%016x\n项目内评测备忘 R7-%016x。", index, index),
			Title: fmt.Sprintf("ACL-EVAL-R7-%016x", index), Body: "项目内评测备忘。", NoteID: int64(200000 + index),
		})
	}
	return result
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
