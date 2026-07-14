package auth

import (
	"testing"
	"time"
)

func TestHashPasswordDoesNotStorePlaintext(t *testing.T) {
	plain := "strong_password_123"

	hashed, err := hashPassword(plain, 4)
	if err != nil {
		t.Fatalf("hashPassword() error = %v", err)
	}

	if hashed == plain {
		t.Fatal("password hash must not equal plaintext")
	}
	if len(hashed) == 0 {
		t.Fatal("password hash must not be empty")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	secret := "test-secret"
	user := User{
		ID:       10001,
		Username: "alice",
		Role:     "normal",
		Status:   "active",
	}

	token, expiresIn, err := newAccessToken(secret, time.Hour, user)
	if err != nil {
		t.Fatalf("newAccessToken() error = %v", err)
	}
	if token == "" {
		t.Fatal("access token must not be empty")
	}
	if expiresIn != 3600 {
		t.Fatalf("expiresIn = %d, want 3600", expiresIn)
	}

	current, err := parseAccessToken(secret, token)
	if err != nil {
		t.Fatalf("parseAccessToken() error = %v", err)
	}
	if current.ID != user.ID || current.Username != user.Username || current.Role != user.Role || current.Status != user.Status {
		t.Fatalf("current user = %+v, want id=%d username=%s", current, user.ID, user.Username)
	}
}

func TestRefreshTokenHashDoesNotStoreToken(t *testing.T) {
	token := "refresh-token-value"

	hashed := hashToken(token)

	if hashed == token {
		t.Fatal("refresh token hash must not equal token")
	}
	if hashToken(token) != hashed {
		t.Fatal("refresh token hash must be deterministic")
	}
}
