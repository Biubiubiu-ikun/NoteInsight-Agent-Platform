package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQdrantClientAuditsAndDeletesPointIDs(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/collections/evidence":
			_, _ = writer.Write([]byte(`{"status":"ok","result":{}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/collections/evidence/points/scroll":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["offset"] == nil {
				if fmt.Sprint(body["with_payload"]) != "[content_hash]" {
					t.Fatalf("scroll payload selector = %v", body["with_payload"])
				}
				_, _ = writer.Write([]byte(`{"status":"ok","result":{"points":[{"id":1,"payload":{"content_hash":"hash-1"}},{"id":2,"payload":{"content_hash":"hash-2"}}],"next_page_offset":2}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"status":"ok","result":{"points":[{"id":3,"payload":{"content_hash":"hash-3"}}],"next_page_offset":null}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/collections/evidence/points/delete":
			var body struct {
				Points []int64 `json:"points"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(body.Points) != "[99]" || request.URL.Query().Get("wait") != "true" {
				t.Fatalf("delete request = %+v query=%s", body, request.URL.RawQuery)
			}
			deleted = true
			_, _ = writer.Write([]byte(`{"status":"ok","result":{}}`))
		default:
			t.Fatalf("unexpected Qdrant request: %s %s", request.Method, request.URL.String())
		}
	}))
	defer server.Close()
	client := NewQdrantClient(server.URL, "", time.Second)
	exists, err := client.CollectionExists(context.Background(), "evidence")
	if err != nil || !exists {
		t.Fatalf("CollectionExists() = %t, %v", exists, err)
	}
	manifest, err := client.ListPointManifest(context.Background(), "evidence")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(manifest) != "[{1 hash-1} {2 hash-2} {3 hash-3}]" {
		t.Fatalf("point manifest = %v", manifest)
	}
	if err := client.DeletePoints(context.Background(), "evidence", []int64{99}); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

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
