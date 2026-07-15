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
	if len(cfg.HTTP.AllowedOrigins) != 0 || len(cfg.HTTP.TrustedProxies) != 0 {
		t.Fatalf("cross-origin access and proxy trust must be opt-in: %+v", cfg.HTTP)
	}
	if !cfg.RateLimit.Enabled || cfg.RateLimit.CommentWrite.Limit != 60 {
		t.Fatalf("unexpected rate limit defaults: %+v", cfg.RateLimit)
	}
	if cfg.Auth.AccessTokenTTL != 30*time.Minute || cfg.RateLimit.Auth.Limit != 20 {
		t.Fatalf("unexpected auth hardening defaults: auth=%+v rate=%+v", cfg.Auth, cfg.RateLimit.Auth)
	}
	if cfg.Postgres.ConnMaxIdleTime != 5*time.Minute || cfg.Postgres.ConnMaxLifetime != 30*time.Minute || cfg.Postgres.MaxIdleConns != 5 {
		t.Fatalf("unexpected PostgreSQL pool defaults: %+v", cfg.Postgres)
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

func TestGetEnvInt32RejectsOverflow(t *testing.T) {
	t.Setenv("POSTGRES_MAX_CONNS", "2147483648")
	if got := getEnvInt32("POSTGRES_MAX_CONNS", 10); got != 10 {
		t.Fatalf("getEnvInt32() = %d, want fallback 10", got)
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

func TestLoadParsesHTTPBoundaries(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_ORIGINS", "https://console.example.com, https://ops.example.com")
	t.Setenv("HTTP_TRUSTED_PROXIES", "10.0.0.0/8,127.0.0.1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.HTTP.AllowedOrigins) != 2 || cfg.HTTP.AllowedOrigins[1] != "https://ops.example.com" {
		t.Fatalf("unexpected allowed origins: %#v", cfg.HTTP.AllowedOrigins)
	}
	if len(cfg.HTTP.TrustedProxies) != 2 || cfg.HTTP.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatalf("unexpected trusted proxies: %#v", cfg.HTTP.TrustedProxies)
	}
}

func TestLoadRejectsWildcardCORSInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("AUTH_JWT_SECRET", "a-production-secret-with-at-least-32-characters")
	t.Setenv("HTTP_ALLOWED_ORIGINS", "*")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected production wildcard CORS error")
	}
}

func TestLoadRejectsInvalidTrustedProxy(t *testing.T) {
	t.Setenv("HTTP_TRUSTED_PROXIES", "not-a-proxy")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected trusted proxy validation error")
	}
}
