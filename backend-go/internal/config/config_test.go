package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/app?sslmode=disable")
	t.Setenv("REDIS_ADDR", "localhost:6379")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.App.Name != "creatorinsight-api" {
		t.Fatalf("unexpected app name: %s", cfg.App.Name)
	}
	if cfg.HTTP.Port != 8080 {
		t.Fatalf("unexpected http port: %d", cfg.HTTP.Port)
	}
	if cfg.HTTP.Addr() != "0.0.0.0:8080" {
		t.Fatalf("unexpected http addr: %s", cfg.HTTP.Addr())
	}
	if !cfg.RateLimit.Enabled || cfg.RateLimit.CommentWrite.Limit != 60 {
		t.Fatalf("unexpected rate limit defaults: %+v", cfg.RateLimit)
	}
	if !cfg.Reconcile.Enabled || cfg.Reconcile.Interval != time.Hour {
		t.Fatalf("unexpected reconcile defaults: %+v", cfg.Reconcile)
	}
	if cfg.NATS.Stream != "NOTEINSIGHT_EVENTS" || cfg.NATS.Consumer != "noteinsight-worker-v1" {
		t.Fatalf("unexpected NATS defaults: %+v", cfg.NATS)
	}
	if cfg.Worker.HTTPPort != 8081 || cfg.Worker.OutboxMaxRetries != 20 {
		t.Fatalf("unexpected worker defaults: %+v", cfg.Worker)
	}
}

func TestValidateRejectsInvalidPort(t *testing.T) {
	cfg := Config{
		HTTP: HTTPConfig{Port: 70000},
		Postgres: PostgresConfig{
			DSN:      "postgres://user:pass@localhost:5432/app?sslmode=disable",
			MaxConns: 2,
			MinConns: 1,
		},
		Redis: RedisConfig{Addr: "localhost:6379"},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() expected error for invalid port")
	}
}

func TestLoadRejectsDevelopmentJWTSecretInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "PRODUCTION")
	t.Setenv("AUTH_JWT_SECRET", defaultDevelopmentJWTSecret)

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected production JWT secret error")
	}
}

func TestLoadRejectsShortJWTSecretInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("AUTH_JWT_SECRET", "too-short")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected short production JWT secret error")
	}
}

func TestLoadNormalizesProductionEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", " Production ")
	t.Setenv("AUTH_JWT_SECRET", "a-production-secret-with-at-least-32-characters")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.Env != "prod" {
		t.Fatalf("App.Env = %q, want prod", cfg.App.Env)
	}
}
