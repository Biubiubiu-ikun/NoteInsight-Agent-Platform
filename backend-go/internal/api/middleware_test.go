package api

import (
	"context"
	"errors"
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
