package interaction

import (
	"context"
	"testing"
)

func TestHooksSessionSignalAfterCommit(t *testing.T) {
	s, id := admitHookRun(t, "")
	calls := 0
	foreign := 0
	activeOwners.Lock()
	activeOwners.cancels[id] = func() {
		var revoked int
		if e := s.db.QueryRow(`SELECT revoked FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&revoked); e != nil || revoked != 1 {
			t.Error("signal before durable commit", e, revoked)
		}
		calls++
	}
	activeOwners.cancels["other-owner"] = func() { foreign++ }
	activeOwners.Unlock()
	defer func() {
		activeOwners.Lock()
		delete(activeOwners.cancels, id)
		delete(activeOwners.cancels, "other-owner")
		activeOwners.Unlock()
	}()
	s.SignalRevokedSessionSkillRuns(context.Background(), "s")
	if calls != 0 {
		t.Fatal("unrevoked signal")
	}
	hookEnable(t, s, hookPolicyExample)
	s.SignalRevokedSessionSkillRuns(context.Background(), "foreign")
	if calls != 0 || foreign != 0 {
		t.Fatal("foreign signal")
	}
	s.SignalRevokedSessionSkillRuns(context.Background(), "s")
	if calls != 1 || foreign != 0 {
		t.Fatal(calls, foreign)
	}
	var cancel int
	s.db.QueryRow(`SELECT cancel_requested FROM interaction_engine WHERE run_id=?`, id).Scan(&cancel)
	if cancel != 0 {
		t.Fatal("signal changed durable cancellation", cancel)
	}
}
