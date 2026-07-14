package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"creatorinsight/backend-go/internal/config"
)

func TestHealthRoute(t *testing.T) {
	router := NewRouter(RouterDeps{
		Config: config.Config{
			App: config.AppConfig{Env: "test"},
		},
		Logger: slog.Default(),
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /health body = %s", rec.Body.String())
	}
}

func TestNotesRoutesReplaceVideoAndDanmuRoutes(t *testing.T) {
	router := NewRouter(RouterDeps{
		Config: config.Config{
			App: config.AppConfig{Env: "test"},
		},
		Logger: slog.Default(),
	})

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/api/v1/notes?limit=1", want: http.StatusInternalServerError},
		{method: http.MethodPost, path: "/api/v1/videos", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/api/v1/videos/1/danmus", want: http.StatusNotFound},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != tt.want {
			t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, rec.Code, tt.want)
		}
	}
}
