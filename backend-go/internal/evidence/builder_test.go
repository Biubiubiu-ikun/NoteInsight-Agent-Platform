package evidence

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestBuildDirectDocumentCreatesVerifiableCitations(t *testing.T) {
	run := testRun()
	payload := json.RawMessage(`{"created_at":"2026-07-01T00:00:00Z","updated_at":"2026-07-02T00:00:00Z"}`)
	sources := []SourceInput{{
		EvidenceSourceID: 91,
		ProjectID:        run.ProjectID,
		DatasetID:        run.DatasetID,
		DatasetVersionID: run.DatasetVersionID,
		DatasetVersion:   run.DatasetVersion,
		SourceType:       "note",
		SourceID:         12,
		SourceVersion:    3,
		ContentHash:      hashParts("source"),
		Visibility:       "project",
		CanonicalText:    "标题\r\n\r\n这是可引用的正文。",
		SourcePayload:    payload,
	}}
	documents, err := BuildSourceDocuments(run, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(documents))
	}
	document := documents[0]
	if document.CanonicalText != "标题\n\n这是可引用的正文。" || len(document.Chunks) != 1 {
		t.Fatalf("unexpected canonical document: %+v", document)
	}
	assertDocumentCitations(t, document)
}

func TestBuildCommentClustersIsDeterministicAndKeepsEverySourceCitation(t *testing.T) {
	run := testRun()
	sources := make([]SourceInput, 0, 13)
	for index := 0; index < 13; index++ {
		payload, _ := json.Marshal(map[string]any{
			"note_id": 77,
			"intent":  map[bool]string{true: "question", false: ""}[index%2 == 0],
		})
		sources = append(sources, SourceInput{
			EvidenceSourceID: int64(100 + index),
			ProjectID:        run.ProjectID,
			DatasetID:        run.DatasetID,
			DatasetVersionID: run.DatasetVersionID,
			DatasetVersion:   run.DatasetVersion,
			SourceType:       "note_comment",
			SourceID:         int64(1000 + index),
			SourceVersion:    1,
			ContentHash:      hashParts(fmt.Sprintf("comment-%d", index)),
			Visibility:       "project",
			CanonicalText:    fmt.Sprintf("第%d条评论包含可追踪证据。", index),
			SourcePayload:    payload,
		})
	}
	first, err := BuildSourceDocuments(run, sources)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSourceDocuments(run, append([]SourceInput(nil), sources...))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].DocumentKey != second[0].DocumentKey || first[1].DocumentKey != second[1].DocumentKey {
		t.Fatalf("comment cluster output is not deterministic: %d/%d", len(first), len(second))
	}
	seen := make(map[int64]bool)
	for _, document := range first {
		assertDocumentCitations(t, document)
		for _, source := range document.Sources {
			seen[source.SourceID] = true
		}
	}
	if len(seen) != len(sources) {
		t.Fatalf("cluster citations cover %d sources, want %d", len(seen), len(sources))
	}
}

func TestBuildFactDocumentUsesImmutablePayloadVersion(t *testing.T) {
	run := testRun()
	payload := json.RawMessage(`{
      "note_id":42,"source_run_id":"facts_1","view_count":10,"like_count":3,
      "collect_count":2,"comment_count":1,"share_count":1,"unique_user_count":8,"event_count":17
    }`)
	facts := []FactInput{{
		DailyFactPayloadID: 300,
		ProjectID:          run.ProjectID,
		DatasetID:          run.DatasetID,
		DatasetVersionID:   run.DatasetVersionID,
		DatasetVersion:     run.DatasetVersion,
		FactType:           "note_daily_fact",
		SubjectID:          42,
		FactDate:           time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		SourceVersion:      2,
		ContentHash:        hashParts("fact-payload"),
		SourcePayload:      payload,
		CapturedAt:         time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	}}
	documents, err := BuildFactDocuments(run, facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].SourceVersion != 2 {
		t.Fatalf("unexpected fact documents: %+v", documents)
	}
	assertDocumentCitations(t, documents[0])
}

func assertDocumentCitations(t *testing.T, document DocumentInput) {
	t.Helper()
	sources := make(map[int64]DocumentSource)
	for _, source := range document.Sources {
		sources[source.SourceID] = source
	}
	documentBytes := []byte(document.CanonicalText)
	for _, chunk := range document.Chunks {
		if chunk.Content != string(documentBytes[chunk.StartByte:chunk.EndByte]) {
			t.Fatalf("chunk %d does not slice canonical document", chunk.ChunkIndex)
		}
		for _, citation := range chunk.Citations {
			source, found := sources[citation.SourceID]
			if !found {
				t.Fatalf("citation source %d is not a document source", citation.SourceID)
			}
			documentQuote := string(documentBytes[citation.DocumentStartByte:citation.DocumentEndByte])
			sourceQuote := string([]byte(source.CanonicalText)[citation.SourceStartByte:citation.SourceEndByte])
			if documentQuote != sourceQuote || citation.QuoteHash != hashParts(sourceQuote) {
				t.Fatalf("citation does not round-trip: document=%q source=%q", documentQuote, sourceQuote)
			}
		}
	}
}

func testRun() Run {
	return Run{DatasetVersionID: 2, DatasetID: 5, ProjectID: 7, DatasetVersion: 3}
}
