package event

import (
	"errors"
	"time"
)

var ErrInvalidEvent = errors.New("invalid event")

type Event struct {
	ID            string
	Source        string
	Type          string
	ProjectID     string
	Payload       []byte
	PrivacyClass  string
	TrustLevel    string
	OccurredAt    time.Time
	ReceivedAt    time.Time
	DedupKey      string
	CorrelationID string
	CausationID   string
	EventDepth    int
}

func (e Event) Validate() error {
	if e.ID == "" || e.Source == "" || e.Type == "" || e.ProjectID == "" {
		return ErrInvalidEvent
	}
	if e.PrivacyClass == "" || e.TrustLevel == "" {
		return ErrInvalidEvent
	}
	if e.EventDepth < 0 {
		return ErrInvalidEvent
	}
	return nil
}

func Normalize(e Event) Event {
	now := time.Now().UTC()
	if e.OccurredAt.IsZero() {
		e.OccurredAt = now
	}
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = now
	}
	if e.PrivacyClass == "" {
		e.PrivacyClass = "local_private"
	}
	if e.TrustLevel == "" {
		e.TrustLevel = "untrusted"
	}
	return e
}
