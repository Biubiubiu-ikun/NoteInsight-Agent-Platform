package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const fixedWindowScript = `
local current = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if current == 1 or ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {current, ttl}
`

type Policy struct {
	Name   string
	Limit  int64
	Window time.Duration
}

type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	ResetAt    time.Time
	RetryAfter time.Duration
}

type evaler interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
}

type Limiter struct {
	redis evaler
	now   func() time.Time
}

func New(client *redis.Client) *Limiter {
	return &Limiter{redis: client, now: time.Now}
}

func (l *Limiter) Allow(ctx context.Context, key string, policy Policy) (Decision, error) {
	if l == nil || l.redis == nil {
		return Decision{}, errors.New("rate limiter redis client is unavailable")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return Decision{}, errors.New("rate limit key is required")
	}
	if policy.Limit <= 0 {
		return Decision{}, errors.New("rate limit must be greater than 0")
	}
	if policy.Window <= 0 {
		return Decision{}, errors.New("rate limit window must be greater than 0")
	}

	values, err := l.redis.Eval(ctx, fixedWindowScript, []string{key}, policy.Window.Milliseconds()).Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("evaluate rate limit: %w", err)
	}
	if len(values) != 2 {
		return Decision{}, fmt.Errorf("unexpected rate limit response length: %d", len(values))
	}

	current, err := integerValue(values[0])
	if err != nil {
		return Decision{}, fmt.Errorf("decode rate limit count: %w", err)
	}
	ttlMillis, err := integerValue(values[1])
	if err != nil {
		return Decision{}, fmt.Errorf("decode rate limit ttl: %w", err)
	}
	if ttlMillis <= 0 {
		ttlMillis = policy.Window.Milliseconds()
	}

	remaining := policy.Limit - current
	if remaining < 0 {
		remaining = 0
	}
	ttl := time.Duration(ttlMillis) * time.Millisecond
	decision := Decision{
		Allowed:   current <= policy.Limit,
		Limit:     policy.Limit,
		Remaining: remaining,
		ResetAt:   l.now().Add(ttl),
	}
	if !decision.Allowed {
		decision.RetryAfter = ttl
	}
	return decision, nil
}

func integerValue(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported integer type %T", value)
	}
}
