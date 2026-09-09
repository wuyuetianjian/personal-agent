package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"agent/internal/agent"
	"agent/internal/observability"
	"agent/internal/orchestrator"
	"agent/internal/runtime"
	"agent/internal/workflow"
)

type streamingEvidenceBus struct {
	next   orchestrator.EvidenceBus
	stdout io.Writer
	mu     sync.Mutex
	seen   map[string]bool
}

func newStreamingEvidenceBus(next orchestrator.EvidenceBus, stdout io.Writer) *streamingEvidenceBus {
	return &streamingEvidenceBus{next: next, stdout: stdout, seen: map[string]bool{}}
}

func (b *streamingEvidenceBus) Publish(ctx context.Context, event orchestrator.Event) error {
	if b.next != nil {
		if err := b.next.Publish(ctx, event); err != nil {
			return err
		}
	}
	if b.stdout == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := streamState(event.Type)
	if state == "" {
		state = observability.Redact(string(event.Type))
	}
	key := string(event.Type) + ":" + event.TaskID + ":" + event.NodeID
	if !b.seen[key] {
		b.seen[key] = true
		fmt.Fprintf(b.stdout, "stream node task_id=%s node_id=%s role=%s state=%s evidence_count=%d\n",
			observability.Redact(event.TaskID),
			observability.Redact(event.NodeID),
			observability.Redact(string(event.Role)),
			state,
			len(event.EvidenceIDs),
		)
	}
	if event.Role == agent.RoleTool || event.Role == agent.RoleBrowser {
		fmt.Fprintf(b.stdout, "stream tool_progress task_id=%s node_id=%s state=%s evidence_count=%d\n",
			observability.Redact(event.TaskID),
			observability.Redact(event.NodeID),
			state,
			len(event.EvidenceIDs),
		)
	}
	if event.ErrorCategory != "" || strings.TrimSpace(event.ErrorMessage) != "" {
		fmt.Fprintf(b.stdout, "stream node_error task_id=%s node_id=%s category=%s message=%s\n",
			observability.Redact(event.TaskID),
			observability.Redact(event.NodeID),
			observability.Redact(string(event.ErrorCategory)),
			observability.Redact(event.ErrorMessage),
		)
	}
	return nil
}

func streamRunStarted(stdout io.Writer, taskID string) {
	fmt.Fprintf(stdout, "stream planning task_id=%s state=started\n", observability.Redact(taskID))
	fmt.Fprintf(stdout, "stream workflow task_id=%s state=pending\n", observability.Redact(taskID))
}

func streamRunFailed(stdout io.Writer, err error) {
	fmt.Fprintf(stdout, "stream planning state=failed error=%s\n", observability.Redact(err.Error()))
}

func streamRunCompleted(ctx context.Context, stdout io.Writer, workflows workflow.Store, taskID string, result *runtime.RunResult) error {
	run, ok := latestWorkflowForTask(ctx, workflows, taskID)
	if ok {
		fmt.Fprintf(stdout, "stream planning task_id=%s workflow_id=%s state=completed\n", observability.Redact(taskID), observability.Redact(run.ID))
		fmt.Fprintf(stdout, "stream workflow task_id=%s workflow_id=%s state=%s\n", observability.Redact(taskID), observability.Redact(run.ID), observability.Redact(string(run.Status)))
		if run.Status == workflow.StatusWaitingApproval {
			fmt.Fprintf(stdout, "stream approval_wait task_id=%s workflow_id=%s state=waiting\n", observability.Redact(taskID), observability.Redact(run.ID))
		} else {
			fmt.Fprintf(stdout, "stream approval_wait task_id=%s workflow_id=%s state=not_required\n", observability.Redact(taskID), observability.Redact(run.ID))
		}
	} else {
		fmt.Fprintf(stdout, "stream planning task_id=%s state=completed\n", observability.Redact(taskID))
		fmt.Fprintf(stdout, "stream workflow task_id=%s state=completed\n", observability.Redact(taskID))
		fmt.Fprintf(stdout, "stream approval_wait task_id=%s state=not_required\n", observability.Redact(taskID))
	}
	fmt.Fprintf(stdout, "stream answer task_id=%s state=started\n", observability.Redact(taskID))
	for _, line := range answerLines(result.Answer) {
		fmt.Fprintf(stdout, "stream answer task_id=%s delta=%s\n", observability.Redact(taskID), observability.Redact(line))
	}
	fmt.Fprintf(stdout, "stream answer task_id=%s state=completed confidence=%.2f remote_tokens=%d\n", observability.Redact(taskID), result.Confidence, result.Usage.RemoteTokens)
	fmt.Fprintf(stdout, "task_id=%s\nstatus=completed\nconfidence=%.2f\nremote_tokens=%d\n\nanswer:\n%s\n",
		taskID, result.Confidence, result.Usage.RemoteTokens, observability.Redact(result.Answer))
	return nil
}

func latestWorkflowForTask(ctx context.Context, store workflow.Store, taskID string) (workflow.Run, bool) {
	runs, err := store.List(ctx, 100)
	if err != nil {
		return workflow.Run{}, false
	}
	for _, run := range runs {
		if run.TaskID == taskID {
			return run, true
		}
	}
	return workflow.Run{}, false
}

func answerLines(answer string) []string {
	parts := strings.Split(answer, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func streamState(eventType orchestrator.EventType) string {
	switch eventType {
	case orchestrator.EventNodeReady:
		return "ready"
	case orchestrator.EventNodeStarted:
		return "running"
	case orchestrator.EventNodeRetrying:
		return "retrying"
	case orchestrator.EventNodeCompleted:
		return "completed"
	case orchestrator.EventNodeFailed:
		return "failed"
	case orchestrator.EventNodeCancelled:
		return "cancelled"
	case orchestrator.EventEarlyStopped:
		return "early_stopped"
	default:
		return ""
	}
}
