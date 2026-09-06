package orchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"agent/internal/agent"
)

type stubSubAgent struct {
	role agent.Role
	fn   func(context.Context, agent.TaskNode) (agent.Result, error)
}

func (s stubSubAgent) Role() agent.Role {
	return s.role
}

func (s stubSubAgent) Execute(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
	return s.fn(ctx, node)
}

func TestSchedulerRunsIndependentNodesInParallel(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{
		{TaskID: "task-1", ID: "a", Role: agent.RoleResearch},
		{TaskID: "task-1", ID: "b", Role: agent.RoleResearch},
	})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}

	started := make(chan string, 2)
	release := make(chan struct{})
	var concurrent int32
	subAgent := stubSubAgent{role: agent.RoleResearch, fn: func(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
		if atomic.AddInt32(&concurrent, 1) == 2 {
			close(release)
		}
		started <- node.ID
		select {
		case <-release:
		case <-ctx.Done():
			return agent.Result{}, ctx.Err()
		}
		atomic.AddInt32(&concurrent, -1)
		return agent.Result{Text: node.ID, EvidenceIDs: []string{"e-" + node.ID}}, nil
	}}

	result, err := Scheduler{
		Agents:      map[agent.Role]agent.SubAgent{agent.RoleResearch: subAgent},
		Parallelism: 2,
	}.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(started) != 2 {
		t.Fatalf("started nodes = %d, want 2", len(started))
	}
	if !result.Completed["a"] || !result.Completed["b"] {
		t.Fatalf("completed = %v, want a and b", result.Completed)
	}
}

func TestSchedulerPropagatesCancellation(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{{TaskID: "task-1", ID: "a", Role: agent.RoleResearch}})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cancelled := make(chan struct{})
	subAgent := stubSubAgent{role: agent.RoleResearch, fn: func(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return agent.Result{ErrorCategory: agent.ErrorRetryable}, ctx.Err()
	}}

	runDone := make(chan error, 1)
	go func() {
		_, err := Scheduler{
			Agents:      map[agent.Role]agent.SubAgent{agent.RoleResearch: subAgent},
			Parallelism: 1,
		}.Run(ctx, dag)
		runDone <- err
	}()
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("sub-agent did not observe cancellation")
	}
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestSchedulerEarlyStopPreventsAdditionalScheduling(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{
		{TaskID: "task-1", ID: "a", Role: agent.RoleResearch},
		{TaskID: "task-1", ID: "b", Role: agent.RoleResearch, Dependencies: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}
	var calls int32
	subAgent := stubSubAgent{role: agent.RoleResearch, fn: func(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
		atomic.AddInt32(&calls, 1)
		return agent.Result{Text: node.ID}, nil
	}}

	result, err := Scheduler{
		Agents:      map[agent.Role]agent.SubAgent{agent.RoleResearch: subAgent},
		Parallelism: 1,
		EarlyStop: func(results []agent.Result) bool {
			return len(results) == 1
		},
	}.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.EarlyStopped {
		t.Fatal("EarlyStopped = false, want true")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if !result.Cancelled["b"] {
		t.Fatalf("cancelled = %v, want b", result.Cancelled)
	}
}

func TestSchedulerRetriesRetryableResult(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{{TaskID: "task-1", ID: "a", Role: agent.RoleResearch, MaxAttempts: 2}})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}
	var calls int32
	subAgent := stubSubAgent{role: agent.RoleResearch, fn: func(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: "try again"}, errors.New("temporary")
		}
		return agent.Result{Text: "ok"}, nil
	}}

	result, err := Scheduler{
		Agents:      map[agent.Role]agent.SubAgent{agent.RoleResearch: subAgent},
		Parallelism: 1,
	}.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if !result.Completed["a"] {
		t.Fatalf("completed = %v, want a", result.Completed)
	}
}

func TestEvidenceBusPreservesLifecycleOrdering(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{{TaskID: "task-1", ID: "a", Role: agent.RoleResearch}})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}
	bus := &InMemoryEvidenceBus{}
	subAgent := stubSubAgent{role: agent.RoleResearch, fn: func(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
		return agent.Result{EvidenceIDs: []string{"evidence-1"}}, nil
	}}

	_, err = Scheduler{
		Agents:      map[agent.Role]agent.SubAgent{agent.RoleResearch: subAgent},
		EvidenceBus: bus,
		Parallelism: 1,
	}.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	events := bus.Events()
	got := make([]EventType, len(events))
	for i, event := range events {
		got[i] = event.Type
	}
	want := []EventType{EventNodeReady, EventNodeStarted, EventNodeCompleted}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	if len(events[2].EvidenceIDs) != 1 || events[2].EvidenceIDs[0] != "evidence-1" {
		t.Fatalf("completed event evidence = %v, want [evidence-1]", events[2].EvidenceIDs)
	}
}

func TestInMemoryEvidenceBusConcurrentPublishOrdering(t *testing.T) {
	bus := &InMemoryEvidenceBus{}
	for i, eventType := range []EventType{EventNodeReady, EventNodeStarted, EventNodeCompleted} {
		if err := bus.Publish(context.Background(), Event{Type: eventType, Attempt: i + 1}); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}
	events := bus.Events()
	var attempts []int
	for _, event := range events {
		attempts = append(attempts, event.Attempt)
	}
	want := []int{1, 2, 3}
	for i := range want {
		if attempts[i] != want[i] {
			t.Fatalf("attempt order = %v, want %v", attempts, want)
		}
	}
}

func TestSchedulerCancelsDependentsAfterFailure(t *testing.T) {
	dag, err := NewDAG([]agent.TaskNode{
		{TaskID: "task-1", ID: "a", Role: agent.RoleResearch},
		{TaskID: "task-1", ID: "b", Role: agent.RoleResearch, Dependencies: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("NewDAG() error = %v", err)
	}
	subAgent := stubSubAgent{role: agent.RoleResearch, fn: func(ctx context.Context, node agent.TaskNode) (agent.Result, error) {
		return agent.Result{ErrorCategory: agent.ErrorPermissionDenied, ErrorMessage: "denied"}, errors.New("denied")
	}}

	result, err := Scheduler{
		Agents:      map[agent.Role]agent.SubAgent{agent.RoleResearch: subAgent},
		Parallelism: 1,
	}.Run(context.Background(), dag)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Failed["a"] || !result.Cancelled["b"] {
		t.Fatalf("failed=%v cancelled=%v, want a failed and b cancelled", result.Failed, result.Cancelled)
	}
}
