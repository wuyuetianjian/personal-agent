package interaction

import (
	"agent/internal/memory"
	"agent/internal/project"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func fixtureProjectLabel(t *testing.T, s *Store, session, id string) *ProjectLabel {
	t.Helper()
	c := t.Context()
	if e := (project.Store{DB: s.db}).Save(c, project.Project{ID: id, Name: "Exact\n名称"}); e != nil {
		t.Fatal(e)
	}
	tx, e := s.db.BeginTx(c, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	p, e := ActiveProjectLabel(c, tx, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = InsertProjectLabel(c, tx, session, p); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestHistoricalProjectLabelCopiesAfterArchiveDeleteAndRecreate(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	original := fixtureProjectLabel(t, s, "s", "p")
	r, e := s.CreateRun(c, "s", "failed", Message{Content: "original authority"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Transition(c, r.ID, r.Version, RunFailed); e != nil {
		t.Fatal(e)
	}
	var policyBefore string
	if e = s.db.QueryRow(`SELECT policy FROM interaction_chat_policies WHERE session_id='s'`).Scan(&policyBefore); e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"archive", "delete", "recreate"} {
		switch state {
		case "archive":
			_, e = s.db.Exec(`UPDATE project_catalog_lifecycle SET state='archived',revision=revision+1 WHERE project_id='p'`)
		case "delete":
			_, e = s.db.Exec(`DELETE FROM projects WHERE id='p'`)
		case "recreate":
			e = (project.Store{DB: s.db}).Save(c, project.Project{ID: "p", Name: "replacement"})
		}
		if e != nil {
			t.Fatal(e)
		}
		for _, kind := range []string{"branch", "clear", "rewind"} {
			op := SessionOperation{RequestID: state + kind, Kind: kind, ExpectedCursor: sessionCursor(t, s, "s")}
			if kind != "clear" {
				op.BoundaryRunID = r.ID
			}
			branch, e := s.BranchSession(c, "s", op)
			if e != nil {
				t.Fatal(e)
			}
			got, e := s.ProjectLabel(c, branch.SessionID)
			if e != nil || !reflect.DeepEqual(got, original) {
				t.Fatal(got, e)
			}
			replay, e := s.BranchSession(c, "s", op)
			if e != nil || replay != branch {
				t.Fatal(replay, e)
			}
			var copied string
			if e = s.db.QueryRow(`SELECT policy FROM interaction_chat_policies WHERE session_id=?`, branch.SessionID).Scan(&copied); e != nil || copied != policyBefore {
				t.Fatal("policy changed", e)
			}
		}
		job, e := s.CreateJob(c, "s", state+"job", "job instruction", "", false)
		if e != nil {
			t.Fatal(e)
		}
		got, e := s.ProjectLabel(c, job.SessionID)
		if e != nil || !reflect.DeepEqual(got, original) {
			t.Fatal(got, e)
		}
		if e = s.ValidateJob(c, job.ID); e != nil {
			t.Fatal("descriptive lifecycle revoked job", e)
		}
	}
	live, e := ActiveProjectLabel(c, s.db, "p")
	if e != nil || live.Generation == original.Generation || live.Name == original.Name {
		t.Fatal(live, e)
	}
}
func TestProjectLabelImmutableAndCorruptionFailsClosed(t *testing.T) {
	s, _ := queueSetup(t)
	c := t.Context()
	fixtureProjectLabel(t, s, "s", "p")
	for _, query := range []string{`UPDATE interaction_session_project_labels SET name='changed' WHERE session_id='s'`, `DELETE FROM interaction_session_project_labels WHERE session_id='s'`, `INSERT OR REPLACE INTO interaction_session_project_labels SELECT * FROM interaction_session_project_labels WHERE session_id='s'`} {
		if _, e := s.db.Exec(query); e == nil {
			t.Fatal("mutable historical label", query)
		}
	}
	if _, e := s.db.Exec(`DROP TRIGGER interaction_project_label_no_update; UPDATE interaction_session_project_labels SET generation='invalid' WHERE session_id='s'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ProjectLabel(c, "s"); e == nil {
		t.Fatal("corrupt label read")
	}
	if _, e := s.BranchSession(c, "s", SessionOperation{RequestID: "corrupt-clear", Kind: "clear", ExpectedCursor: 0}); e == nil {
		t.Fatal("copied corrupt provenance")
	}
	if _, e := s.CreateJob(c, "s", "corrupt-job", "work", "", false); e == nil {
		t.Fatal("job copied corrupt provenance")
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&n); e != nil || n != 1 {
		t.Fatal("partial derived session", n, e)
	}
}
func TestProjectLabelDoesNotChangeKnowledgeScopeOrLegacyRetrieval(t *testing.T) {
	s, w := knowledgeSetup(t, ModeDefault)
	c := t.Context()
	// knowledgeSetup owns an already-running fixture turn. End it before a new
	// retrieval turn, preserving the production one-active-run invariant.
	var priorID string
	if e := s.db.QueryRow(`SELECT id FROM interaction_runs WHERE session_id='s' AND state='running'`).Scan(&priorID); e != nil {
		t.Fatal(e)
	}
	prior, e := s.GetRun(c, priorID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Transition(c, prior.ID, prior.Version, RunFailed); e != nil {
		t.Fatal(e)
	}

	fixtureProjectLabel(t, s, "s", "label-a")
	// Label name/id is not inserted into context and is not a legacy memory grant.
	mem := memory.NewSemanticStore(s.db, nil)
	if e := mem.Upsert(c, memory.SemanticFact{ID: "legacy-label", Scope: "project:label-a", Subject: "seahorse", Object: "LEGACY_SECRET"}); e != nil {
		t.Fatal(e)
	}
	p, e := s.PrepareKnowledge(c, "s", KnowledgeIntent{RequestID: "ingest", Kind: "ingest", SourceKind: "text", Source: "owned", Content: "unique seahorse shared evidence"})
	if e != nil {
		t.Fatal(e)
	}
	if got := knowledgeConfirm(t, s, p, "confirm"); got.State != "committed" {
		t.Fatal(got)
	}
	for _, spec := range []struct{ id, root, label string }{{"s", w.Policy.Root, "label-a"}, {"same", w.Policy.Root, "label-b"}, {"other", t.TempDir(), "label-a"}} {
		bound := w
		if spec.id != "s" {
			if e = s.CreateSession(c, spec.id); e != nil {
				t.Fatal(e)
			}
			bound, e = s.BindWorkspace(c, spec.id, WorkspacePolicy{Root: spec.root, Mode: ModeDefault})
			if e != nil {
				t.Fatal(e)
			}
			fixtureProjectLabel(t, s, spec.id, spec.label)
		}
		if spec.id == "same" && bound.ScopeID != w.ScopeID || spec.id == "other" && bound.ScopeID == w.ScopeID {
			t.Fatal("label affected workspace scope")
		}
		if _, e = s.CreateRun(c, spec.id, "search", Message{Content: "seahorse"}); e != nil {
			t.Fatal(e)
		}
		out, e := NewContextAssembler(s).Build(c, spec.id, TokenBudget{Total: 20000, Output: 1000})
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "LEGACY_SECRET") || strings.Contains(string(raw), spec.label) {
			t.Fatal("label leaked into execution context", string(raw))
		}
		if strings.Contains(string(raw), "unique seahorse shared evidence") != (spec.id != "other") {
			t.Fatal("knowledge boundary mismatch", spec.id, string(raw))
		}
	}
}
