package domain_test

import (
	"testing"
	"time"

	"selfcloud/internal/domain"
)

func TestUserPasswordHashingAndChecking(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := domain.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	user := &domain.User{
		Username:     "testuser",
		PasswordHash: hash,
	}

	if !user.CheckPassword(password) {
		t.Errorf("CheckPassword returned false for correct password")
	}

	if user.CheckPassword("wrong-password") {
		t.Errorf("CheckPassword returned true for wrong password")
	}
}

func TestCheckDummyPassword(t *testing.T) {
	// Must not panic
	domain.CheckDummyPassword("any_password")
}

func TestSessionIsExpired(t *testing.T) {
	now := time.Now()

	expiredSession := &domain.Session{
		Token:     "tok-expired",
		ExpiresAt: now.Add(-1 * time.Hour),
	}
	if !expiredSession.IsExpired() {
		t.Errorf("expected session to be expired")
	}

	activeSession := &domain.Session{
		Token:     "tok-active",
		ExpiresAt: now.Add(1 * time.Hour),
	}
	if activeSession.IsExpired() {
		t.Errorf("expected session to be active")
	}
}

func TestShareIsExpired(t *testing.T) {
	now := time.Now()

	// 1. Share with no expiration should never expire
	infiniteShare := &domain.Share{
		Token:     "tok-infinite",
		ExpiresAt: nil,
	}
	if infiniteShare.IsExpired() {
		t.Errorf("share without expires_at should not be expired")
	}

	// 2. Share expired in the past
	expiredShare := &domain.Share{
		Token:     "tok-past",
		ExpiresAt: new(now.Add(-1 * time.Hour)),
	}
	if !expiredShare.IsExpired() {
		t.Errorf("share with past expires_at should be expired")
	}

	// 3. Share with expiration in the future
	activeShare := &domain.Share{
		Token:     "tok-future",
		ExpiresAt: new(now.Add(1 * time.Hour)),
	}
	if activeShare.IsExpired() {
		t.Errorf("share with future expires_at should not be expired")
	}
}
