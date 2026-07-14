package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateUserWithCredentials(ctx context.Context, username string, passwordHash string, nickname string) (User, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin register user: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	user, err := scanUser(tx.QueryRowxContext(ctx, `
INSERT INTO users (username, nickname, role, status, created_at, updated_at)
VALUES ($1, NULLIF($2, ''), 'normal', 'active', now(), now())
RETURNING `+userSelectColumns(),
		username,
		nickname,
	))
	if err != nil {
		return User{}, mapDBError(err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_credentials (user_id, username, password_hash, password_algo, created_at, updated_at)
VALUES ($1, $2, $3, 'bcrypt', now(), now())`,
		user.ID,
		username,
		passwordHash,
	); err != nil {
		return User{}, mapDBError(err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit register user: %w", err)
	}
	return user, nil
}

func (r *Repository) GetCredentialByUsername(ctx context.Context, username string) (credentialRecord, User, error) {
	var record struct {
		credentialRecord
		User
	}

	err := r.db.QueryRowxContext(ctx, `
SELECT
  c.user_id, c.username, c.password_hash, c.password_algo,
  `+prefixedUserSelectColumns("u")+`
FROM user_credentials c
JOIN users u ON u.id = c.user_id
WHERE c.username = $1`,
		username,
	).Scan(
		&record.credentialRecord.UserID,
		&record.credentialRecord.Username,
		&record.credentialRecord.PasswordHash,
		&record.credentialRecord.PasswordAlgo,
		&record.User.ID,
		&record.User.Username,
		&record.User.Nickname,
		&record.User.AvatarURL,
		&record.User.Bio,
		&record.User.Role,
		&record.User.Status,
		&record.User.CreatedAt,
		&record.User.UpdatedAt,
	)
	if err != nil {
		return credentialRecord{}, User{}, mapDBError(err)
	}
	return record.credentialRecord, record.User, nil
}

func (r *Repository) GetUserByID(ctx context.Context, id int64) (User, error) {
	user, err := scanUser(r.db.QueryRowxContext(ctx, `
SELECT `+userSelectColumns()+`
FROM users
WHERE id = $1`,
		id,
	))
	if err != nil {
		return User{}, mapDBError(err)
	}
	return user, nil
}

func (r *Repository) GetUserByDevTokenHash(ctx context.Context, tokenHash string) (User, error) {
	user, err := scanUser(r.db.QueryRowxContext(ctx, `
SELECT `+prefixedUserSelectColumns("u")+`
FROM user_auth_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND t.status = 'active'
  AND (t.expired_at IS NULL OR t.expired_at > now())`,
		tokenHash,
	))
	if err != nil {
		return User{}, mapDBError(err)
	}
	return user, nil
}

func (r *Repository) UpdateUserProfile(ctx context.Context, id int64, input UpdateProfileInput) (User, error) {
	current, err := r.GetUserByID(ctx, id)
	if err != nil {
		return User{}, err
	}

	nickname := current.Nickname
	avatarURL := current.AvatarURL
	bio := current.Bio
	if input.Nickname != nil {
		nickname = strings.TrimSpace(*input.Nickname)
	}
	if input.AvatarURL != nil {
		avatarURL = strings.TrimSpace(*input.AvatarURL)
	}
	if input.Bio != nil {
		bio = strings.TrimSpace(*input.Bio)
	}

	user, err := scanUser(r.db.QueryRowxContext(ctx, `
UPDATE users
SET nickname = NULLIF($2, ''), avatar_url = NULLIF($3, ''), bio = NULLIF($4, ''), updated_at = now()
WHERE id = $1
RETURNING `+userSelectColumns(),
		id,
		nickname,
		avatarURL,
		bio,
	))
	if err != nil {
		return User{}, mapDBError(err)
	}
	return user, nil
}

func (r *Repository) CreateSession(ctx context.Context, userID int64, refreshTokenHash string, userAgent string, ipAddress string, expiresAt time.Time) (sessionRecord, error) {
	var session sessionRecord
	err := r.db.QueryRowxContext(ctx, `
INSERT INTO user_sessions (user_id, refresh_token_hash, user_agent, ip_address, revoked, expires_at, created_at, updated_at)
VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), FALSE, $5, now(), now())
RETURNING id, user_id, refresh_token_hash, revoked, expires_at`,
		userID,
		refreshTokenHash,
		userAgent,
		ipAddress,
		expiresAt,
	).Scan(&session.ID, &session.UserID, &session.RefreshTokenHash, &session.Revoked, &session.ExpiresAt)
	if err != nil {
		return sessionRecord{}, mapDBError(err)
	}
	return session, nil
}

func (r *Repository) GetSessionByRefreshHash(ctx context.Context, refreshTokenHash string) (sessionRecord, error) {
	var session sessionRecord
	err := r.db.QueryRowxContext(ctx, `
SELECT id, user_id, refresh_token_hash, revoked, expires_at
FROM user_sessions
WHERE refresh_token_hash = $1`,
		refreshTokenHash,
	).Scan(&session.ID, &session.UserID, &session.RefreshTokenHash, &session.Revoked, &session.ExpiresAt)
	if err != nil {
		return sessionRecord{}, mapDBError(err)
	}
	return session, nil
}

func (r *Repository) RevokeSessionByRefreshHash(ctx context.Context, refreshTokenHash string) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE user_sessions
SET revoked = TRUE, updated_at = now()
WHERE refresh_token_hash = $1`,
		refreshTokenHash,
	)
	if err != nil {
		return mapDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked session rows affected: %w", err)
	}
	if affected == 0 {
		return ErrUnauthorized
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func userSelectColumns() string {
	return `id, username, COALESCE(nickname, ''), COALESCE(avatar_url, ''), COALESCE(bio, ''), role, status, created_at, updated_at`
}

func prefixedUserSelectColumns(prefix string) string {
	return prefix + `.id, ` + prefix + `.username, COALESCE(` + prefix + `.nickname, ''), COALESCE(` + prefix + `.avatar_url, ''), COALESCE(` + prefix + `.bio, ''), ` + prefix + `.role, ` + prefix + `.status, ` + prefix + `.created_at, ` + prefix + `.updated_at`
}

func scanUser(row scanner) (User, error) {
	var user User
	if err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Nickname,
		&user.AvatarURL,
		&user.Bio,
		&user.Role,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
	); err != nil {
		return User{}, err
	}
	return user, nil
}

func rollbackUnlessCommitted(tx *sqlx.Tx) {
	_ = tx.Rollback()
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrUsernameExists
		}
	}

	return err
}
