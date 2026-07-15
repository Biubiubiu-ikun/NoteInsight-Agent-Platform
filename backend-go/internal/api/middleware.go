package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/api/ctxauth"
	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/platform/observability"
	"creatorinsight/backend-go/internal/platform/ratelimit"
	"creatorinsight/backend-go/internal/platform/requestmeta"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

type RateLimiter interface {
	Allow(ctx context.Context, key string, policy ratelimit.Policy) (ratelimit.Decision, error)
}

func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return func(ctx *gin.Context) {
		origin := strings.TrimSpace(ctx.GetHeader("Origin"))
		if origin == "" {
			ctx.Next()
			return
		}
		if _, ok := allowed[origin]; !ok {
			ctx.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
			ctx.Abort()
			return
		}
		ctx.Header("Vary", "Origin")
		ctx.Header("Access-Control-Allow-Origin", origin)
		ctx.Header("Access-Control-Allow-Credentials", "true")
		ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, X-Trace-ID")
		ctx.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if ctx.Request.Method == http.MethodOptions {
			ctx.Status(http.StatusNoContent)
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}

func DBPoolMetrics(db *sqlx.DB) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Next()
		if db == nil {
			return
		}
		observability.SetDBPoolStats(db.Stats())
	}
}

func AuditMutation(logger *slog.Logger, action string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Next()
		currentUser, ok := ctxauth.CurrentUser(ctx)
		if !ok {
			return
		}
		metadata := requestmeta.From(ctx.Request.Context())
		logger.Info("audit mutation",
			"action", action,
			"actor_user_id", currentUser.ID,
			"actor_role", currentUser.Role,
			"elevated", currentUser.Role == "admin",
			"method", ctx.Request.Method,
			"path", ctx.Request.URL.Path,
			"status", ctx.Writer.Status(),
			"request_id", metadata.RequestID,
			"trace_id", metadata.TraceID,
		)
	}
}

func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		startedAt := time.Now()
		requestID := strings.TrimSpace(ctx.GetHeader("X-Request-ID"))
		if requestID == "" {
			requestID = requestmeta.NewID("req")
		}
		traceID := strings.TrimSpace(ctx.GetHeader("X-Trace-ID"))
		if traceID == "" {
			traceID = requestID
		}
		ctx.Header("X-Request-ID", requestID)
		ctx.Request = ctx.Request.WithContext(requestmeta.With(ctx.Request.Context(), requestmeta.Metadata{
			RequestID: requestID,
			TraceID:   traceID,
		}))
		ctx.Next()

		fields := []any{
			"http request",
			"method", ctx.Request.Method,
			"path", ctx.Request.URL.Path,
			"status", ctx.Writer.Status(),
			"latency_ms", time.Since(startedAt).Milliseconds(),
			"client_ip", ctx.ClientIP(),
			"request_id", requestID,
			"trace_id", traceID,
		}
		if currentUser, ok := ctxauth.CurrentUser(ctx); ok {
			fields = append(fields, "user_id", currentUser.ID)
		}
		logger.Info(fields[0].(string), fields[1:]...)

		status := strconv.Itoa(ctx.Writer.Status())
		routePath := ctx.FullPath()
		if routePath == "" {
			routePath = ctx.Request.URL.Path
		}
		observability.HTTPRequestsTotal.WithLabelValues(ctx.Request.Method, routePath, status).Inc()
		observability.HTTPRequestDuration.WithLabelValues(ctx.Request.Method, routePath, status).Observe(time.Since(startedAt).Seconds())
	}
}

func AuthMiddleware(authService *auth.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		header := strings.TrimSpace(ctx.GetHeader("Authorization"))
		if header == "" {
			ctx.Next()
			return
		}

		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			ctx.Abort()
			return
		}

		currentUser, err := authService.AuthenticateBearer(ctx.Request.Context(), strings.TrimSpace(strings.TrimPrefix(header, prefix)))
		if err != nil {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			ctx.Abort()
			return
		}

		ctxauth.SetCurrentUser(ctx, currentUser)
		ctx.Next()
	}
}

func RequireAuth() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if _, ok := ctxauth.CurrentUser(ctx); !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}

func RequireActiveUser() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		currentUser, ok := ctxauth.CurrentUser(ctx)
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			ctx.Abort()
			return
		}
		if currentUser.Status != "active" {
			ctx.JSON(http.StatusForbidden, gin.H{"error": "active user required"})
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}

func UserRateLimit(limiter RateLimiter, enabled bool, policy ratelimit.Policy) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !enabled {
			ctx.Next()
			return
		}

		currentUser, ok := ctxauth.CurrentUser(ctx)
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			ctx.Abort()
			return
		}

		key := fmt.Sprintf("rate:user:%d:%s", currentUser.ID, policy.Name)
		decision, err := limiter.Allow(ctx.Request.Context(), key, policy)
		if err != nil {
			observability.IncRateLimitDecision(policy.Name, "error")
			slog.Warn("rate limiter unavailable", "policy", policy.Name, "user_id", currentUser.ID, "error", err)
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
			ctx.Abort()
			return
		}

		ctx.Header("X-RateLimit-Limit", strconv.FormatInt(decision.Limit, 10))
		ctx.Header("X-RateLimit-Remaining", strconv.FormatInt(decision.Remaining, 10))
		ctx.Header("X-RateLimit-Reset", strconv.FormatInt(decision.ResetAt.Unix(), 10))
		if !decision.Allowed {
			retrySeconds := int(math.Ceil(decision.RetryAfter.Seconds()))
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			ctx.Header("Retry-After", strconv.Itoa(retrySeconds))
			observability.IncRateLimitDecision(policy.Name, "denied")
			ctx.JSON(http.StatusTooManyRequests, gin.H{
				"error":               "rate limit exceeded",
				"policy":              policy.Name,
				"retry_after_seconds": retrySeconds,
			})
			ctx.Abort()
			return
		}

		observability.IncRateLimitDecision(policy.Name, "allowed")
		ctx.Next()
	}
}

func IPRateLimit(limiter RateLimiter, enabled bool, policy ratelimit.Policy) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !enabled {
			ctx.Next()
			return
		}
		key := fmt.Sprintf("rate:ip:%s:%s", ctx.ClientIP(), policy.Name)
		decision, err := limiter.Allow(ctx.Request.Context(), key, policy)
		if err != nil {
			observability.IncRateLimitDecision(policy.Name, "error")
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "rate limiter unavailable"})
			ctx.Abort()
			return
		}
		ctx.Header("X-RateLimit-Limit", strconv.FormatInt(decision.Limit, 10))
		ctx.Header("X-RateLimit-Remaining", strconv.FormatInt(decision.Remaining, 10))
		ctx.Header("X-RateLimit-Reset", strconv.FormatInt(decision.ResetAt.Unix(), 10))
		if !decision.Allowed {
			retrySeconds := max(1, int(math.Ceil(decision.RetryAfter.Seconds())))
			ctx.Header("Retry-After", strconv.Itoa(retrySeconds))
			observability.IncRateLimitDecision(policy.Name, "denied")
			ctx.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded", "policy": policy.Name, "retry_after_seconds": retrySeconds})
			ctx.Abort()
			return
		}
		observability.IncRateLimitDecision(policy.Name, "allowed")
		ctx.Next()
	}
}

func RequireOwnerOrAdmin(check func(*gin.Context, auth.CurrentUser) (bool, error)) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		currentUser, ok := ctxauth.CurrentUser(ctx)
		if !ok {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			ctx.Abort()
			return
		}
		if currentUser.Role == "admin" {
			ctx.Next()
			return
		}

		allowed, err := check(ctx, currentUser)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, auth.ErrNotFound) {
				status = http.StatusNotFound
			}
			ctx.JSON(status, gin.H{"error": "permission check failed"})
			ctx.Abort()
			return
		}
		if !allowed {
			ctx.JSON(http.StatusForbidden, gin.H{"error": "owner or admin required"})
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}
