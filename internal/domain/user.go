package domain

import (
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           int64     `db:"id" json:"id"`
	Username     string    `db:"username" json:"username"`
	PasswordHash string    `db:"password_hash" json:"-"`
	Email        string    `db:"email" json:"email"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

func (u *User) CheckPassword(password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	return err == nil
}

// DummyPasswordHash is a pre-computed bcrypt hash (cost 10) used to equalize comparison timing
// when a requested username does not exist, mitigating timing-based user enumeration attacks.
const DummyPasswordHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// CheckDummyPassword executes a bcrypt comparison against DummyPasswordHash to burn the same
// CPU time as a valid user password check.
func CheckDummyPassword(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(DummyPasswordHash), []byte(password))
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

