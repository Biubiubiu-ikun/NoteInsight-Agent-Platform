//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/config"
)

var integrationUserSequence atomic.Int64

func TestRefreshReplayRevokesEveryActiveSession(t *testing.T) {
	service := integrationAuthService()
	first := registerIntegrationUser(t, service)
	second, err := service.Login(context.Background(), auth.LoginInput{
		Username: first.User.Username,
		Password: "integration_password",
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := service.Refresh(context.Background(), auth.RefreshInput{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), auth.RefreshInput{RefreshToken: first.RefreshToken}); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("replayed refresh error = %v, want unauthorized", err)
	}
	for name, token := range map[string]string{"replacement": rotated.RefreshToken, "parallel_login": second.RefreshToken} {
		if _, err := service.Refresh(context.Background(), auth.RefreshInput{RefreshToken: token}); !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("%s refresh error = %v, want revoked session", name, err)
		}
	}
}

func TestConcurrentRefreshAllowsOneRotationAndRevokesTheWinner(t *testing.T) {
	service := integrationAuthService()
	registered := registerIntegrationUser(t, service)
	const callers = 12
	start := make(chan struct{})
	results := make(chan auth.AuthResponse, callers)
	errorsChannel := make(chan error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for range callers {
		go func() {
			defer wait.Done()
			<-start
			response, err := service.Refresh(context.Background(), auth.RefreshInput{RefreshToken: registered.RefreshToken})
			results <- response
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)

	successes := 0
	winnerToken := ""
	for response := range results {
		if response.RefreshToken != "" {
			successes++
			winnerToken = response.RefreshToken
		}
	}
	for err := range errorsChannel {
		if err != nil && !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("concurrent refresh error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful rotations = %d, want 1", successes)
	}
	if _, err := service.Refresh(context.Background(), auth.RefreshInput{RefreshToken: winnerToken}); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("winner session should be revoked after replay detection, error = %v", err)
	}
}

func integrationAuthService() *auth.Service {
	return auth.NewService(auth.NewRepository(integrationDB), config.AuthConfig{
		JWTSecret:       "integration-test-secret-at-least-32-characters",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: time.Hour,
		BcryptCost:      4,
	}, "test")
}

func registerIntegrationUser(t *testing.T, service *auth.Service) auth.AuthResponse {
	t.Helper()
	sequence := integrationUserSequence.Add(1)
	response, err := service.Register(context.Background(), auth.RegisterInput{
		Username:  fmt.Sprintf("it_%d_%d", time.Now().UnixNano(), sequence),
		Password:  "integration_password",
		Nickname:  "Integration User",
		UserAgent: "integration-test",
		IPAddress: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("register integration user: %v", err)
	}
	return response
}
