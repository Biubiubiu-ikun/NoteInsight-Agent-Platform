package auth

import "time"

type User struct {
	ID        int64     `json:"id" db:"id"`
	Username  string    `json:"username" db:"username"`
	Nickname  string    `json:"nickname" db:"nickname"`
	AvatarURL string    `json:"avatar_url" db:"avatar_url"`
	Bio       string    `json:"bio" db:"bio"`
	Role      string    `json:"role" db:"role"`
	Status    string    `json:"status" db:"status"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type CurrentUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

type AuthResponse struct {
	User         User   `json:"user"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
}

type RegisterInput struct {
	Username  string
	Password  string
	Nickname  string
	UserAgent string
	IPAddress string
}

type LoginInput struct {
	Username  string
	Password  string
	UserAgent string
	IPAddress string
}

type RefreshInput struct {
	RefreshToken string
	UserAgent    string
	IPAddress    string
}

type LogoutInput struct {
	RefreshToken string
}

type UpdateProfileInput struct {
	Nickname  *string
	AvatarURL *string
	Bio       *string
}

type credentialRecord struct {
	UserID       int64  `db:"user_id"`
	Username     string `db:"username"`
	PasswordHash string `db:"password_hash"`
	PasswordAlgo string `db:"password_algo"`
}

type sessionRecord struct {
	ID               int64     `db:"id"`
	UserID           int64     `db:"user_id"`
	RefreshTokenHash string    `db:"refresh_token_hash"`
	Revoked          bool      `db:"revoked"`
	ExpiresAt        time.Time `db:"expires_at"`
}
