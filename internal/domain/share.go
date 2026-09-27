package domain

import (
	"time"
)

type Share struct {
	ID        int64      `db:"id" json:"id"`
	Token     string     `db:"token" json:"token"`
	UserID    int64      `db:"user_id" json:"user_id"`
	Path      string     `db:"path" json:"path"`
	IsDir     bool       `db:"is_dir" json:"is_dir"`
	ExpiresAt *time.Time `db:"expires_at" json:"expires_at,omitempty"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
}

func (s *Share) IsExpired() bool {
	if s.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*s.ExpiresAt)
}
