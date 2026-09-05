package browser

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrSessionExpired       = errors.New("browser session expired")
	ErrDomainNotAllowed     = errors.New("browser domain not allowed")
	ErrNonLocalStorage      = errors.New("browser session storage must be local only")
	ErrSessionReuseDisabled = errors.New("browser session reuse disabled")
)

type Session struct {
	ID              string
	ProfileDir      string
	Domains         []string
	ExpiresAt       time.Time
	ReusePolicy     ReusePolicy
	IsolationPolicy IsolationPolicy
	StoragePolicy   StoragePolicy
}

type SessionMetadata struct {
	ID              string
	Domains         []string
	ExpiresAt       time.Time
	ReusePolicy     ReusePolicy
	IsolationPolicy IsolationPolicy
	StoragePolicy   StoragePolicy
}

type SessionManager interface {
	Create(session Session) (SessionMetadata, error)
	Attach(sessionID string, rawURL string, now time.Time) (SessionMetadata, error)
	AllowDomain(sessionID string, rawURL string, now time.Time) error
}

type InMemorySessionManager struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewInMemorySessionManager() *InMemorySessionManager {
	return &InMemorySessionManager{sessions: make(map[string]Session)}
}

func (m *InMemorySessionManager) Create(session Session) (SessionMetadata, error) {
	if session.StoragePolicy != StorageLocalOnly {
		return SessionMetadata{}, ErrNonLocalStorage
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return redactedSessionMetadata(session), nil
}

func (m *InMemorySessionManager) Attach(sessionID string, rawURL string, now time.Time) (SessionMetadata, error) {
	m.mu.RLock()
	session, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if !ok {
		return SessionMetadata{}, ErrDomainNotAllowed
	}
	if session.ReusePolicy == ReuseNever {
		return SessionMetadata{}, ErrSessionReuseDisabled
	}
	if isExpired(session, now) {
		return SessionMetadata{}, ErrSessionExpired
	}
	if !domainAllowed(session.Domains, rawURL) {
		return SessionMetadata{}, ErrDomainNotAllowed
	}
	return redactedSessionMetadata(session), nil
}

func (m *InMemorySessionManager) AllowDomain(sessionID string, rawURL string, now time.Time) error {
	_, err := m.Attach(sessionID, rawURL, now)
	return err
}

func redactedSessionMetadata(session Session) SessionMetadata {
	return SessionMetadata{
		ID:              session.ID,
		Domains:         append([]string(nil), session.Domains...),
		ExpiresAt:       session.ExpiresAt,
		ReusePolicy:     session.ReusePolicy,
		IsolationPolicy: session.IsolationPolicy,
		StoragePolicy:   session.StoragePolicy,
	}
}

func isExpired(session Session, now time.Time) bool {
	return !session.ExpiresAt.IsZero() && !now.Before(session.ExpiresAt)
}

func domainAllowed(allowed []string, rawURL string) bool {
	host := rawURL
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, domain := range allowed {
		normalized := strings.ToLower(strings.TrimSpace(domain))
		if host == normalized || strings.HasSuffix(host, "."+normalized) {
			return true
		}
	}
	return false
}
