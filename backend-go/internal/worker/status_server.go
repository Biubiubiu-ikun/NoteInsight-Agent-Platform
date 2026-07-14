package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

type brokerChecker interface {
	Check(ctx context.Context) error
	Connected() bool
}

type StatusServerDeps struct {
	Host         string
	Port         int
	DB           *sqlx.DB
	Redis        *redis.Client
	Broker       brokerChecker
	Logger       *slog.Logger
	CheckTimeout time.Duration
}

func NewStatusServer(deps StatusServerDeps) *http.Server {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	checkTimeout := deps.CheckTimeout
	if checkTimeout <= 0 {
		checkTimeout = 3 * time.Second
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(writer, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/ready", func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), checkTimeout)
		defer cancel()
		status := map[string]string{"postgres": "ok", "redis": "ok", "nats": "ok"}
		ready := true
		if deps.DB == nil || deps.DB.PingContext(ctx) != nil {
			status["postgres"] = "error"
			ready = false
		}
		if deps.Redis == nil || deps.Redis.Ping(ctx).Err() != nil {
			status["redis"] = "error"
			ready = false
		}
		if deps.Broker == nil || !deps.Broker.Connected() || deps.Broker.Check(ctx) != nil {
			status["nats"] = "error"
			ready = false
		}
		code := http.StatusOK
		if !ready {
			code = http.StatusServiceUnavailable
		}
		writeStatusJSON(writer, code, map[string]any{"status": status})
	})
	mux.Handle("/metrics", promhttp.Handler())

	return &http.Server{
		Addr:              net.JoinHostPort(deps.Host, strconv.Itoa(deps.Port)),
		Handler:           requestLogHandler(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func requestLogHandler(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(writer, request)
		logger.Debug("worker http request", "method", request.Method, "path", request.URL.Path, "latency_ms", time.Since(startedAt).Milliseconds())
	})
}

func writeStatusJSON(writer http.ResponseWriter, code int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(code)
	_ = json.NewEncoder(writer).Encode(payload)
}
