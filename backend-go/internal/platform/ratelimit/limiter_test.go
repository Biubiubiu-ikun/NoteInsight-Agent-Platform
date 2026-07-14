package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeEvaler struct {
	values []any
	err    error
	key    string
	window any
}

func (f *fakeEvaler) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if len(keys) > 0 {
		f.key = keys[0]
	}
	if len(args) > 0 {
		f.window = args[0]
	}
	if f.err != nil {
		cmd.SetErr(f.err)
		return cmd
	}
	cmd.SetVal(f.values)
	return cmd
}

func TestLimiterAllow(t *testing.T) {
	runner := &fakeEvaler{values: []any{int64(3), int64(2500)}}
	now := time.Unix(100, 0)
	limiter := &Limiter{redis: runner, now: func() time.Time { return now }}

	decision, err := limiter.Allow(context.Background(), "rate:user:42:comment_write", Policy{
		Name:   "comment_write",
		Limit:  5,
		Window: time.Minute,
	})
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !decision.Allowed || decision.Remaining != 2 {
		t.Fatalf("Allow() decision = %+v", decision)
	}
	if got, want := decision.ResetAt, now.Add(2500*time.Millisecond); !got.Equal(want) {
		t.Fatalf("ResetAt = %v, want %v", got, want)
	}
	if runner.key != "rate:user:42:comment_write" {
		t.Fatalf("redis key = %q", runner.key)
	}
	if runner.window != int64(time.Minute.Milliseconds()) {
		t.Fatalf("window arg = %#v", runner.window)
	}
}

func TestLimiterRejectsOverLimit(t *testing.T) {
	runner := &fakeEvaler{values: []any{int64(6), int64(1200)}}
	limiter := &Limiter{redis: runner, now: time.Now}

	decision, err := limiter.Allow(context.Background(), "rate:user:42:interaction_write", Policy{
		Name:   "interaction_write",
		Limit:  5,
		Window: time.Minute,
	})
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if decision.Allowed || decision.Remaining != 0 || decision.RetryAfter != 1200*time.Millisecond {
		t.Fatalf("Allow() decision = %+v", decision)
	}
}

func TestLimiterPropagatesRedisError(t *testing.T) {
	limiter := &Limiter{redis: &fakeEvaler{err: errors.New("redis down")}, now: time.Now}
	_, err := limiter.Allow(context.Background(), "rate:user:42:comment_write", Policy{
		Limit:  1,
		Window: time.Minute,
	})
	if err == nil {
		t.Fatal("Allow() expected error")
	}
}
