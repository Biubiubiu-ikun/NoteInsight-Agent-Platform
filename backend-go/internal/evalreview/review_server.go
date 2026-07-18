package evalreview

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed review_ui.html
var reviewUI embed.FS

type reviewStateResponse struct {
	ReviewerID  string                      `json:"reviewer_id"`
	Assignments []Assignment                `json:"assignments"`
	Submissions map[string]ReviewSubmission `json:"submissions"`
	Finalized   bool                        `json:"finalized"`
}

type ReviewServer struct {
	mu          sync.Mutex
	assignments []Assignment
	byCase      map[string]Assignment
	submissions map[string]ReviewSubmission
	reviewerID  string
	progress    string
	final       string
	finalized   bool
	ui          []byte
}

func NewReviewServer(workspace string, reviewerSlot string) (*ReviewServer, error) {
	if reviewerSlot != "reviewer_a" && reviewerSlot != "reviewer_b" {
		return nil, fmt.Errorf("reviewer slot must be reviewer_a or reviewer_b")
	}
	directory := filepath.Join(workspace, reviewerSlot)
	assignments, err := ReadJSONLines[Assignment](filepath.Join(directory, "assignments.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		return nil, fmt.Errorf("review assignment is empty")
	}
	reviewerID := assignments[0].ReviewerID
	if !validRoleID(reviewerID) {
		return nil, fmt.Errorf("review assignment has an invalid reviewer identity")
	}
	byCase := make(map[string]Assignment, len(assignments))
	for _, assignment := range assignments {
		if assignment.ReviewerID != reviewerID || !assignment.ReviewBlind {
			return nil, fmt.Errorf("review assignment %s violates reviewer identity or blind-review controls", assignment.CaseID)
		}
		if _, duplicate := byCase[assignment.CaseID]; duplicate {
			return nil, fmt.Errorf("duplicate review assignment %s", assignment.CaseID)
		}
		byCase[assignment.CaseID] = assignment
	}
	ui, err := reviewUI.ReadFile("review_ui.html")
	if err != nil {
		return nil, fmt.Errorf("load review UI: %w", err)
	}
	server := &ReviewServer{
		assignments: assignments,
		byCase:      byCase,
		submissions: map[string]ReviewSubmission{},
		reviewerID:  reviewerID,
		progress:    filepath.Join(directory, "submissions.in_progress.jsonl"),
		final:       filepath.Join(directory, "submissions.jsonl"),
		ui:          ui,
	}
	if _, err := os.Stat(server.final); err == nil {
		server.finalized = true
		if err := server.loadSubmissions(server.final); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if _, err := os.Stat(server.progress); err == nil {
		if err := server.loadSubmissions(server.progress); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return server, nil
}

func (s *ReviewServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /api/review-state", s.handleState)
	mux.HandleFunc("PUT /api/reviews/{case_id}", s.handleReview)
	mux.HandleFunc("POST /api/finalize", s.handleFinalize)
	return securityHeaders(mux)
}

func (s *ReviewServer) handleIndex(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write(s.ui)
}

func (s *ReviewServer) handleState(response http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := make(map[string]ReviewSubmission, len(s.submissions))
	for caseID, submission := range s.submissions {
		snapshot[caseID] = submission
	}
	writeReviewJSON(response, http.StatusOK, reviewStateResponse{
		ReviewerID: s.reviewerID, Assignments: s.assignments,
		Submissions: snapshot, Finalized: s.finalized,
	})
}

func (s *ReviewServer) handleReview(response http.ResponseWriter, request *http.Request) {
	caseID := strings.TrimSpace(request.PathValue("case_id"))
	assignment, ok := s.byCase[caseID]
	if !ok {
		writeReviewError(response, http.StatusNotFound, "unknown review case")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	var input ReviewSubmission
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeReviewError(response, http.StatusBadRequest, "invalid review submission: "+err.Error())
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeReviewError(response, http.StatusBadRequest, err.Error())
		return
	}
	input.CaseID = caseID
	input.ReviewerID = s.reviewerID
	input.ReviewedAt = time.Now().UTC()
	if err := validateReviewSubmission(assignment, input); err != nil {
		writeReviewError(response, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		writeReviewError(response, http.StatusConflict, "review is finalized and immutable")
		return
	}
	previous, existed := s.submissions[caseID]
	s.submissions[caseID] = input
	if err := s.writeProgress(); err != nil {
		if existed {
			s.submissions[caseID] = previous
		} else {
			delete(s.submissions, caseID)
		}
		writeReviewError(response, http.StatusInternalServerError, err.Error())
		return
	}
	writeReviewJSON(response, http.StatusOK, input)
}

func (s *ReviewServer) handleFinalize(response http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		writeReviewError(response, http.StatusConflict, "review is already finalized")
		return
	}
	if len(s.submissions) != len(s.assignments) {
		writeReviewError(response, http.StatusConflict, fmt.Sprintf("reviewed %d of %d cases", len(s.submissions), len(s.assignments)))
		return
	}
	ordered, err := s.orderedSubmissions()
	if err != nil {
		writeReviewError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := os.Stat(s.final); err == nil {
		writeReviewError(response, http.StatusConflict, "final review artifact already exists")
		return
	} else if !os.IsNotExist(err) {
		writeReviewError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if err := WriteJSONLines(s.final, ordered); err != nil {
		writeReviewError(response, http.StatusInternalServerError, err.Error())
		return
	}
	s.finalized = true
	writeReviewJSON(response, http.StatusOK, map[string]any{"status": "finalized", "reviewed_cases": len(ordered)})
}

func (s *ReviewServer) loadSubmissions(path string) error {
	submissions, err := ReadJSONLines[ReviewSubmission](path)
	if err != nil {
		return err
	}
	for _, submission := range submissions {
		assignment, ok := s.byCase[submission.CaseID]
		if !ok {
			return fmt.Errorf("submission %s has no assignment", submission.CaseID)
		}
		if submission.ReviewerID != s.reviewerID {
			return fmt.Errorf("submission %s has reviewer %q; expected %q", submission.CaseID, submission.ReviewerID, s.reviewerID)
		}
		if err := validateReviewSubmission(assignment, submission); err != nil {
			return err
		}
		if _, duplicate := s.submissions[submission.CaseID]; duplicate {
			return fmt.Errorf("duplicate submission %s", submission.CaseID)
		}
		s.submissions[submission.CaseID] = submission
	}
	return nil
}

func (s *ReviewServer) writeProgress() error {
	ordered, err := s.orderedSubmissions()
	if err != nil {
		return err
	}
	return WriteJSONLines(s.progress, ordered)
}

func (s *ReviewServer) orderedSubmissions() ([]ReviewSubmission, error) {
	ordered := make([]ReviewSubmission, 0, len(s.submissions))
	for _, assignment := range s.assignments {
		if submission, ok := s.submissions[assignment.CaseID]; ok {
			ordered = append(ordered, submission)
		}
	}
	if len(ordered) != len(s.submissions) {
		return nil, fmt.Errorf("submission set contains an unknown case")
	}
	return ordered, nil
}

func validateReviewSubmission(assignment Assignment, submission ReviewSubmission) error {
	if submission.CaseID != assignment.CaseID || submission.ReviewerID != assignment.ReviewerID {
		return fmt.Errorf("review identity does not match assignment %s", assignment.CaseID)
	}
	if !validAnswerability(submission.Answerability) {
		return fmt.Errorf("case %s requires a valid answerability label", assignment.CaseID)
	}
	if len(submission.Judgments) != len(assignment.CandidatePool) {
		return fmt.Errorf("case %s must grade all %d candidate sources", assignment.CaseID, len(assignment.CandidatePool))
	}
	expected := make(map[string]struct{}, len(assignment.CandidatePool))
	for _, source := range assignment.CandidatePool {
		expected[refKey(source.Ref())] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, judgment := range submission.Judgments {
		key := refKey(judgment.Ref())
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("case %s judgment references a source outside the assignment", assignment.CaseID)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("case %s duplicates a source judgment", assignment.CaseID)
		}
		if judgment.RelevanceGrade < 0 || judgment.RelevanceGrade > 3 {
			return fmt.Errorf("case %s relevance grades must be between 0 and 3", assignment.CaseID)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return fmt.Errorf("request must contain exactly one JSON object")
}

func writeReviewJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeReviewError(response http.ResponseWriter, status int, message string) {
	writeReviewJSON(response, status, map[string]string{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}
