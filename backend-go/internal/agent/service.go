package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/platform/requestmeta"
	"creatorinsight/backend-go/internal/retrieval"

	"github.com/google/uuid"
)

type runRepository interface {
	GetPromptVersion(context.Context, string, string) (PromptVersion, error)
	CreateRun(context.Context, createRecord) (Run, bool, error)
	GetRun(context.Context, string, int64, bool) (Run, error)
	ListRuns(context.Context, int64, bool, int, *runCursor) ([]Run, error)
	CancelRun(context.Context, string, int64, bool) (Run, error)
}

type scopeResolver interface {
	ResolveScope(context.Context, int64, int64, string) (retrieval.Scope, error)
	ResolveAccess(context.Context, retrieval.Scope, retrieval.Principal) (string, error)
}

type Service struct {
	repository runRepository
	scopes     scopeResolver
}

func NewService(repository runRepository, scopes scopeResolver) *Service {
	return &Service{repository: repository, scopes: scopes}
}

func DefaultBudget() Budget {
	return Budget{
		MaxSteps:          8,
		MaxRetrievalCalls: 4,
		MaxModelCalls:     4,
		MaxInputTokens:    32000,
		MaxOutputTokens:   4096,
		MaxDurationMS:     120000,
		MaxCostMicros:     50000,
	}
}

func (s *Service) CreateRun(ctx context.Context, current auth.CurrentUser, input CreateRunInput) (CreateResult, error) {
	if current.ID <= 0 {
		return CreateResult{}, auth.ErrUnauthorized
	}
	if current.Status != "active" {
		return CreateResult{}, auth.ErrForbidden
	}
	input.Query = strings.TrimSpace(input.Query)
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.IngestionRunID = strings.TrimSpace(input.IngestionRunID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.ProjectID == 0 {
		input.ProjectID = 1
	}
	if input.Mode == "" {
		input.Mode = retrieval.ModeLexical
	}
	if input.ProjectID < 1 || input.DatasetVersionID < 1 {
		return CreateResult{}, fmt.Errorf("%w: project_id and dataset_version_id must be positive", ErrInvalidInput)
	}
	if input.Query == "" || utf8.RuneCountInString(input.Query) > MaxQueryRunes {
		return CreateResult{}, fmt.Errorf("%w: query must contain 1-%d characters", ErrInvalidInput, MaxQueryRunes)
	}
	if input.Mode != retrieval.ModeLexical && input.Mode != retrieval.ModeVector && input.Mode != retrieval.ModeHybrid {
		return CreateResult{}, fmt.Errorf("%w: unsupported retrieval mode %q", ErrInvalidInput, input.Mode)
	}
	if len(input.IdempotencyKey) > 128 || strings.ContainsAny(input.IdempotencyKey, "\r\n\x00") {
		return CreateResult{}, fmt.Errorf("%w: invalid idempotency key", ErrInvalidInput)
	}
	input.Budget = applyBudgetDefaults(input.Budget)
	if err := validateBudget(input.Budget); err != nil {
		return CreateResult{}, err
	}

	scope, err := s.scopes.ResolveScope(ctx, input.ProjectID, input.DatasetVersionID, input.IngestionRunID)
	if err != nil {
		if errors.Is(err, retrieval.ErrScopeNotFound) {
			return CreateResult{}, ErrScopeUnavailable
		}
		return CreateResult{}, err
	}
	accessScope, err := s.scopes.ResolveAccess(ctx, scope, retrieval.Principal{UserID: current.ID})
	if err != nil {
		return CreateResult{}, err
	}
	if accessScope == "none" {
		return CreateResult{}, ErrForbidden
	}
	if err := validateScopeForMode(scope, input.Mode); err != nil {
		return CreateResult{}, err
	}
	prompt, err := s.repository.GetPromptVersion(ctx, DefaultPromptKey, DefaultPromptVersion)
	if err != nil {
		return CreateResult{}, err
	}
	contract := RetrievalContract{
		Mode: input.Mode, ProjectID: scope.ProjectID, DatasetID: scope.DatasetID,
		DatasetVersionID: scope.DatasetVersionID, DatasetManifestChecksum: scope.DatasetManifestChecksum,
		IngestionRunID: scope.IngestionRunID, IngestionOutputChecksum: scope.IngestionOutputChecksum,
		ParserVersion: scope.ParserVersion, ChunkerVersion: scope.ChunkerVersion,
		TokenizerVersion: scope.TokenizerVersion, AccessScope: accessScope,
	}
	if input.Mode == retrieval.ModeLexical || input.Mode == retrieval.ModeHybrid {
		contract.LexicalIndexVersion = scope.LexicalIndexVersion
		contract.LexicalIndexChecksum = scope.LexicalIndexChecksum
	}
	if input.Mode == retrieval.ModeVector || input.Mode == retrieval.ModeHybrid {
		contract.VectorIndexVersion = scope.VectorIndexVersion
		contract.VectorIndexChecksum = scope.VectorIndexChecksum
		contract.EmbeddingModel = scope.EmbeddingModel
		contract.EmbeddingRevision = scope.EmbeddingRevision
	}
	requestHash, err := hashRunRequest(input.Query, contract, prompt, input.Budget)
	if err != nil {
		return CreateResult{}, err
	}
	metadata := requestmeta.From(ctx)
	run, replayed, err := s.repository.CreateRun(ctx, createRecord{
		ProjectID: scope.ProjectID, DatasetVersionID: scope.DatasetVersionID,
		IngestionRunID: scope.IngestionRunID, RequestedBy: current.ID,
		Query: input.Query, Mode: input.Mode, Intent: json.RawMessage(`{"status":"pending"}`),
		RetrievalPlan: contract, PromptVersionID: prompt.ID, Budget: input.Budget,
		IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash,
		RequestID: metadata.RequestID, TraceID: metadata.TraceID,
	})
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Run: run, Replayed: replayed}, nil
}

func (s *Service) GetRun(ctx context.Context, current auth.CurrentUser, runID string) (Run, error) {
	if current.ID <= 0 {
		return Run{}, auth.ErrUnauthorized
	}
	if _, err := uuid.Parse(strings.TrimSpace(runID)); err != nil {
		return Run{}, fmt.Errorf("%w: malformed run_id", ErrInvalidInput)
	}
	return s.repository.GetRun(ctx, runID, current.ID, current.Role == "admin")
}

func (s *Service) ListRuns(ctx context.Context, current auth.CurrentUser, input ListRunsInput) (RunPage, error) {
	if current.ID <= 0 {
		return RunPage{}, auth.ErrUnauthorized
	}
	if input.Limit == 0 {
		input.Limit = DefaultListLimit
	}
	if input.Limit < 1 || input.Limit > MaxListLimit {
		return RunPage{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, MaxListLimit)
	}
	cursor, err := decodeRunCursor(input.Cursor)
	if err != nil {
		return RunPage{}, err
	}
	runs, err := s.repository.ListRuns(ctx, current.ID, current.Role == "admin", input.Limit+1, cursor)
	if err != nil {
		return RunPage{}, err
	}
	page := RunPage{Items: runs}
	if len(runs) > input.Limit {
		page.Items = runs[:input.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = encodeRunCursor(runCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return RunPage{}, err
		}
	}
	return page, nil
}

func (s *Service) CancelRun(ctx context.Context, current auth.CurrentUser, runID string) (Run, error) {
	if current.ID <= 0 {
		return Run{}, auth.ErrUnauthorized
	}
	if current.Status != "active" {
		return Run{}, auth.ErrForbidden
	}
	if _, err := uuid.Parse(strings.TrimSpace(runID)); err != nil {
		return Run{}, fmt.Errorf("%w: malformed run_id", ErrInvalidInput)
	}
	return s.repository.CancelRun(ctx, runID, current.ID, current.Role == "admin")
}

func applyBudgetDefaults(budget Budget) Budget {
	defaults := DefaultBudget()
	if budget.MaxSteps == 0 {
		budget.MaxSteps = defaults.MaxSteps
	}
	if budget.MaxRetrievalCalls == 0 {
		budget.MaxRetrievalCalls = defaults.MaxRetrievalCalls
	}
	if budget.MaxModelCalls == 0 {
		budget.MaxModelCalls = defaults.MaxModelCalls
	}
	if budget.MaxInputTokens == 0 {
		budget.MaxInputTokens = defaults.MaxInputTokens
	}
	if budget.MaxOutputTokens == 0 {
		budget.MaxOutputTokens = defaults.MaxOutputTokens
	}
	if budget.MaxDurationMS == 0 {
		budget.MaxDurationMS = defaults.MaxDurationMS
	}
	if budget.MaxCostMicros == 0 {
		budget.MaxCostMicros = defaults.MaxCostMicros
	}
	return budget
}

func validateBudget(budget Budget) error {
	if budget.MaxSteps < 1 || budget.MaxSteps > 32 ||
		budget.MaxRetrievalCalls < 1 || budget.MaxRetrievalCalls > 32 ||
		budget.MaxModelCalls < 1 || budget.MaxModelCalls > 32 ||
		budget.MaxInputTokens < 1 || budget.MaxInputTokens > 1000000 ||
		budget.MaxOutputTokens < 1 || budget.MaxOutputTokens > 1000000 ||
		budget.MaxDurationMS < 1000 || budget.MaxDurationMS > 3600000 ||
		budget.MaxCostMicros < 0 || budget.MaxCostMicros > 1000000000 {
		return fmt.Errorf("%w: agent budget is outside supported bounds", ErrInvalidInput)
	}
	return nil
}

func validateScopeForMode(scope retrieval.Scope, mode string) error {
	if mode == retrieval.ModeLexical || mode == retrieval.ModeHybrid {
		if scope.LexicalIndexVersion != retrieval.LexicalIndexVersion || scope.LexicalIndexChecksum == "" {
			return ErrScopeUnavailable
		}
	}
	if mode == retrieval.ModeVector || mode == retrieval.ModeHybrid {
		if scope.VectorIndexVersion != retrieval.VectorIndexVersion || scope.VectorIndexChecksum == "" ||
			scope.EmbeddingModel == "" || scope.EmbeddingRevision == "" {
			return ErrScopeUnavailable
		}
	}
	return nil
}

func hashRunRequest(query string, contract RetrievalContract, prompt PromptVersion, budget Budget) (string, error) {
	payload, err := json.Marshal(struct {
		Query        string            `json:"query"`
		Contract     RetrievalContract `json:"contract"`
		PromptSHA256 string            `json:"prompt_sha256"`
		Budget       Budget            `json:"budget"`
	}{Query: query, Contract: contract, PromptSHA256: prompt.TemplateSHA256, Budget: budget})
	if err != nil {
		return "", fmt.Errorf("marshal agent request lineage: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func encodeRunCursor(cursor runCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode agent cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeRunCursor(raw string) (*runCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	var cursor runCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
	}
	return &cursor, nil
}
