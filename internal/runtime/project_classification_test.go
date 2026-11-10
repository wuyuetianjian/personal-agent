package runtime

import (
	"agent/internal/orchestrator"
	"agent/internal/project"
	"agent/internal/trigger"
	"agent/internal/workflow"
	"errors"
	"testing"
)

func TestProjectClassificationAdmissionBeforeAnyWork(t *testing.T) {
	for _, id := range []string{"missing", "metadata", "unknown", "unclassified", " "} {
		t.Run(id, func(t *testing.T) {
			db := openRuntimeTestDB(t)
			ctx := t.Context()
			events := &orchestrator.InMemoryEvidenceBus{}
			rt := NewLocal(configForRuntimeTest(), db, events)
			if _, err := db.SQL.Exec(`INSERT INTO project_execution_classification VALUES('metadata','metadata_only');INSERT INTO projects VALUES('metadata','label','local_private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL.Exec(`PRAGMA ignore_check_constraints=ON;INSERT INTO project_execution_classification VALUES('unknown','future');PRAGMA ignore_check_constraints=OFF;INSERT INTO projects VALUES('unknown','corrupt','private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL.Exec(`INSERT INTO projects VALUES('unclassified','missing class','private','[]','[]','','[]','[]','[]','{}','[]')`); err != nil {
				t.Fatal(err)
			}
			var ddl string
			if err := db.SQL.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='project_execution_immutable_delete'`).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL.Exec(`DROP TRIGGER project_execution_immutable_delete;DELETE FROM project_execution_classification WHERE project_id='unclassified';` + ddl); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.projectForRun(ctx, id); !errors.Is(err, ErrWorkflowProjectDenied) {
				t.Errorf("runtime helper admitted %q: %v", id, err)
			}
			if _, err := NewWorkflowEngine(rt).project(ctx, id); !errors.Is(err, ErrWorkflowProjectDenied) {
				t.Errorf("engine helper admitted %q: %v", id, err)
			}
			var before, after int
			if err := db.SQL.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			// A skeletal runtime is deliberate: any retrieval/provider/process entry
			// before project admission would panic or produce events/writes.
			guarded := &Runtime{Projects: rt.Projects}
			if _, err := guarded.Run(ctx, RunRequest{TaskID: "denied", ProjectID: id, Input: "owned test"}); !errors.Is(err, ErrWorkflowProjectDenied) {
				t.Fatalf("dispatch: %v", err)
			}
			if _, err := (TriggerWorkflowStarter{Runtime: rt}).StartTriggerWorkflow(ctx, trigger.Trigger{ID: "denied", ProjectID: id}); !errors.Is(err, ErrWorkflowProjectDenied) {
				t.Fatalf("trigger: %v", err)
			}
			if err := db.SQL.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || before != after {
				t.Fatalf("denied admission changed DB %d -> %d: %v", before, after, err)
			}
			if len(events.Events()) != 0 {
				t.Fatal("denied admission published work")
			}
			for _, nodes := range [][]workflow.Node{nil, {{WorkflowID: "wf-node", NodeID: "n", CapabilityID: "rag.search", Status: workflow.NodePending}}} {
				wf := "wf-empty"
				if len(nodes) > 0 {
					wf = "wf-node"
				}
				if err := rt.Workflows.Create(ctx, workflow.Run{ID: wf, TaskID: "t", ProjectID: id, Status: workflow.StatusPending, InputJSON: `{"input":"fixture"}`}, nodes); err != nil {
					t.Fatal(err)
				}
				if err := NewWorkflowEngine(rt).RunWorkflow(ctx, wf); !errors.Is(err, ErrWorkflowProjectDenied) {
					t.Fatalf("workflow %s: %v", wf, err)
				}
				var count int
				if err := db.SQL.QueryRow(`SELECT count(*) FROM workflow_checkpoints WHERE workflow_id=?`, wf).Scan(&count); err != nil || count != 0 {
					t.Fatalf("node work happened: %d %v", count, err)
				}
			}
		})
	}
}

func TestProjectClassificationLegitimateLegacyAdmission(t *testing.T) {
	db := openRuntimeTestDB(t)
	rt := NewLocal(configForRuntimeTest(), db, &orchestrator.InMemoryEvidenceBus{})
	if err := rt.Projects.Save(t.Context(), project.Project{ID: "legacy", Name: "existing legacy"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "legacy"} {
		p, err := rt.projectForRun(t.Context(), id)
		if err != nil || !p.AllowsModel("model") || !p.AllowsSkill("skill") {
			t.Fatal(p, err)
		}
		p, err = NewWorkflowEngine(rt).project(t.Context(), id)
		if err != nil || !p.AllowsCapability("cap") || !p.AllowsCodingAgent("agent") {
			t.Fatal(p, err)
		}
	}
}
