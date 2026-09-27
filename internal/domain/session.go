package domain

import (
	"time"
)

type Session struct {
	Token     string    `db:"token" json:"token"`
	UserID    int64     `db:"user_id" json:"user_id"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}
