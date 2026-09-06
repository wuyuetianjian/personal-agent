package observability

import (
	"context"
	"sync"
	"time"
)

type TraceSpan struct {
	Name       string            `json:"name"`
	TaskID     string            `json:"task_id,omitempty"`
	WorkflowID string            `json:"workflow_id,omitempty"`
	NodeID     string            `json:"node_id,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	StartedAt  string            `json:"started_at"`
	EndedAt    string            `json:"ended_at,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Tracer struct {
	mu    sync.Mutex
	spans []TraceSpan
}

func NewTracer() *Tracer {
	return &Tracer{}
}

func (t *Tracer) Start(ctx context.Context, span TraceSpan) (context.Context, func(error)) {
	if span.StartedAt == "" {
		span.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	span.Name = Redact(span.Name)
	span.TaskID = Redact(span.TaskID)
	span.WorkflowID = Redact(span.WorkflowID)
	span.NodeID = Redact(span.NodeID)
	span.Kind = Redact(span.Kind)
	span.Attributes = sanitizeStringMap(span.Attributes)
	return ctx, func(err error) {
		completed := span
		completed.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err != nil {
			if completed.Attributes == nil {
				completed.Attributes = map[string]string{}
			}
			completed.Attributes["error"] = Redact(err.Error())
		}
		if t == nil {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		t.spans = append(t.spans, completed)
	}
}

func (t *Tracer) Spans() []TraceSpan {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TraceSpan(nil), t.spans...)
}

func sanitizeStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[Redact(key)] = Redact(value)
	}
	return out
}
