package browser

import (
	"errors"
	"os"
	"time"
)

var ErrProfileDirUnavailable = errors.New("browser profile directory unavailable")

type ProfileStore struct {
	ProfileDirEnv  string
	AllowedDomains []string
	SessionTTL     time.Duration
	Now            func() time.Time
}

func (s ProfileStore) Session(sessionID string) (Session, error) {
	profileDir, ok := envValue(s.ProfileDirEnv)
	if !ok {
		return Session{}, ErrProfileDirUnavailable
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	expiresAt := time.Time{}
	if s.SessionTTL > 0 {
		expiresAt = now.Add(s.SessionTTL)
	}
	return Session{
		ID:              sessionID,
		ProfileDir:      profileDir,
		Domains:         append([]string(nil), s.AllowedDomains...),
		ExpiresAt:       expiresAt,
		ReusePolicy:     ReuseWhenAllowed,
		IsolationPolicy: IsolationPerDomain,
		StoragePolicy:   StorageLocalOnly,
	}, nil
}

func envValue(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	value, ok := os.LookupEnv(name)
	return value, ok && value != ""
}
