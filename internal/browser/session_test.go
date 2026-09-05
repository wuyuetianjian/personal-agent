package browser

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestInMemorySessionManagerAttachEnforcesPolicy(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	manager := NewInMemorySessionManager()
	metadata, err := manager.Create(Session{
		ID:              "session-1",
		ProfileDir:      "/Users/alice/profile",
		Domains:         []string{"example.com"},
		ExpiresAt:       now.Add(time.Hour),
		ReusePolicy:     ReuseWhenAllowed,
		IsolationPolicy: IsolationPerDomain,
		StoragePolicy:   StorageLocalOnly,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if metadata.StoragePolicy != StorageLocalOnly {
		t.Fatalf("StoragePolicy = %q, want %q", metadata.StoragePolicy, StorageLocalOnly)
	}
	if _, ok := reflect.TypeOf(metadata).FieldByName("ProfileDir"); ok {
		t.Fatal("SessionMetadata must not expose ProfileDir")
	}

	if _, err := manager.Attach("session-1", "https://docs.example.com/path", now); err != nil {
		t.Fatalf("Attach() allowed subdomain error = %v", err)
	}

	if _, err := manager.Attach("session-1", "https://other.example.net", now); !errors.Is(err, ErrDomainNotAllowed) {
		t.Fatalf("Attach() outside allowlist error = %v, want %v", err, ErrDomainNotAllowed)
	}

	if _, err := manager.Attach("session-1", "https://example.com", now.Add(2*time.Hour)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Attach() expired error = %v, want %v", err, ErrSessionExpired)
	}
}

func TestInMemorySessionManagerRejectsNonLocalStorage(t *testing.T) {
	manager := NewInMemorySessionManager()
	_, err := manager.Create(Session{
		ID:            "session-1",
		StoragePolicy: StoragePolicy("remote"),
	})
	if !errors.Is(err, ErrNonLocalStorage) {
		t.Fatalf("Create() error = %v, want %v", err, ErrNonLocalStorage)
	}
}
