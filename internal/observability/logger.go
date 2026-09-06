package observability

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

type LogFields struct {
	TaskID        string
	WorkflowID    string
	NodeID        string
	Agent         string
	Capability    string
	Provider      string
	Duration      time.Duration
	Status        string
	ErrorCategory string
}

type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

func NewLogger(out io.Writer) *Logger {
	return &Logger{out: out}
}

func (l *Logger) Log(message string, fields LogFields) error {
	if l == nil || l.out == nil {
		return nil
	}
	record := map[string]any{
		"message":        Redact(message),
		"task_id":        Redact(fields.TaskID),
		"workflow_id":    Redact(fields.WorkflowID),
		"node_id":        Redact(fields.NodeID),
		"agent":          Redact(fields.Agent),
		"capability":     Redact(fields.Capability),
		"provider":       Redact(fields.Provider),
		"duration_ms":    fields.Duration.Milliseconds(),
		"status":         Redact(fields.Status),
		"error_category": Redact(fields.ErrorCategory),
		"recorded_at":    time.Now().UTC().Format(time.RFC3339),
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	encoder := json.NewEncoder(l.out)
	return encoder.Encode(record)
}
