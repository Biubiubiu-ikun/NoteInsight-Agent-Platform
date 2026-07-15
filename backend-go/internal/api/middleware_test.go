package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/api/ctxauth"
	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/platform/ratelimit"

	"github.com/gin-gonic/gin"
)

type fakeRateLimiter struct {
	decision ratelimit.Decision
	err      error
	key      string
	policy   ratelimit.Policy
}

func (f *fakeRateLimiter) Allow(_ context.Context, key string, policy ratelimit.Policy) (ratelimit.Decision, error) {
	f.key = key
	f.policy = policy
	return f.decision, f.err
}

func TestUserRateLimitRejectsOverLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &fakeRateLimiter{decision: ratelimit.Decision{
		Allowed:    false,
		Limit:      2,
		Remaining:  0,
		ResetAt:    time.Unix(200, 0),
		RetryAfter: 1500 * time.Millisecond,
	}}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctxauth.SetCurrentUser(ctx, auth.CurrentUser{ID: 42, Status: "active"})
	})
	router.POST("/write", UserRateLimit(limiter, true, ratelimit.Policy{
		Name: "comment_write", Limit: 2, Window: time.Minute,
	}), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/write", nil))

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
	}
	if recorder.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After = %q, want 2", recorder.Header().Get("Retry-After"))
	}
	if limiter.key != "rate:user:42:comment_write" {
		t.Fatalf("key = %q", limiter.key)
	}
}

func TestIPRateLimitUsesClientAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &fakeRateLimiter{decision: ratelimit.Decision{Allowed: true, Limit: 10, Remaining: 9, ResetAt: time.Now().Add(time.Minute)}}
	router := gin.New()
	router.POST("/login", IPRateLimit(limiter, true, ratelimit.Policy{Name: "auth", Limit: 10, Window: time.Minute}), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "192.0.2.10:4321"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if limiter.key != "rate:ip:192.0.2.10:auth" {
		t.Fatalf("key = %q", limiter.key)
	}
}

func TestRequestLoggerAddsCorrelationHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestLogger(slog.Default()))
	router.GET("/request-id", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/request-id", nil))
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID header is missing")
	}
}

func TestUserRateLimitFailsClosedWhenRedisFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &fakeRateLimiter{err: errors.New("redis unavailable")}
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctxauth.SetCurrentUser(ctx, auth.CurrentUser{ID: 7, Status: "active"})
	})
	router.POST("/write", UserRateLimit(limiter, true, ratelimit.Policy{
		Name: "content_write", Limit: 1, Window: time.Minute,
	}), func(ctx *gin.Context) {
		ctx.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/write", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestCORSAllowsConfiguredOriginAndPreflight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS([]string{"https://console.example.com"}))
	router.OPTIONS("/notes", func(ctx *gin.Context) { ctx.Status(http.StatusTeapot) })
	request := httptest.NewRequest(http.MethodOptions, "/notes", nil)
	request.Header.Set("Origin", "https://console.example.com")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "https://console.example.com" {
		t.Fatalf("unexpected allow origin: %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS([]string{"https://console.example.com"}))
	router.GET("/notes", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/notes", nil)
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
