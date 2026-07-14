package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResponseCompression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ResponseCompression())
	router.GET("/api/v1/large", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"body": strings.Repeat("note insight ", 100)})
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/large", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", response.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read compressed response: %v", err)
	}
	if !strings.Contains(string(decompressed), "note insight") {
		t.Fatalf("decompressed body = %q", decompressed)
	}
}

func TestResponseCompressionHonorsClientPreference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ResponseCompression())
	router.GET("/api/v1/plain", func(ctx *gin.Context) {
		ctx.String(http.StatusOK, "plain response")
	})

	for _, encoding := range []string{"", "gzip;q=0"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/plain", nil)
		request.Header.Set("Accept-Encoding", encoding)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if got := response.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Accept-Encoding %q produced Content-Encoding %q", encoding, got)
		}
		if response.Body.String() != "plain response" {
			t.Fatalf("response body = %q", response.Body.String())
		}
	}
}
