package retrievaleval

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"creatorinsight/backend-go/internal/evalbench"
	"creatorinsight/backend-go/internal/retrieval"
)

func TestEvaluateCaseSeparatesNoRelevantAndInsufficientEvidence(t *testing.T) {
	noRelevant := evalbench.Case{
		TaskType: "no_relevant_document", CaseChecksum: "none",
		Metadata: map[string]any{"answerable": false},
	}
	noResult := evaluateCase(noRelevant, retrieval.SearchResponse{Results: []retrieval.Result{}}, 0, false, 10)
	if !noResult.Metrics.Rejected || noResult.FailureCategory != "" {
		t.Fatalf("no relevant result = %+v", noResult)
	}

	noteID := int64(9)
	insufficient := evalbench.Case{
		TaskType: "insufficient_evidence", CaseChecksum: "insufficient",
		GoldSources: []evalbench.GoldSource{{SourceType: "note", NoteID: noteID}},
		Metadata:    map[string]any{"answerable": false},
	}
	found := evaluateCase(insufficient, retrieval.SearchResponse{Results: []retrieval.Result{{
		Citations: []retrieval.Citation{{SourceType: "note", NoteID: &noteID}},
	}}}, 0, false, 10)
	if found.Metrics.RecallAtK != 1 || found.Metrics.Rejected {
		t.Fatalf("insufficient evidence source was not retrieved: %+v", found)
	}
}

func TestEvaluateCaseSeparatesCitationIntegrityFromSourcePrecision(t *testing.T) {
	noteID := int64(10)
	quote := "可复核证据"
	validHash := fmt.Sprintf("%x", sha256.Sum256([]byte(quote)))
	evalCase := evalbench.Case{
		TaskType: "semantic_paraphrase", CaseChecksum: "citation",
		GoldSources: []evalbench.GoldSource{{SourceType: "note", NoteID: noteID}},
		Metadata:    map[string]any{"answerable": true},
	}
	response := retrieval.SearchResponse{Results: []retrieval.Result{{Citations: []retrieval.Citation{
		{CitationKey: "citation:1", SourceType: "note", NoteID: &noteID, Quote: quote, QuoteHash: validHash, DocumentStartByte: 0, DocumentEndByte: len([]byte(quote)), SourceStartByte: 10, SourceEndByte: 10 + len([]byte(quote))},
	}}}}
	result := evaluateCase(evalCase, response, 0, false, 10)
	if result.Metrics.CitationPrecision != 1 || result.Metrics.SourcePrecisionAtK != 1 {
		t.Fatalf("valid citation metrics = %+v", result.Metrics)
	}
	response.Results[0].Citations[0].QuoteHash = strings.Repeat("0", 64)
	result = evaluateCase(evalCase, response, 0, false, 10)
	if result.Metrics.CitationPrecision != 0 || result.Metrics.SourcePrecisionAtK != 1 {
		t.Fatalf("invalid citation metrics = %+v", result.Metrics)
	}
}

func TestEvaluateCaseRequiresExactMediaPosition(t *testing.T) {
	noteID := int64(10)
	position := 1
	evalCase := evalbench.Case{
		TaskType: "ocr_detail", CaseChecksum: "ocr",
		GoldSources: []evalbench.GoldSource{{SourceType: "note_media", NoteID: noteID, Position: 2}},
		Metadata:    map[string]any{"answerable": true},
	}
	result := evaluateCase(evalCase, retrieval.SearchResponse{Results: []retrieval.Result{{
		Citations: []retrieval.Citation{{SourceType: "note_media", NoteID: &noteID, MediaPosition: &position}},
	}}}, 0, false, 10)
	if result.Metrics.RecallAtK != 0 || result.FailureCategory != "missed_all_gold" {
		t.Fatalf("wrong OCR position matched gold: %+v", result)
	}
}

func TestHoldoutRequiresExplicitReleaseControls(t *testing.T) {
	config := Config{RunID: "run", BenchmarkDirectory: "bench", Split: "holdout", ProjectID: 1, DatasetVersionID: 2, IngestionRunID: "ingest", TopK: 10}
	if err := config.Validate(); err == nil {
		t.Fatal("unguarded holdout config was accepted")
	}
	config.AllowHoldout = true
	config.ReleaseID = "release-1"
	config.InputFile = "private.jsonl"
	config.AuthorizedUserID = 7
	if err := config.Validate(); err != nil {
		t.Fatalf("guarded holdout config rejected: %v", err)
	}
}
