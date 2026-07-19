package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/retrieval"
)

type fakeRunRepository struct {
	prompt       PromptVersion
	createdInput createRecord
	createCalls  int
	createErr    error
}

func (f *fakeRunRepository) GetPromptVersion(context.Context, string, string) (PromptVersion, error) {
	if f.prompt.ID == 0 {
		f.prompt = PromptVersion{ID: 7, PromptKey: DefaultPromptKey, Version: DefaultPromptVersion, TemplateSHA256: "prompt-checksum"}
	}
	return f.prompt, nil
}

func (f *fakeRunRepository) CreateRun(_ context.Context, input createRecord) (Run, bool, error) {
	f.createCalls++
	f.createdInput = input
	if f.createErr != nil {
		return Run{}, false, f.createErr
	}
	return Run{
		ID: "01986f10-5012-7d9c-a6ca-70f43b9ae914", ProjectID: input.ProjectID,
		DatasetVersionID: input.DatasetVersionID, IngestionRunID: input.IngestionRunID,
		RequestedBy: input.RequestedBy, Query: input.Query, RequestedMode: input.Mode,
		RetrievalPlan: input.RetrievalPlan, Budget: input.Budget, Status: StatusQueued,
	}, false, nil
}

func (f *fakeRunRepository) GetRun(context.Context, string, int64, bool) (Run, error) {
	return Run{}, ErrNotFound
}

func (f *fakeRunRepository) ListRuns(context.Context, int64, bool, int, *runCursor) ([]Run, error) {
	return []Run{}, nil
}

func (f *fakeRunRepository) CancelRun(context.Context, string, int64, bool) (Run, error) {
	return Run{}, ErrNotFound
}

type fakeScopeResolver struct {
	scope  retrieval.Scope
	access string
	err    error
}

func (f fakeScopeResolver) ResolveScope(context.Context, int64, int64, string) (retrieval.Scope, error) {
	return f.scope, f.err
}

func (f fakeScopeResolver) ResolveAccess(context.Context, retrieval.Scope, retrieval.Principal) (string, error) {
	return f.access, f.err
}

func TestCreateRunFreezesAuthorizedRetrievalAndBudgetLineage(t *testing.T) {
	repository := &fakeRunRepository{}
	service := NewService(repository, fakeScopeResolver{scope: readyScope(), access: "member"})
	result, err := service.CreateRun(context.Background(), activeUser(), CreateRunInput{
		ProjectID: 9, DatasetVersionID: 12, Query: "  summarize audience risks  ",
		IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.Status != StatusQueued || result.Run.RequestedBy != activeUser().ID {
		t.Fatalf("created run = %+v", result.Run)
	}
	if repository.createdInput.Query != "summarize audience risks" || repository.createdInput.Mode != retrieval.ModeLexical {
		t.Fatalf("normalized create input = %+v", repository.createdInput)
	}
	if repository.createdInput.RetrievalPlan.IngestionOutputChecksum != "ingestion-checksum" ||
		repository.createdInput.RetrievalPlan.AccessScope != "member" ||
		repository.createdInput.RetrievalPlan.VectorIndexChecksum != "" {
		t.Fatalf("retrieval contract = %+v", repository.createdInput.RetrievalPlan)
	}
	if repository.createdInput.Budget != DefaultBudget() || len(repository.createdInput.RequestHash) != 64 {
		t.Fatalf("budget/hash = %+v %q", repository.createdInput.Budget, repository.createdInput.RequestHash)
	}
}

func TestCreateRunRejectsUnauthorizedScopeBeforePersistence(t *testing.T) {
	repository := &fakeRunRepository{}
	service := NewService(repository, fakeScopeResolver{scope: readyScope(), access: "none"})
	_, err := service.CreateRun(context.Background(), activeUser(), CreateRunInput{
		ProjectID: 9, DatasetVersionID: 12, Query: "private evidence",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateRun() error = %v, want forbidden", err)
	}
	if repository.createCalls != 0 {
		t.Fatal("unauthorized request reached persistence")
	}
}

func TestCreateRunRejectsUnavailableVectorIndex(t *testing.T) {
	scope := readyScope()
	scope.VectorIndexChecksum = ""
	repository := &fakeRunRepository{}
	service := NewService(repository, fakeScopeResolver{scope: scope, access: "member"})
	_, err := service.CreateRun(context.Background(), activeUser(), CreateRunInput{
		ProjectID: 9, DatasetVersionID: 12, Query: "semantic query", Mode: retrieval.ModeVector,
	})
	if !errors.Is(err, ErrScopeUnavailable) {
		t.Fatalf("CreateRun() error = %v, want unavailable scope", err)
	}
}

func TestCreateRunRejectsBudgetOutsideHardBounds(t *testing.T) {
	repository := &fakeRunRepository{}
	service := NewService(repository, fakeScopeResolver{scope: readyScope(), access: "member"})
	_, err := service.CreateRun(context.Background(), activeUser(), CreateRunInput{
		ProjectID: 9, DatasetVersionID: 12, Query: "query",
		Budget: Budget{MaxSteps: 33},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateRun() error = %v, want invalid input", err)
	}
}

func TestRunCursorRoundTripAndMalformedInput(t *testing.T) {
	want := runCursor{CreatedAt: time.Date(2026, 7, 19, 8, 30, 0, 123, time.UTC), ID: "01986f10-5012-7d9c-a6ca-70f43b9ae914"}
	raw, err := encodeRunCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRunCursor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
	if _, err := decodeRunCursor("not-base64!"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed cursor error = %v", err)
	}
}

func readyScope() retrieval.Scope {
	return retrieval.Scope{
		ProjectID: 9, DatasetID: 10, DatasetVersionID: 12,
		DatasetManifestChecksum: "dataset-checksum", IngestionRunID: "ingestion-12",
		IngestionOutputChecksum: "ingestion-checksum", ParserVersion: "parser-v1",
		ChunkerVersion: "chunker-v1", TokenizerVersion: "tokenizer-v1",
		LexicalIndexVersion: retrieval.LexicalIndexVersion, LexicalIndexChecksum: "lexical-checksum",
		VectorIndexVersion: retrieval.VectorIndexVersion, VectorIndexChecksum: "vector-checksum",
		EmbeddingModel: "embedding-model", EmbeddingRevision: "embedding-revision",
	}
}

func activeUser() auth.CurrentUser {
	return auth.CurrentUser{ID: 42, Username: "agent-user", Role: "user", Status: "active"}
}
