package domain

import (
	"time"
)

type UserIdentity struct {
	ID             int64     `db:"id" json:"id"`
	UserID         int64     `db:"user_id" json:"user_id"`
	Provider       string    `db:"provider" json:"provider"`
	ProviderUserID string    `db:"provider_user_id" json:"provider_user_id"`
	Email          string    `db:"email" json:"email"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
}
