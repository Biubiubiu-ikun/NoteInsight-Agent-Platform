package retrieval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQdrantClientQueryUsesAPIKeyAndDecodesPoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/collections/evidence/points/query" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.Header.Get("api-key") != "secret" {
			t.Fatalf("api-key header = %q", request.Header.Get("api-key"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["filter"] == nil || body["with_payload"] != true {
			t.Fatalf("query body = %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok","result":{"points":[{"id":123,"score":0.75,"payload":{"chunk_id":123}}]}}`))
	}))
	defer server.Close()
	client := NewQdrantClient(server.URL, "secret", time.Second)
	hits, err := client.Query(context.Background(), "evidence", []float32{0.1, 0.2}, map[string]any{"must": []any{}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 123 || hits[0].Score != 0.75 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestQdrantClientSurfacesBoundedErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "collection unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewQdrantClient(server.URL, "", time.Second)
	if _, err := client.Count(context.Background(), "evidence"); err == nil {
		t.Fatal("Count() expected dependency error")
	}
}
