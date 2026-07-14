package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(env string, level string) *slog.Logger {
	return NewForService("creatorinsight-api", env, level)
}

func NewForService(service string, env string, level string) *slog.Logger {
	handlerOptions := &slog.HandlerOptions{
		Level: parseLevel(level),
	}

	var handler slog.Handler
	output := io.Writer(os.Stdout)
	if env == "local" || env == "dev" {
		handler = slog.NewTextHandler(output, handlerOptions)
	} else {
		handler = slog.NewJSONHandler(output, handlerOptions)
	}

	return slog.New(handler).With(
		slog.String("service", service),
		slog.String("env", env),
	)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
