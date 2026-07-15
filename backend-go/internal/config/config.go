package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultDevelopmentJWTSecret = "local-dev-noteinsight-secret-change-me"

type Config struct {
	App       AppConfig
	HTTP      HTTPConfig
	Auth      AuthConfig
	Log       LogConfig
	Postgres  PostgresConfig
	Redis     RedisConfig
	NATS      NATSConfig
	RateLimit RateLimitConfig
	Worker    WorkerConfig
	Reconcile ReconcileConfig
}

type AppConfig struct {
	Name string
	Env  string
}

type HTTPConfig struct {
	Host              string
	Port              int
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	AllowedOrigins    []string
	TrustedProxies    []string
}

func (c HTTPConfig) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

type AuthConfig struct {
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	BcryptCost      int
}

type LogConfig struct {
	Level string
}

type PostgresConfig struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxIdleConns    int32
	ConnMaxIdleTime time.Duration
	ConnMaxLifetime time.Duration
	ConnectTimeout  time.Duration
	PingTimeout     time.Duration
}

type RedisConfig struct {
	Addr         string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PingTimeout  time.Duration
}

type NATSConfig struct {
	URL              string
	ConnectionName   string
	Stream           string
	SubjectPrefix    string
	DLQStream        string
	DLQSubjectPrefix string
	Consumer         string
	ConnectTimeout   time.Duration
	RequestTimeout   time.Duration
	DrainTimeout     time.Duration
	StreamMaxAge     time.Duration
	DLQMaxAge        time.Duration
	DuplicateWindow  time.Duration
	AckWait          time.Duration
	NakDelay         time.Duration
	MaxDeliver       int
	MaxAckPending    int
}

type RateLimitConfig struct {
	Enabled          bool
	Auth             RateLimitPolicyConfig
	ContentWrite     RateLimitPolicyConfig
	CommentWrite     RateLimitPolicyConfig
	InteractionWrite RateLimitPolicyConfig
}

type RateLimitPolicyConfig struct {
	Limit  int64
	Window time.Duration
}

type WorkerConfig struct {
	HTTPHost                string
	HTTPPort                int
	OutboxBatchSize         int
	OutboxMaxRetries        int
	OutboxPollInterval      time.Duration
	OutboxRecoveryInterval  time.Duration
	OutboxProcessingTimeout time.Duration
	ConsumerBatchSize       int
	MetricsInterval         time.Duration
}

type ReconcileConfig struct {
	Enabled      bool
	StartupDelay time.Duration
	Interval     time.Duration
	Timeout      time.Duration
	RankingLimit int64
}

func Load() (Config, error) {
	cfg := Config{
		App: AppConfig{
			Name: getEnv("APP_NAME", "creatorinsight-api"),
			Env:  normalizeEnvironment(getEnv("APP_ENV", "local")),
		},
		HTTP: HTTPConfig{
			Host:              getEnv("HTTP_HOST", "0.0.0.0"),
			Port:              getEnvInt("HTTP_PORT", 8080),
			ReadHeaderTimeout: getEnvDuration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       getEnvDuration("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:      getEnvDuration("HTTP_WRITE_TIMEOUT", 10*time.Second),
			IdleTimeout:       getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			AllowedOrigins:    getEnvList("HTTP_ALLOWED_ORIGINS"),
			TrustedProxies:    getEnvList("HTTP_TRUSTED_PROXIES"),
		},
		Auth: AuthConfig{
			JWTSecret:       getEnv("AUTH_JWT_SECRET", defaultDevelopmentJWTSecret),
			AccessTokenTTL:  getEnvDuration("AUTH_ACCESS_TOKEN_TTL", 30*time.Minute),
			RefreshTokenTTL: getEnvDuration("AUTH_REFRESH_TOKEN_TTL", 7*24*time.Hour),
			BcryptCost:      getEnvInt("AUTH_BCRYPT_COST", 10),
		},
		Log: LogConfig{
			Level: getEnv("LOG_LEVEL", "info"),
		},
		Postgres: PostgresConfig{
			DSN:             getEnv("POSTGRES_DSN", "postgres://creatorinsight:creatorinsight@localhost:5432/creatorinsight?sslmode=disable"),
			MaxConns:        int32(getEnvInt("POSTGRES_MAX_CONNS", 10)),
			MinConns:        int32(getEnvInt("POSTGRES_MIN_CONNS", 1)),
			MaxIdleConns:    int32(getEnvInt("POSTGRES_MAX_IDLE_CONNS", 5)),
			ConnMaxIdleTime: getEnvDuration("POSTGRES_CONN_MAX_IDLE_TIME", 5*time.Minute),
			ConnMaxLifetime: getEnvDuration("POSTGRES_CONN_MAX_LIFETIME", 30*time.Minute),
			ConnectTimeout:  getEnvDuration("POSTGRES_CONNECT_TIMEOUT", 5*time.Second),
			PingTimeout:     getEnvDuration("POSTGRES_PING_TIMEOUT", 3*time.Second),
		},
		Redis: RedisConfig{
			Addr:         getEnv("REDIS_ADDR", "localhost:6379"),
			Password:     getEnv("REDIS_PASSWORD", ""),
			DB:           getEnvInt("REDIS_DB", 0),
			DialTimeout:  getEnvDuration("REDIS_DIAL_TIMEOUT", 5*time.Second),
			ReadTimeout:  getEnvDuration("REDIS_READ_TIMEOUT", 3*time.Second),
			WriteTimeout: getEnvDuration("REDIS_WRITE_TIMEOUT", 3*time.Second),
			PingTimeout:  getEnvDuration("REDIS_PING_TIMEOUT", 3*time.Second),
		},
		NATS: NATSConfig{
			URL:              getEnv("NATS_URL", "nats://localhost:4222"),
			ConnectionName:   getEnv("NATS_CONNECTION_NAME", "noteinsight-worker"),
			Stream:           getEnv("NATS_STREAM", "NOTEINSIGHT_EVENTS"),
			SubjectPrefix:    getEnv("NATS_SUBJECT_PREFIX", "noteinsight.events"),
			DLQStream:        getEnv("NATS_DLQ_STREAM", "NOTEINSIGHT_DLQ"),
			DLQSubjectPrefix: getEnv("NATS_DLQ_SUBJECT_PREFIX", "noteinsight.dlq"),
			Consumer:         getEnv("NATS_CONSUMER", "noteinsight-worker-v1"),
			ConnectTimeout:   getEnvDuration("NATS_CONNECT_TIMEOUT", 5*time.Second),
			RequestTimeout:   getEnvDuration("NATS_REQUEST_TIMEOUT", 5*time.Second),
			DrainTimeout:     getEnvDuration("NATS_DRAIN_TIMEOUT", 10*time.Second),
			StreamMaxAge:     getEnvDuration("NATS_STREAM_MAX_AGE", 7*24*time.Hour),
			DLQMaxAge:        getEnvDuration("NATS_DLQ_MAX_AGE", 30*24*time.Hour),
			DuplicateWindow:  getEnvDuration("NATS_DUPLICATE_WINDOW", 10*time.Minute),
			AckWait:          getEnvDuration("NATS_ACK_WAIT", 30*time.Second),
			NakDelay:         getEnvDuration("NATS_NAK_DELAY", 2*time.Second),
			MaxDeliver:       getEnvInt("NATS_MAX_DELIVER", 5),
			MaxAckPending:    getEnvInt("NATS_MAX_ACK_PENDING", 1000),
		},
		RateLimit: RateLimitConfig{
			Enabled: getEnvBool("RATE_LIMIT_ENABLED", true),
			Auth: RateLimitPolicyConfig{
				Limit:  int64(getEnvInt("RATE_LIMIT_AUTH_LIMIT", 20)),
				Window: getEnvDuration("RATE_LIMIT_AUTH_WINDOW", time.Minute),
			},
			ContentWrite: RateLimitPolicyConfig{
				Limit:  int64(getEnvInt("RATE_LIMIT_CONTENT_WRITE_LIMIT", 30)),
				Window: getEnvDuration("RATE_LIMIT_CONTENT_WRITE_WINDOW", time.Minute),
			},
			CommentWrite: RateLimitPolicyConfig{
				Limit:  int64(getEnvInt("RATE_LIMIT_COMMENT_WRITE_LIMIT", 60)),
				Window: getEnvDuration("RATE_LIMIT_COMMENT_WRITE_WINDOW", time.Minute),
			},
			InteractionWrite: RateLimitPolicyConfig{
				Limit:  int64(getEnvInt("RATE_LIMIT_INTERACTION_WRITE_LIMIT", 120)),
				Window: getEnvDuration("RATE_LIMIT_INTERACTION_WRITE_WINDOW", time.Minute),
			},
		},
		Worker: WorkerConfig{
			HTTPHost:                getEnv("WORKER_HTTP_HOST", "0.0.0.0"),
			HTTPPort:                getEnvInt("WORKER_HTTP_PORT", 8081),
			OutboxBatchSize:         getEnvInt("OUTBOX_BATCH_SIZE", 50),
			OutboxMaxRetries:        getEnvInt("OUTBOX_MAX_RETRIES", 20),
			OutboxPollInterval:      getEnvDuration("OUTBOX_POLL_INTERVAL", 500*time.Millisecond),
			OutboxRecoveryInterval:  getEnvDuration("OUTBOX_RECOVERY_INTERVAL", time.Minute),
			OutboxProcessingTimeout: getEnvDuration("OUTBOX_PROCESSING_TIMEOUT", 5*time.Minute),
			ConsumerBatchSize:       getEnvInt("WORKER_CONSUMER_BATCH_SIZE", 50),
			MetricsInterval:         getEnvDuration("WORKER_METRICS_INTERVAL", 5*time.Second),
		},
		Reconcile: ReconcileConfig{
			Enabled:      getEnvBool("RECONCILE_ENABLED", true),
			StartupDelay: getEnvDuration("RECONCILE_STARTUP_DELAY", 10*time.Second),
			Interval:     getEnvDuration("RECONCILE_INTERVAL", time.Hour),
			Timeout:      getEnvDuration("RECONCILE_TIMEOUT", 5*time.Minute),
			RankingLimit: int64(getEnvInt("RECONCILE_RANKING_LIMIT", 1000)),
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.HTTP.Port <= 0 || c.HTTP.Port > 65535 {
		return fmt.Errorf("HTTP_PORT must be between 1 and 65535, got %d", c.HTTP.Port)
	}
	for _, proxy := range c.HTTP.TrustedProxies {
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				return fmt.Errorf("HTTP_TRUSTED_PROXIES contains invalid IP or CIDR %q", proxy)
			}
		}
	}
	if c.Postgres.DSN == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	if c.Redis.Addr == "" {
		return errors.New("REDIS_ADDR is required")
	}
	if c.Postgres.MaxConns < c.Postgres.MinConns {
		return errors.New("POSTGRES_MAX_CONNS must be greater than or equal to POSTGRES_MIN_CONNS")
	}
	if c.Postgres.MaxIdleConns < 0 || c.Postgres.MaxIdleConns > c.Postgres.MaxConns {
		return errors.New("POSTGRES_MAX_IDLE_CONNS must be between 0 and POSTGRES_MAX_CONNS")
	}
	if c.Postgres.ConnMaxIdleTime <= 0 || c.Postgres.ConnMaxLifetime <= 0 {
		return errors.New("PostgreSQL connection idle time and lifetime must be greater than 0")
	}
	if c.Auth.JWTSecret == "" {
		return errors.New("AUTH_JWT_SECRET is required")
	}
	if c.App.Env == "prod" {
		if c.Auth.JWTSecret == defaultDevelopmentJWTSecret {
			return errors.New("AUTH_JWT_SECRET must be changed in production")
		}
		if len(c.Auth.JWTSecret) < 32 {
			return errors.New("AUTH_JWT_SECRET must be at least 32 characters in production")
		}
		if c.Auth.AccessTokenTTL > 2*time.Hour {
			return errors.New("AUTH_ACCESS_TOKEN_TTL must not exceed 2 hours in production")
		}
		for _, origin := range c.HTTP.AllowedOrigins {
			if origin == "*" {
				return errors.New("HTTP_ALLOWED_ORIGINS must not contain wildcard in production")
			}
		}
	}
	if c.Auth.AccessTokenTTL <= 0 {
		return errors.New("AUTH_ACCESS_TOKEN_TTL must be greater than 0")
	}
	if c.Auth.RefreshTokenTTL <= 0 {
		return errors.New("AUTH_REFRESH_TOKEN_TTL must be greater than 0")
	}
	if c.Auth.BcryptCost < 4 || c.Auth.BcryptCost > 31 {
		return errors.New("AUTH_BCRYPT_COST must be between 4 and 31")
	}
	if c.NATS.URL == "" || c.NATS.Stream == "" || c.NATS.SubjectPrefix == "" || c.NATS.DLQStream == "" || c.NATS.DLQSubjectPrefix == "" || c.NATS.Consumer == "" {
		return errors.New("NATS URL, stream, subjects, DLQ stream, and consumer are required")
	}
	if c.NATS.ConnectTimeout <= 0 || c.NATS.RequestTimeout <= 0 || c.NATS.DrainTimeout <= 0 || c.NATS.StreamMaxAge <= 0 || c.NATS.DLQMaxAge <= 0 || c.NATS.DuplicateWindow <= 0 || c.NATS.AckWait <= 0 || c.NATS.NakDelay <= 0 {
		return errors.New("NATS timeout and retention settings must be greater than 0")
	}
	if c.NATS.MaxDeliver <= 0 || c.NATS.MaxAckPending <= 0 {
		return errors.New("NATS delivery limits must be greater than 0")
	}
	if c.RateLimit.Enabled {
		policies := map[string]RateLimitPolicyConfig{
			"auth":              c.RateLimit.Auth,
			"content write":     c.RateLimit.ContentWrite,
			"comment write":     c.RateLimit.CommentWrite,
			"interaction write": c.RateLimit.InteractionWrite,
		}
		for name, policy := range policies {
			if policy.Limit <= 0 || policy.Window <= 0 {
				return fmt.Errorf("%s rate limit and window must be greater than 0", name)
			}
		}
	}
	if c.Worker.HTTPPort <= 0 || c.Worker.HTTPPort > 65535 {
		return fmt.Errorf("WORKER_HTTP_PORT must be between 1 and 65535, got %d", c.Worker.HTTPPort)
	}
	if c.Worker.OutboxBatchSize <= 0 || c.Worker.OutboxMaxRetries <= 0 || c.Worker.OutboxPollInterval <= 0 || c.Worker.OutboxRecoveryInterval <= 0 || c.Worker.OutboxProcessingTimeout <= 0 || c.Worker.ConsumerBatchSize <= 0 || c.Worker.MetricsInterval <= 0 {
		return errors.New("outbox worker settings must be greater than 0")
	}
	if c.Reconcile.Enabled && (c.Reconcile.StartupDelay < 0 || c.Reconcile.Interval <= 0 || c.Reconcile.Timeout <= 0 || c.Reconcile.RankingLimit <= 0) {
		return errors.New("reconcile interval, timeout, and ranking limit must be greater than 0")
	}
	return nil
}

func normalizeEnvironment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "production" {
		return "prod"
	}
	return value
}

func getEnv(key string, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func getEnvInt(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvBool(key string, fallback bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
