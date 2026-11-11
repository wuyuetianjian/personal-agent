package interaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent/internal/model"
	"agent/internal/runtime"
	"agent/internal/storage"
)

type streamFunc func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error)

func (f streamFunc) Stream(c context.Context, r model.TurnRequest, e func(model.ModelEvent) error) (model.TurnResult, error) {
	return f(c, r, e)
}

type testBroker struct {
	approval   bool
	executions atomic.Int32
	execute    func(context.Context) (ToolResult, error)
}

func (b *testBroker) Propose(c context.Context, r string, call ToolCall) (ToolProposal, error) {
	return ToolProposal{ID: r + ":" + call.ID, Call: call, RequiresApproval: b.approval, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (b *testBroker) Execute(c context.Context, id string) (ToolResult, error) {
	b.executions.Add(1)
	if b.execute != nil {
		return b.execute(c)
	}
	return ToolResult{Content: "observed"}, nil
}
func engineFixture(t *testing.T, p model.StreamProvider, b ToolBroker) (*Engine, *Store, string) {
	t.Helper()
	s, c := setup(t)
	r, err := s.CreateRun(c, "s", "request", Message{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(s, p, b, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return e, s, r.ID
}
func toolTurn() model.TurnResult {
	return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "c1", Name: "read", Arguments: `{"x":1}`}}, Usage: &model.Usage{InputTokens: 1, OutputTokens: 1}}
}
func TestEngineTextOnly(t *testing.T) {
	e, s, id := engineFixture(t, streamFunc(func(c context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		if err := emit(model.ModelEvent{Kind: model.EventTextDelta, Text: "answer"}); err != nil {
			return model.TurnResult{}, err
		}
		return model.TurnResult{Content: "answer"}, nil
	}), nil)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetRun(context.Background(), id)
	cp, err := e.Checkpoint(context.Background(), id)
	if err != nil || r.State != RunCompleted || len(cp.Messages) != 2 || cp.Messages[1].Content != "answer" {
		t.Fatalf("%+v %+v %v", r, cp, err)
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM side_effect_records").Scan(&n); err != nil || n != 0 {
		t.Fatalf("side effects %d %v", n, err)
	}
	ev, _ := s.Events(context.Background(), "s", 0, 100)
	found := false
	for _, v := range ev {
		if v.Type == "model.text_delta" {
			found = true
		}
	}
	if !found {
		t.Fatal("no incremental delta")
	}
}
func TestEngineToolRoundProtocol(t *testing.T) {
	turns := 0
	b := &testBroker{}
	e, _, id := engineFixture(t, streamFunc(func(c context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		turns++
		if turns == 1 {
			return toolTurn(), nil
		}
		if len(r.Messages) != 3 || len(r.Messages[1].ToolCalls) != 1 || r.Messages[2].ToolCallID != "c1" || r.Messages[2].Content != "observed" {
			t.Fatalf("invalid protocol: %+v", r.Messages)
		}
		return model.TurnResult{Content: "done"}, nil
	}), b)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if b.executions.Load() != 1 {
		t.Fatal("execution missing")
	}
}
func TestEngineBudgetStopsBeforeTool(t *testing.T) {
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), &testBroker{})
	e.options.Budgets.MaxSteps = 1
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM side_effect_records").Scan(&n)
	if n != 0 {
		t.Fatal("tool started after budget")
	}
}
func TestEngineApprovalResume(t *testing.T) {
	turn := 0
	b := &testBroker{approval: true}
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		turn++
		if turn == 1 {
			return toolTurn(), nil
		}
		return model.TurnResult{Content: "done"}, nil
	}), b)
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(context.Background(), id)
	if cp.Phase != PhaseWaitingApproval || b.executions.Load() != 0 {
		t.Fatal(cp)
	}
	if _, err := s.CreateRun(context.Background(), "s", "another", Message{}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	p := cp.Pending[0]
	if err := s.DecideApproval(context.Background(), p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetApproval(context.Background(), p.ID)
	if a.ConsumedAt == nil || b.executions.Load() != 1 {
		t.Fatalf("%+v", a)
	}
}
func TestEngineCancelWaitsForExecutorAndSingleOwner(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	b := &testBroker{execute: func(c context.Context) (ToolResult, error) {
		close(entered)
		<-release
		return ToolResult{Content: "finished"}, nil
	}}
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), b)
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background(), id) }()
	<-entered
	other, _ := NewEngine(s, e.provider, b, EngineOptions{})
	if err := other.Run(context.Background(), id); !errors.Is(err, ErrOwned) {
		t.Fatal(err)
	}
	if err := e.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetRun(context.Background(), id)
	if r.State != RunRunning {
		t.Fatal("terminal before executor returned")
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r, _ = s.GetRun(context.Background(), id)
	if r.State != RunCancelled || b.executions.Load() != 1 {
		t.Fatal(r)
	}
}

func TestEngineRecoverCrashCheckpoints(t *testing.T) {
	for _, stage := range []string{"before_approval", "before_execution", "started", "completed_journal"} {
		t.Run(stage, func(t *testing.T) {
			b := &testBroker{approval: true}
			turn := 0
			e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				turn++
				if turn == 1 {
					return toolTurn(), nil
				}
				return model.TurnResult{Content: "recovered"}, nil
			}), b)
			if err := e.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, _ := e.Checkpoint(context.Background(), id)
			p := cp.Pending[0]
			if stage != "before_approval" {
				if err := s.DecideApproval(context.Background(), p.ID, p.Digest, "approved"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "started" || stage == "completed_journal" {
				if err := s.ConsumeApproval(context.Background(), p.ID, p.Digest); err != nil {
					t.Fatal(err)
				}
				cp.Phase = PhaseExecuting
				if err := e.save(context.Background(), mustRun(t, s, id), "", &cp, "", nil); err != nil {
					t.Fatal(err)
				}
				j := runtime.SQLiteSideEffectStore{DB: s.db}
				if _, _, err := j.Claim(context.Background(), runtime.SideEffectRecord{IdempotencyKey: effectKey(id, p.ID), WorkflowID: id, NodeID: p.ID, CapabilityID: p.Call.Name}); err != nil {
					t.Fatal(err)
				}
				if stage == "completed_journal" {
					if err := j.Complete(context.Background(), effectKey(id, p.ID), `{"Content":"saved result"}`); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.db.Exec("UPDATE interaction_engine SET owner='dead-process' WHERE run_id=?", id); err != nil {
				t.Fatal(err)
			}
			var seq int
			var dbName, dbPath string
			if err := s.db.QueryRow("PRAGMA database_list").Scan(&seq, &dbName, &dbPath); err != nil {
				t.Fatal(err)
			}
			if err := s.db.Close(); err != nil {
				t.Fatal(err)
			}
			disk, err := storage.OpenSQLite(context.Background(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer disk.Close()
			reopened := NewStore(disk.SQL)
			s = reopened
			recovered, _ := NewEngine(reopened, e.provider, b, EngineOptions{})
			err = recovered.Recover(context.Background(), id)
			if stage == "started" {
				if !errors.Is(err, ErrReconciliation) {
					t.Fatal(err)
				}
				if b.executions.Load() != 0 {
					t.Fatal("repeated uncertain action")
				}
				if _, err = s.CreateRun(context.Background(), "s", "second", Message{}); !errors.Is(err, ErrConflict) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before_approval" {
				if err = recovered.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
					t.Fatal(err)
				}
				return
			}
			if err = recovered.Run(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			expected := int32(1)
			if stage == "completed_journal" {
				expected = 0
			}
			if b.executions.Load() != expected {
				t.Fatal("repeated execution", b.executions.Load())
			}
			after, _ := recovered.Checkpoint(context.Background(), id)
			if after.Phase != PhaseCompleted {
				t.Fatal(after)
			}
		})
	}
}
func mustRun(t *testing.T, s *Store, id string) Run {
	t.Helper()
	r, err := s.GetRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestEngineRecoverRefusesLiveOwner(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	e, _, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		close(entered)
		<-release
		return model.TurnResult{Content: "ok"}, nil
	}), nil)
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background(), id) }()
	<-entered
	if err := e.Recover(context.Background(), id); !errors.Is(err, ErrOwned) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestEngineDeniedExpiredAndCancelledApproval(t *testing.T) {
	for _, kind := range []string{"denied", "expired", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			b := &testBroker{approval: true}
			e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				return toolTurn(), nil
			}), b)
			if err := e.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, _ := e.Checkpoint(context.Background(), id)
			p := cp.Pending[0]
			switch kind {
			case "denied":
				if err := s.DecideApproval(context.Background(), p.ID, p.Digest, "denied"); err != nil {
					t.Fatal(err)
				}
			case "expired":
				cp.Pending[0].ExpiresAt = time.Now().Add(-time.Second)
				s.db.Exec("DELETE FROM interaction_approvals WHERE id=?", p.ID)
				if err := e.save(context.Background(), mustRun(t, s, id), "", &cp, "", nil); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				if err := e.Cancel(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Run(context.Background(), id); err == nil {
				t.Fatal("expected rejection")
			}
			if b.executions.Load() != 0 {
				t.Fatal("executed unapproved")
			}
		})
	}
}
func TestEngineUnknownUsageReservation(t *testing.T) {
	b := &testBroker{}
	e, _, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		r := toolTurn()
		r.Usage = nil
		return r, nil
	}), b)
	e.options.Budgets.MaxTokens = 100
	e.options.Budgets.MaxOutputTokens = 99
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	if b.executions.Load() != 0 {
		t.Fatal("tool after reservation failed")
	}
}
func TestProposalDigestCanonicalAndBound(t *testing.T) {
	a, _ := ProposalDigest("run", ToolCall{ID: "c", Name: "write", Arguments: `{"b":2,"a":1}`})
	b, _ := ProposalDigest("run", ToolCall{ID: "c", Name: "write", Arguments: `{ "a":1, "b":2 }`})
	c, _ := ProposalDigest("other", ToolCall{ID: "c", Name: "write", Arguments: `{"b":2,"a":1}`})
	if a == "" || a != b || a == c {
		t.Fatalf("%s %s %s", a, b, c)
	}
}

func TestEngineResumeUsesImmutableRequest(t *testing.T) {
	b := &testBroker{approval: true}
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), b)
	e.options.Request.Model.Model = "original"
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(context.Background(), id)
	p := cp.Pending[0]
	if err := s.DecideApproval(context.Background(), p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	replacement, _ := NewEngine(s, streamFunc(func(c context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		if r.Model.Model != "original" {
			t.Fatalf("request mutated on resume: %s", r.Model.Model)
		}
		return model.TurnResult{Content: "done"}, nil
	}), b, EngineOptions{Request: model.TurnRequest{ChatRequest: model.ChatRequest{Model: model.ModelMetadata{Model: "different"}}}})
	if err := replacement.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
func TestEngineFallbackOutputBudgetAcrossTurns(t *testing.T) {
	turn := 0
	e, _, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		turn++
		if turn == 1 {
			r := toolTurn()
			r.Content = "123456"
			return r, nil
		}
		return model.TurnResult{Content: "123456"}, nil
	}), &testBroker{})
	e.options.Budgets.MaxOutputBytes = 10
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrBudget) {
		t.Fatalf("expected cumulative output bound, got %v", err)
	}
}
func TestEngineAllEventsBounded(t *testing.T) {
	e, _, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), &testBroker{})
	e.options.Budgets.MaxEvents = 1
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrBudget) {
		t.Fatalf("expected event bound, got %v", err)
	}
}
func TestEngineNoBrokerFailsClosed(t *testing.T) {
	e, _, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), nil)
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrNoBroker) {
		t.Fatal(err)
	}
}

func TestStoreSessionTranscript(t *testing.T) {
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return model.TurnResult{Content: "answer"}, nil
	}), nil)
	e.options.Request.Messages = []model.ChatMessage{{Role: "system", Content: "system context"}}
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	messages, err := s.SessionTranscript(context.Background(), "s")
	if err != nil || len(messages) != 2 || messages[0].Role != "user" || messages[1].Content != "answer" {
		t.Fatalf("%+v %v", messages, err)
	}
}

func TestEngineCancelBeforeStart(t *testing.T) {
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		t.Fatal("model after cancellation")
		return model.TurnResult{}, nil
	}), nil)
	if err := e.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	err := e.Run(context.Background(), id)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r := mustRun(t, s, id); r.State != RunCancelled {
		t.Fatal(r)
	}
}
func TestEngineCancelRejectsApprovalConsumption(t *testing.T) {
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), &testBroker{approval: true})
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(context.Background(), id)
	p := cp.Pending[0]
	if err := s.DecideApproval(context.Background(), p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err := e.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeApproval(context.Background(), p.ID, p.Digest); !errors.Is(err, ErrConflict) {
		t.Fatalf("consumed after cancel: %v", err)
	}
}
func TestEngineDurationAndConcurrencyLimits(t *testing.T) {
	t.Run("concurrency while a provider is held", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		e, s, id := engineFixture(t, streamFunc(func(c context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
			entered <- struct{}{}
			<-c.Done()
			return model.TurnResult{}, c.Err()
		}), nil)
		e.options.Budgets.MaxConcurrent = 1
		// Prepare both durable runs before starting the clock. On Windows the
		// SQLite writes can take longer than a deliberately short duration limit.
		if err := s.CreateSession(context.Background(), "s2"); err != nil {
			t.Fatal(err)
		}
		r, err := s.CreateRun(context.Background(), "s2", "r2", Message{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- e.Run(ctx, id) }()
		select {
		case <-entered:
		case err := <-done:
			t.Fatal("first run never entered provider", err)
		}
		attempt, stopAttempt := context.WithTimeout(ctx, 5*time.Second)
		capacityErr := e.Run(attempt, r.ID)
		stopAttempt()
		cancel()
		firstErr := <-done
		if !errors.Is(capacityErr, ErrCapacity) {
			t.Fatal("second run was not refused while first was active", capacityErr)
		}
		if !errors.Is(firstErr, context.Canceled) {
			t.Fatal(firstErr)
		}
		if len(entered) != 0 {
			t.Fatal("capacity refusal called the provider")
		}
	})
	t.Run("duration cancels an active provider", func(t *testing.T) {
		entered := make(chan bool, 1)
		e, _, id := engineFixture(t, streamFunc(func(c context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
			entered <- c.Err() == nil
			<-c.Done()
			return model.TurnResult{}, c.Err()
		}), nil)
		// Allow durable setup on slower native filesystems, then prove that the
		// engine's own deadline reaches a provider that entered with a live context.
		e.options.Budgets.MaxDuration = 5 * time.Second
		if err := e.Run(context.Background(), id); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		select {
		case live := <-entered:
			if !live {
				t.Fatal("provider entered with an already expired context")
			}
		default:
			t.Fatal("deadline expired before the provider was exercised")
		}
	})
}

func TestProposalDigestRejectsAmbiguousArguments(t *testing.T) {
	for _, args := range []string{`{"path":"a","path":"b"}`, `{"nested":{"x":1,"x":2}}`, `{} {}`} {
		if _, err := ProposalDigest("r", ToolCall{ID: "id", Name: "write", Arguments: args}); err == nil {
			t.Fatalf("accepted ambiguous args %s", args)
		}
	}
}
func TestEngineUnknownExecutionFailureReconciles(t *testing.T) {
	b := &testBroker{execute: func(context.Context) (ToolResult, error) {
		return ToolResult{}, errors.New("connection lost after action")
	}}
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return toolTurn(), nil
	}), b)
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(context.Background(), id)
	if cp.Phase != PhaseReconciliation || mustRun(t, s, id).State != RunRunning {
		t.Fatal(cp)
	}
	if err := e.Recover(context.Background(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	if b.executions.Load() != 1 {
		t.Fatal("uncertain execution repeated")
	}
}

func TestEngineTerminalCommitHonorsCancelIntent(t *testing.T) {
	e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return model.TurnResult{}, nil
	}), nil)
	r := mustRun(t, s, id)
	cp, err := e.claim(context.Background(), r, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	cp.Phase = PhaseCompleted
	err = e.finish(context.Background(), r, "owner", &cp, RunCompleted)
	if !errors.Is(err, context.Canceled) || mustRun(t, s, id).State != RunCancelled {
		t.Fatalf("cancellation lost at terminal commit: %v %+v", err, mustRun(t, s, id))
	}
}

func TestEngineCumulativeTokenUsageBound(t *testing.T) {
	turn := 0
	e, _, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		turn++
		if turn == 1 {
			r := toolTurn()
			r.Usage = &model.Usage{InputTokens: 4000}
			return r, nil
		}
		return model.TurnResult{Content: "done", Usage: &model.Usage{InputTokens: 2000}}, nil
	}), &testBroker{})
	e.options.Budgets.MaxTokens = 5000
	e.options.Budgets.MaxOutputTokens = 1
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrBudget) {
		t.Fatalf("over-budget answer accepted: %v", err)
	}
}

// A failed uncertainty checkpoint must not make a consumed approval look like
// an ordinary validation failure on the next invocation.
func TestEngineFailedReconciliationWriteKeepsSessionBlocked(t *testing.T) {
	for _, cancelAfterFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelAfterFailure), func(t *testing.T) {
			outcomeErr := errors.New("external outcome unknown")
			b := &testBroker{approval: true, execute: func(context.Context) (ToolResult, error) { return ToolResult{}, outcomeErr }}
			e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				return toolTurn(), nil
			}), b)
			ctx := context.Background()
			if err := e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, err := e.Checkpoint(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			p := cp.Pending[0]
			if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`CREATE TEMP TRIGGER reject_reconciliation BEFORE UPDATE OF phase ON interaction_engine WHEN NEW.phase='needs_reconciliation' BEGIN SELECT RAISE(ABORT,'reconciliation write rejected'); END;`); err != nil {
				t.Fatal(err)
			}
			err = e.Run(ctx, id)
			if !errors.Is(err, ErrReconciliation) || !strings.Contains(err.Error(), "reconciliation write rejected") {
				t.Fatalf("missing persistence diagnosis: %v", err)
			}
			if !errors.Is(err, outcomeErr) {
				t.Errorf("lost original execution error: %v", err)
			}
			var owner string
			if err = s.db.QueryRow("SELECT owner FROM interaction_engine WHERE run_id=?", id).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner != "" {
				t.Fatal("fixture must exercise successful owner release")
			}
			cp, err = e.Checkpoint(ctx, id)
			if err != nil || cp.Phase != PhaseExecuting {
				t.Fatalf("expected durable executing checkpoint: %+v %v", cp, err)
			}
			if _, err = s.db.Exec("DROP TRIGGER reject_reconciliation"); err != nil {
				t.Fatal(err)
			}
			if cancelAfterFailure {
				if err = e.Cancel(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if err = e.Run(ctx, id); !errors.Is(err, ErrReconciliation) {
				t.Errorf("ordinary Run must require offline recovery: %v", err)
			}
			if b.executions.Load() != 1 {
				t.Error("repeated uncertain execution")
			}
			if r := mustRun(t, s, id); r.State != RunRunning {
				t.Errorf("unknown outcome became terminal: %+v", r)
			}
			if _, err = s.CreateRun(ctx, "s", "next", Message{}); !errors.Is(err, ErrConflict) {
				t.Errorf("session no longer guarded: %v", err)
			}
			if err = e.Recover(ctx, id); !errors.Is(err, ErrReconciliation) {
				t.Errorf("recovery lost uncertainty: %v", err)
			}
			cp, err = e.Checkpoint(ctx, id)
			if err != nil || cp.Phase != PhaseReconciliation {
				t.Errorf("recovery did not persist reconciliation: %+v %v", cp, err)
			}
		})
	}
}

func TestRecoveredToolObservationAtomicWithCheckpointAndCancellation(t *testing.T) {
	for _, variant := range []struct {
		cancel    bool
		failEvent string
	}{{false, "tool.recovered"}, {true, "tool.recovered"}, {true, "run.cancelled"}} {
		t.Run(fmt.Sprintf("cancel=%t/fail=%s", variant.cancel, variant.failEvent), func(t *testing.T) {
			ctx := context.Background()
			b := &testBroker{approval: true}
			e, s, id := engineFixture(t, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				return toolTurn(), nil
			}), b)
			if err := e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, err := e.Checkpoint(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			p := cp.Pending[0]
			if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
				t.Fatal(err)
			}
			if err = s.ConsumeApproval(ctx, p.ID, p.Digest); err != nil {
				t.Fatal(err)
			}
			cp.Phase = PhaseExecuting
			cp.Events = cp.Budgets.MaxEvents // Known recovered results are control observations, even at the ordinary-event cap.
			if err = e.save(ctx, mustRun(t, s, id), "", &cp, "", nil); err != nil {
				t.Fatal(err)
			}
			journal := runtime.SQLiteSideEffectStore{DB: s.db}
			if _, _, err = journal.Claim(ctx, runtime.SideEffectRecord{IdempotencyKey: effectKey(id, p.ID), WorkflowID: id, NodeID: p.ID, CapabilityID: p.Call.Name}); err != nil {
				t.Fatal(err)
			}
			if err = journal.Complete(ctx, effectKey(id, p.ID), `{"Content":"observed exactly once","IsError":false}`); err != nil {
				t.Fatal(err)
			}
			if variant.cancel {
				if err = e.Cancel(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.db.Exec(fmt.Sprintf("CREATE TEMP TRIGGER interrupt_recovery BEFORE INSERT ON interaction_events WHEN NEW.type='%s' BEGIN SELECT RAISE(ABORT,'test recovery commit failure'); END", variant.failEvent)); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(ctx, id); err == nil {
				t.Fatal("recovery did not atomically attempt observation", variant.failEvent)
			}
			stored, err := e.Checkpoint(ctx, id)
			if err != nil || len(stored.Pending) != 1 {
				t.Fatal("checkpoint committed without observation", stored, err)
			}
			if mustRun(t, s, id).State != RunRunning {
				t.Fatal("terminal committed despite failed observation transaction")
			}
			var n int
			if err = s.db.QueryRow("SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.recovered'", id).Scan(&n); err != nil || n != 0 {
				t.Fatal("observation committed without checkpoint", n, err)
			}
			if _, err = s.db.Exec("DROP TRIGGER interrupt_recovery"); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(ctx, id); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRow("SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.recovered'", id).Scan(&n); err != nil || n != 1 {
				t.Fatal("recovery observation not exactly once", n, err)
			}
			if b.executions.Load() != 0 {
				t.Fatal("recovery repeated execution")
			}
			stored, err = e.Checkpoint(ctx, id)
			if err != nil || len(stored.Pending) != 0 {
				t.Fatal(stored, err)
			}
		})
	}
}
