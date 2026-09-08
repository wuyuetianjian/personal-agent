package trigger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type WorkflowStarter interface {
	StartTriggerWorkflow(ctx context.Context, t Trigger) (workflowID string, err error)
}

type ConditionEvaluator interface {
	Evaluate(ctx context.Context, t Trigger, previous bool) (bool, error)
}

type Daemon struct {
	Store     Store
	Starter   WorkflowStarter
	Evaluator ConditionEvaluator
	Now       func() time.Time
}

func (d Daemon) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		_, _ = d.Recover(ctx)
		_, _ = d.Tick(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = d.Tick(ctx)
			}
		}
	}()
}

func (d Daemon) Tick(ctx context.Context) (int, error) {
	now := d.now()
	triggers, err := d.Store.ListEnabled(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, tr := range triggers {
		st, err := d.Store.StateOrDefault(ctx, tr.ID)
		if err != nil {
			return started, err
		}
		if InCooldown(st, now) {
			continue
		}
		due, err := d.isDue(ctx, tr, st, now)
		if err != nil {
			return started, err
		}
		if !due {
			continue
		}
		if err := d.RunNow(ctx, tr.ID); err != nil {
			return started, err
		}
		started++
	}
	return started, nil
}

func (d Daemon) RunNow(ctx context.Context, id string) error {
	tr, err := d.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	now := d.now()
	st, err := d.Store.StateOrDefault(ctx, tr.ID)
	if err != nil {
		return err
	}
	st.LastFiredAt = &now
	if tr.Type == TypeConditionWatch {
		st = MarkConditionNotification(st, now)
	}
	workflowID := ""
	if d.Starter != nil {
		workflowID, err = d.Starter.StartTriggerWorkflow(ctx, tr)
	}
	status := "completed"
	message := "trigger fired"
	if err != nil {
		status = "failed"
		message = err.Error()
		st.LastFailureAt = &now
		st.ConsecutiveFailures++
		letterID, idErr := randomTriggerID("dlq")
		if idErr == nil {
			_ = d.Store.PutDeadLetter(ctx, DeadLetter{ID: letterID, SourceID: tr.ID, SourceType: "trigger", Reason: err.Error(), Payload: []byte(tr.ID)})
		}
	} else {
		tr, st = MarkSuccess(tr, st, now)
	}
	next, nextErr := NextFire(tr, st, now)
	if nextErr == nil {
		st.NextFireAt = next
	}
	if putErr := d.Store.Put(ctx, tr); putErr != nil {
		return putErr
	}
	if putErr := d.Store.PutState(ctx, st); putErr != nil {
		return putErr
	}
	historyID, idErr := randomTriggerID("hist")
	if idErr != nil {
		return idErr
	}
	return d.Store.PutHistory(ctx, History{ID: historyID, TriggerID: tr.ID, WorkflowID: workflowID, Status: status, Message: message})
}

func (d Daemon) Recover(ctx context.Context) (int, error) {
	triggers, err := d.Store.ListEnabled(ctx)
	if err != nil {
		return 0, err
	}
	now := d.now()
	for _, tr := range triggers {
		st, err := d.Store.StateOrDefault(ctx, tr.ID)
		if err != nil {
			return 0, err
		}
		next, err := NextFire(tr, st, now)
		if err != nil {
			return 0, err
		}
		st.NextFireAt = next
		if err := d.Store.PutState(ctx, st); err != nil {
			return 0, err
		}
	}
	return len(triggers), nil
}

func (d Daemon) isDue(ctx context.Context, tr Trigger, st State, now time.Time) (bool, error) {
	if tr.Type == TypeManual || tr.Type == TypeGoal || tr.Type == TypeEvent {
		return false, nil
	}
	if tr.Type == TypeConditionWatch {
		previous := st.CurrentState == "true" || st.StateJSON == `{"condition":true}`
		evaluator := d.Evaluator
		if evaluator == nil {
			evaluator = FalseToTrueEvaluator{}
		}
		current, err := evaluator.Evaluate(ctx, tr, previous)
		st = RecordConditionCheck(st, current, now)
		if putErr := d.Store.PutState(ctx, st); putErr != nil {
			return false, putErr
		}
		if err != nil || !current || previous {
			return false, err
		}
		return true, nil
	}
	next := st.NextFireAt
	if next == nil {
		var err error
		next, err = NextFire(tr, st, now)
		if err != nil {
			return false, err
		}
	}
	return next != nil && !next.After(now), nil
}

func (d Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

type FalseToTrueEvaluator struct{}

func (FalseToTrueEvaluator) Evaluate(ctx context.Context, t Trigger, previous bool) (bool, error) {
	return !previous, ctx.Err()
}

func randomTriggerID(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}
