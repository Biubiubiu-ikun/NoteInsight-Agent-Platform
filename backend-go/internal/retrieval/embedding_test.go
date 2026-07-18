package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTEIEmbedderSeparatesQueryInstructionFromDocuments(t *testing.T) {
	var requests [][]string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			Inputs []string `json:"inputs"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, input.Inputs)
		vectors := make([][]float32, len(input.Inputs))
		for index := range vectors {
			vectors[index] = []float32{0.1, 0.2, 0.3}
		}
		_ = json.NewEncoder(writer).Encode(vectors)
	}))
	defer server.Close()
	embedder := NewTEIEmbedder(server.URL, "model", "revision", 3, time.Second)
	if _, err := embedder.EmbedDocuments(context.Background(), []string{"原始证据正文"}); err != nil {
		t.Fatal(err)
	}
	if _, err := embedder.EmbedQuery(context.Background(), "哪条证据支持结论"); err != nil {
		t.Fatal(err)
	}
	if requests[0][0] != "原始证据正文" {
		t.Fatalf("document embedding input was changed: %q", requests[0][0])
	}
	if !strings.HasPrefix(requests[1][0], "Instruct: ") || !strings.Contains(requests[1][0], "\nQuery: 哪条证据支持结论") {
		t.Fatalf("query instruction missing: %q", requests[1][0])
	}
}

func TestTEIEmbedderRejectsWrongDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode([][]float32{{0.1, 0.2}})
	}))
	defer server.Close()
	embedder := NewTEIEmbedder(server.URL, "model", "revision", 3, time.Second)
	if _, err := embedder.EmbedDocuments(context.Background(), []string{"evidence"}); err == nil || !strings.Contains(err.Error(), "dimension 2, want 3") {
		t.Fatalf("wrong-dimension response error = %v", err)
	}
}

func TestTEIEmbedderRetriesTransientOverload(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < 3 {
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(`{"error":"Model is overloaded"}`))
			return
		}
		_ = json.NewEncoder(writer).Encode([][]float32{{0.1, 0.2, 0.3}})
	}))
	defer server.Close()
	embedder := NewTEIEmbedder(server.URL, "model", "revision", 3, 2*time.Second)
	if _, err := embedder.EmbedQuery(context.Background(), "query"); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestTEIEmbedderDoesNotRetryClientError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts++
		writer.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	embedder := NewTEIEmbedder(server.URL, "model", "revision", 3, time.Second)
	if _, err := embedder.EmbedQuery(context.Background(), "query"); err == nil {
		t.Fatal("client error was accepted")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
