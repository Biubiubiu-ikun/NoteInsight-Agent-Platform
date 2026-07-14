package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type accessClaims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	jwt.RegisteredClaims
}

func newAccessToken(secret string, ttl time.Duration, user User) (string, int64, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)
	jti, err := randomToken(16)
	if err != nil {
		return "", 0, err
	}

	claims := accessClaims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		Status:   user.Status,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(user.ID, 10),
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", 0, fmt.Errorf("sign access token: %w", err)
	}

	return signed, int64(ttl.Seconds()), nil
}

func parseAccessToken(secret string, rawToken string) (CurrentUser, error) {
	claims := accessClaims{}
	parsed, err := jwt.ParseWithClaims(rawToken, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil || !parsed.Valid {
		return CurrentUser{}, ErrUnauthorized
	}
	if claims.UserID <= 0 {
		return CurrentUser{}, ErrUnauthorized
	}

	return CurrentUser{
		ID:       claims.UserID,
		Username: claims.Username,
		Role:     claims.Role,
		Status:   claims.Status,
	}, nil
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
