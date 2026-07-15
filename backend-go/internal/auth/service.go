package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/config"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

type Service struct {
	repo *Repository
	cfg  config.AuthConfig
	env  string
}

func NewService(repo *Repository, cfg config.AuthConfig, env string) *Service {
	return &Service{repo: repo, cfg: cfg, env: env}
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (AuthResponse, error) {
	input.Username = normalizeUsername(input.Username)
	input.Nickname = strings.TrimSpace(input.Nickname)
	if err := validateUsername(input.Username); err != nil {
		return AuthResponse{}, err
	}
	if err := validatePassword(input.Password); err != nil {
		return AuthResponse{}, err
	}
	if len(input.Nickname) > 64 {
		return AuthResponse{}, ValidationError{Field: "nickname", Message: "must be at most 64 characters"}
	}

	passwordHash, err := hashPassword(input.Password, s.cfg.BcryptCost)
	if err != nil {
		return AuthResponse{}, err
	}

	user, err := s.repo.CreateUserWithCredentials(ctx, input.Username, passwordHash, input.Nickname)
	if err != nil {
		return AuthResponse{}, err
	}
	return s.issueTokens(ctx, user, input.UserAgent, input.IPAddress)
}

func (s *Service) Login(ctx context.Context, input LoginInput) (AuthResponse, error) {
	input.Username = normalizeUsername(input.Username)
	if err := validateUsername(input.Username); err != nil {
		return AuthResponse{}, ErrUnauthorized
	}

	credential, user, err := s.repo.GetCredentialByUsername(ctx, input.Username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return AuthResponse{}, ErrUnauthorized
		}
		return AuthResponse{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(credential.PasswordHash), []byte(input.Password)); err != nil {
		return AuthResponse{}, ErrUnauthorized
	}

	return s.issueTokens(ctx, user, input.UserAgent, input.IPAddress)
}

func (s *Service) Refresh(ctx context.Context, input RefreshInput) (AuthResponse, error) {
	input.RefreshToken = strings.TrimSpace(input.RefreshToken)
	if input.RefreshToken == "" {
		return AuthResponse{}, ValidationError{Field: "refresh_token", Message: "is required"}
	}

	refreshToken, err := randomToken(32)
	if err != nil {
		return AuthResponse{}, err
	}
	session, err := s.repo.RotateSession(
		ctx,
		hashToken(input.RefreshToken),
		hashToken(refreshToken),
		input.UserAgent,
		input.IPAddress,
		time.Now().Add(s.cfg.RefreshTokenTTL),
	)
	if err != nil {
		return AuthResponse{}, err
	}

	user, err := s.repo.GetUserByID(ctx, session.UserID)
	if err != nil {
		return AuthResponse{}, err
	}
	accessToken, expiresIn, err := newAccessToken(s.cfg.JWTSecret, s.cfg.AccessTokenTTL, user)
	if err != nil {
		return AuthResponse{}, err
	}

	return AuthResponse{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	}, nil
}

func (s *Service) Logout(ctx context.Context, input LogoutInput) error {
	input.RefreshToken = strings.TrimSpace(input.RefreshToken)
	if input.RefreshToken == "" {
		return ValidationError{Field: "refresh_token", Message: "is required"}
	}
	return s.repo.RevokeSessionByRefreshHash(ctx, hashToken(input.RefreshToken))
}

func (s *Service) AuthenticateBearer(ctx context.Context, bearerToken string) (CurrentUser, error) {
	bearerToken = strings.TrimSpace(bearerToken)
	if bearerToken == "" {
		return CurrentUser{}, ErrUnauthorized
	}

	current, err := parseAccessToken(s.cfg.JWTSecret, bearerToken)
	if err == nil {
		user, err := s.repo.GetUserByID(ctx, current.ID)
		if err != nil {
			return CurrentUser{}, err
		}
		return CurrentUser{ID: user.ID, Username: user.Username, Role: user.Role, Status: user.Status}, nil
	}

	if s.env == "prod" {
		return CurrentUser{}, ErrUnauthorized
	}

	user, devErr := s.repo.GetUserByDevTokenHash(ctx, hashToken(bearerToken))
	if devErr != nil {
		return CurrentUser{}, ErrUnauthorized
	}
	return CurrentUser{ID: user.ID, Username: user.Username, Role: user.Role, Status: user.Status}, nil
}

func (s *Service) GetUser(ctx context.Context, id int64) (User, error) {
	return s.repo.GetUserByID(ctx, id)
}

func (s *Service) UpdateProfile(ctx context.Context, current CurrentUser, input UpdateProfileInput) (User, error) {
	if current.ID <= 0 {
		return User{}, ErrUnauthorized
	}
	if current.Status != "active" {
		return User{}, ErrForbidden
	}
	if input.Nickname != nil && len(strings.TrimSpace(*input.Nickname)) > 64 {
		return User{}, ValidationError{Field: "nickname", Message: "must be at most 64 characters"}
	}
	if input.AvatarURL != nil && len(strings.TrimSpace(*input.AvatarURL)) > 512 {
		return User{}, ValidationError{Field: "avatar_url", Message: "must be at most 512 characters"}
	}
	if input.Bio != nil && len(strings.TrimSpace(*input.Bio)) > 500 {
		return User{}, ValidationError{Field: "bio", Message: "must be at most 500 characters"}
	}
	return s.repo.UpdateUserProfile(ctx, current.ID, input)
}

func (s *Service) issueTokens(ctx context.Context, user User, userAgent string, ipAddress string) (AuthResponse, error) {
	accessToken, expiresIn, err := newAccessToken(s.cfg.JWTSecret, s.cfg.AccessTokenTTL, user)
	if err != nil {
		return AuthResponse{}, err
	}

	refreshToken, err := randomToken(32)
	if err != nil {
		return AuthResponse{}, err
	}
	if _, err := s.repo.CreateSession(ctx, user.ID, hashToken(refreshToken), userAgent, ipAddress, time.Now().Add(s.cfg.RefreshTokenTTL)); err != nil {
		return AuthResponse{}, err
	}

	return AuthResponse{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
	}, nil
}

func hashPassword(password string, cost int) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateUsername(value string) error {
	if !usernamePattern.MatchString(value) {
		return ValidationError{Field: "username", Message: "must be 3-32 characters and contain only letters, numbers, or underscores"}
	}
	return nil
}

func validatePassword(value string) error {
	if len(value) < 8 {
		return ValidationError{Field: "password", Message: "must be at least 8 characters"}
	}
	if len(value) > 128 {
		return ValidationError{Field: "password", Message: "must be at most 128 characters"}
	}
	return nil
}
