package retrieval

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRedactedNoAccessResponseUsesModeThresholdWithoutDependencyCalls(t *testing.T) {
	response := redactedNoAccessResponse(ModeVector, SearchInput{ProjectID: 9, DatasetVersionID: 4}, QueryPlan{Original: "query"}, time.Now())
	if response.Decision.Threshold != MinimumVectorScore {
		t.Fatalf("threshold = %v, want %v", response.Decision.Threshold, MinimumVectorScore)
	}
	if response.ExternalModelCalls != 0 || response.EmbeddingCalls != 0 {
		t.Fatalf("unauthorized response recorded dependency calls: %+v", response)
	}
	if response.Scope.VectorIndexChecksum != "" || response.Scope.IngestionRunID != "" {
		t.Fatalf("unauthorized response leaked retrieval scope: %+v", response.Scope)
	}
}

func TestRetrievalMetricStatus(t *testing.T) {
	for name, testCase := range map[string]struct {
		err      error
		decision string
		expected string
	}{
		"success":    {decision: "candidates", expected: "candidates"},
		"dependency": {err: fmt.Errorf("wrapped: %w", ErrDependencyUnavailable), expected: "dependency_error"},
		"timeout":    {err: context.DeadlineExceeded, expected: "timeout_error"},
		"index":      {err: ErrIndexNotReady, expected: "index_error"},
		"request":    {err: ErrInvalidInput, expected: "request_error"},
		"internal":   {err: fmt.Errorf("database failed"), expected: "internal_error"},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := retrievalMetricStatus(testCase.err, testCase.decision); actual != testCase.expected {
				t.Fatalf("status = %q, want %q", actual, testCase.expected)
			}
		})
	}
}

func TestThresholdForMode(t *testing.T) {
	for mode, expected := range map[string]float64{
		ModeLexical: MinimumConfidence,
		ModeVector:  MinimumVectorScore,
		ModeHybrid:  MinimumHybridScore,
	} {
		if actual := thresholdForMode(mode); actual != expected {
			t.Fatalf("thresholdForMode(%q) = %v, want %v", mode, actual, expected)
		}
	}
}
