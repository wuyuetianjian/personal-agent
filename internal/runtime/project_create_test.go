package runtime_test

import (
	"agent/internal/config"
	"agent/internal/interaction"
	"agent/internal/orchestrator"
	"agent/internal/project"
	"agent/internal/runtime"
	"agent/internal/storage"
	"agent/internal/trigger"
	"agent/internal/workflow"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestOperatorCreatedProjectDeniedByConcreteRuntime(t *testing.T) {
	db, e := storage.OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "create.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = storage.Migrate(t.Context(), db.SQL); e != nil {
		t.Fatal(e)
	}
	c := t.Context()
	events := &orchestrator.InMemoryEvidenceBus{}
	rt := runtime.NewLocal(config.Config{}, db, events)
	s := interaction.NewStore(db.SQL)
	if e := s.CreateSession(c, "create-session"); e != nil {
		t.Fatal(e)
	}
	if e := s.BindChatOnly(c, "create-session", interaction.ChatOnlyPolicy{}); e != nil {
		t.Fatal(e)
	}
	if _, e := db.SQL.Exec(`INSERT INTO interaction_api_sessions VALUES('create-session','s','binding','chat_only')`); e != nil {
		t.Fatal(e)
	}
	p, e := s.PrepareOperator(c, "create-session", interaction.OperatorIntent{RequestID: "create", Kind: "project.create", NewValue: "created inert project"})
	if e != nil {
		t.Fatal(e)
	}
	check := func(stage string) {
		t.Helper()
		var before, after int
		db.SQL.QueryRow(`SELECT total_changes()`).Scan(&before)
		guarded := &runtime.Runtime{Projects: rt.Projects}
		if _, e := guarded.Run(c, runtime.RunRequest{TaskID: "denied", ProjectID: p.TargetID, Input: "owned fixture"}); !errors.Is(e, runtime.ErrWorkflowProjectDenied) {
			t.Fatal(stage, e)
		}
		if _, e := (runtime.TriggerWorkflowStarter{Runtime: rt}).StartTriggerWorkflow(c, trigger.Trigger{ID: "denied", ProjectID: p.TargetID}); !errors.Is(e, runtime.ErrWorkflowProjectDenied) {
			t.Fatal(stage, e)
		}
		db.SQL.QueryRow(`SELECT total_changes()`).Scan(&after)
		if before != after || len(events.Events()) != 0 {
			t.Fatal("side effect", stage, before, after)
		}
		wf := "wf-" + stage
		if e := rt.Workflows.Create(c, workflow.Run{ID: wf, TaskID: "t", ProjectID: p.TargetID, Status: workflow.StatusPending, InputJSON: `{"input":"fixture"}`}, []workflow.Node{{WorkflowID: wf, NodeID: "n", CapabilityID: "rag.search", Status: workflow.NodePending}}); e != nil {
			t.Fatal(e)
		}
		if e := runtime.NewWorkflowEngine(rt).RunWorkflow(c, wf); !errors.Is(e, runtime.ErrWorkflowProjectDenied) {
			t.Fatal(stage, e)
		}
		var n int
		db.SQL.QueryRow(`SELECT count(*) FROM workflow_checkpoints WHERE workflow_id=?`, wf).Scan(&n)
		if n != 0 {
			t.Fatal("executed node", stage)
		}
	}
	check("pending")
	done, e := s.DecideOperator(c, "create-session", interaction.OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || done.State != "committed" {
		t.Fatal(done, e)
	}
	check("committed")
	row, e := rt.Projects.Get(c, p.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(row)
	var decoded project.Project
	if e = json.Unmarshal(raw, &decoded); e != nil {
		t.Fatal(e)
	}
	decoded.ExecutionClass = project.ExecutionLegacyPolicy
	decoded.ID = "copy"
	if decoded.ExecutionAllowed() {
		t.Fatal("decoded promotion")
	}
	if e = rt.Projects.Save(c, decoded); !errors.Is(e, project.ErrProjectWriteDenied) {
		t.Fatal(e)
	}
}
