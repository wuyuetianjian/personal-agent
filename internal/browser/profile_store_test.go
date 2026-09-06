package browser

import (
	"errors"
	"testing"
	"time"
)

func TestProfileStoreBuildsLocalSessionFromEnv(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	t.Setenv("PACHAT_TEST_PROFILE", "/tmp/profile")

	store := ProfileStore{
		ProfileDirEnv:  "PACHAT_TEST_PROFILE",
		AllowedDomains: []string{"example.com"},
		SessionTTL:     time.Hour,
		Now:            func() time.Time { return now },
	}

	session, err := store.Session("session-1")
	if err != nil {
		t.Fatalf("Session() error = %v", err)
	}
	if session.ProfileDir != "/tmp/profile" {
		t.Fatalf("ProfileDir = %q, want env value", session.ProfileDir)
	}
	if session.StoragePolicy != StorageLocalOnly {
		t.Fatalf("StoragePolicy = %q, want local only", session.StoragePolicy)
	}
	if !session.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %v, want %v", session.ExpiresAt, now.Add(time.Hour))
	}
}

func TestProfileStoreRequiresEnvValue(t *testing.T) {
	store := ProfileStore{ProfileDirEnv: "PACHAT_MISSING_PROFILE"}
	_, err := store.Session("session-1")
	if !errors.Is(err, ErrProfileDirUnavailable) {
		t.Fatalf("Session() error = %v, want %v", err, ErrProfileDirUnavailable)
	}
}
