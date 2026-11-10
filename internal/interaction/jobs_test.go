package interaction

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestJobIndependentIdentityAdmissionAndRecovery(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	parent, e := s.CreateRun(c, "s", "parent", Message{Content: "busy parent"})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.CreateJob(c, "s", "create-job", "work", parent.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if j.OriginSessionID != "s" || j.OriginRunID != parent.ID || j.SessionID == "s" || j.RunID == "" || j.State != "registered" {
		t.Fatal(j)
	}
	again, e := s.CreateJob(c, "s", "create-job", "work", parent.ID, false)
	if e != nil || again.ID != j.ID || again.RunID != j.RunID {
		t.Fatal(again, e)
	}
	if _, e = s.CreateJob(c, "s", "create-job", "changed", parent.ID, false); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	run, e := s.AdmitJob(c, j.ID)
	if e != nil || run.ID != j.RunID || run.SessionID != j.SessionID {
		t.Fatal(run, e)
	}
	if _, e = s.AdmitJob(c, j.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("repeated claim", e)
	}
	j, e = s.Job(c, j.ID)
	if e != nil || j.State != "unknown" {
		t.Fatal(j, e)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM interaction_runs WHERE session_id='s'").Scan(&n)
	if n != 1 {
		t.Fatal("origin mutated", n)
	}
	if _, e = s.db.Exec("UPDATE interaction_jobs SET origin_session_id=? WHERE id=?", j.SessionID, j.ID); e == nil {
		t.Fatal("mutable origin")
	}
}
func TestJobStopLedgerAndPolicyRevocation(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	j, e := s.CreateJob(c, "s", "job", "work", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.StopJob(c, "s", j.ID, "stop"); e != nil {
		t.Fatal(e)
	}
	if e = s.StopJob(c, "s", j.ID, "stop"); e != nil {
		t.Fatal(e)
	}
	r, e := s.JobOperation(c, "s", "stop")
	if e != nil || r.Job.State != "stopped" || r.Kind != "stop" {
		t.Fatal(r, e)
	}
	if _, e = s.AdmitJob(c, j.ID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	j, e = s.CreateJob(c, "s", "next", "work", "", false)
	if e != nil {
		t.Fatal(e)
	}
	s.db.Exec("DROP TRIGGER interaction_api_binding_immutable")
	if _, e = s.db.Exec("UPDATE interaction_api_sessions SET binding='changed' WHERE session_id='s'"); e != nil {
		t.Fatal(e)
	}
	if e = s.ValidateJob(c, j.ID); !errors.Is(e, ErrJobRevoked) {
		t.Fatal(e)
	}
	if _, e = s.AdmitJob(c, j.ID); e == nil {
		t.Fatal("admitted revoked")
	}
}
func TestJobAdmissionTransactionFailurePreservesStableNeverStartedIdentity(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	j, e := s.CreateJob(c, "s", "job", "work", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`CREATE TRIGGER fail_job_admission BEFORE INSERT ON interaction_events BEGIN SELECT RAISE(ABORT,'owned fixture admission fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdmitJob(c, j.ID); e == nil {
		t.Fatal("fault did not reject")
	}
	got, e := s.Job(c, j.ID)
	if e != nil || got.State != "registered" || got.RunID != j.RunID {
		t.Fatal(got, e)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM interaction_runs WHERE id=?", j.RunID).Scan(&n)
	if n != 0 {
		t.Fatal("partial run committed")
	}
	s.db.Exec("DROP TRIGGER fail_job_admission")
	run, e := s.AdmitJob(c, j.ID)
	if e != nil || run.ID != j.RunID {
		t.Fatal(run, e)
	}
}
func TestJobsGlobalCapacityAndEffectScope(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	for i := 0; i < 4; i++ {
		origin := fmt.Sprintf("origin%d", i)
		if e := s.CreateSession(c, origin); e != nil {
			t.Fatal(e)
		}
		if e := s.BindChatOnly(c, origin, ChatOnlyPolicy{}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.db.Exec("INSERT INTO interaction_api_sessions VALUES(?,?,?,'chat_only')", origin, "create:"+origin, "fixture"); e != nil {
			t.Fatal(e)
		}
		for j := 0; j < JobOriginLimit; j++ {
			if _, e := s.CreateJob(c, origin, fmt.Sprintf("%s-%d", origin, j), "work", "", false); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e := s.CreateJob(c, "s", "overflow", "work", "", false); !errors.Is(e, ErrJobFull) {
		t.Fatal(e)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM interaction_jobs WHERE request_id='overflow'").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
func TestJobPersistsExecutionBudgetAndAdapterPolicy(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	j, e := s.CreateJob(c, "s", "job", "work", "", false)
	if e != nil {
		t.Fatal(e)
	}
	var policy string
	if e = s.db.QueryRow("SELECT execution_policy FROM interaction_jobs WHERE id=?", j.ID).Scan(&policy); e != nil {
		t.Fatal("execution policy not durable", e)
	}
	if !strings.Contains(policy, "MaxSteps") || !strings.Contains(policy, "apply_patch") || !strings.Contains(policy, "false") {
		t.Fatal(policy)
	}
	s.db.Exec("DROP TRIGGER interaction_jobs_immutable")
	if _, e = s.db.Exec("UPDATE interaction_jobs SET execution_policy='changed' WHERE id=?", j.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ValidateJob(c, j.ID); !errors.Is(e, ErrJobRevoked) {
		t.Fatal("changed execution budget/capabilities not revoked", e)
	}
}
