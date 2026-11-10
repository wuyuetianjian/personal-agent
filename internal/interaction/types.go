package interaction

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrConflict = errors.New("interaction conflict")
var ErrInvalid = errors.New("invalid interaction input")

type RunState string

const (
	RunRunning   RunState = "running"
	RunCompleted RunState = "completed"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
)

type Message struct {
	Content string `json:"content"`
}
type Run struct {
	ID        string   `json:"id"`
	SessionID string   `json:"session_id"`
	RequestID string   `json:"request_id"`
	Message   Message  `json:"message"`
	State     RunState `json:"state"`
	Version   int64    `json:"version"`
}
type Event struct {
	Version   int64           `json:"version"`
	SessionID string          `json:"session_id"`
	RunID     string          `json:"run_id"`
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}
type Approval struct {
	ID           string
	RunID        string
	ActionDigest string
	ExpiresAt    time.Time
	Decision     string
	ConsumedAt   *time.Time
}
