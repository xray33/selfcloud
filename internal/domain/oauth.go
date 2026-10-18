package domain

import (
	"time"
)

type OAuthAuthCode struct {
	Code                string    `db:"code" json:"code"`
	ClientID            string    `db:"client_id" json:"client_id"`
	UserID              int64     `db:"user_id" json:"user_id"`
	RedirectURI         string    `db:"redirect_uri" json:"redirect_uri"`
	CodeChallenge       string    `db:"code_challenge" json:"code_challenge"`
	CodeChallengeMethod string    `db:"code_challenge_method" json:"code_challenge_method"`
	Scope               string    `db:"scope" json:"scope"`
	ExpiresAt           time.Time `db:"expires_at" json:"expires_at"`
	CreatedAt           time.Time `db:"created_at" json:"created_at"`
}

func (c *OAuthAuthCode) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

type OAuthToken struct {
	ID                    int64     `db:"id" json:"id"`
	ClientID              string    `db:"client_id" json:"client_id"`
	UserID                int64     `db:"user_id" json:"user_id"`
	AccessToken           string    `db:"access_token" json:"access_token"`
	RefreshToken          string    `db:"refresh_token" json:"refresh_token"`
	AccessTokenExpiresAt  time.Time `db:"access_token_expires_at" json:"access_token_expires_at"`
	RefreshTokenExpiresAt time.Time `db:"refresh_token_expires_at" json:"refresh_token_expires_at"`
	CreatedAt             time.Time `db:"created_at" json:"created_at"`
}

func (t *OAuthToken) IsAccessTokenExpired() bool {
	return time.Now().After(t.AccessTokenExpiresAt)
}

func (t *OAuthToken) IsRefreshTokenExpired() bool {
	return time.Now().After(t.RefreshTokenExpiresAt)
}
