package interaction

import (
	"agent/internal/project"
	"agent/internal/rag"
	"agent/internal/storage"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestProjectCatalogLifecycle(t *testing.T) {
	s := operatorSetup(t)
	c := t.Context()
	var before string
	var snapshot string
	// Preserve every legacy policy byte, not merely its effective interpretation.
	rows, e := s.db.Query(`SELECT * FROM projects WHERE id='p'`)
	if e != nil {
		t.Fatal(e)
	}
	cols, _ := rows.Columns()
	rows.Close()
	snapshot = "SELECT json_array("
	for i, col := range cols {
		if i > 0 {
			snapshot += ","
		}
		snapshot += col
	}
	snapshot += ") FROM projects WHERE id='p'"
	if e = s.db.QueryRow(snapshot).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for i, step := range []struct{ kind, value, want string }{{"archive", "archived", "committed"}, {"archive", "archived", "no_op"}, {"restore", "active", "committed"}, {"restore", "active", "no_op"}} {
		p, e := s.PrepareOperator(c, "s", OperatorIntent{fmt.Sprintf("p%d", i), "project." + step.kind, "p", step.value})
		if e != nil {
			t.Fatal(e)
		}
		d := OperatorDecision{fmt.Sprintf("d%d", i), p.OperationID, p.Digest, "confirm"}
		out, e := s.DecideOperator(c, "s", d)
		if e != nil || out.State != step.want {
			t.Fatal(out, e)
		}
		replay, e := s.DecideOperator(c, "s", d)
		if e != nil || replay != out {
			t.Fatal(replay, e)
		}
		var after, state string
		var rev int
		if e = s.db.QueryRow(snapshot).Scan(&after); e != nil || before != after {
			t.Fatal("policy/name mutated", e, before, after)
		}
		if e = s.db.QueryRow(`SELECT state,revision FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&state, &rev); e != nil || state != step.value || rev != 2+i/2 {
			t.Fatal(state, rev, e)
		}
	}
}
func TestProjectCatalogRejectsABAAndRecreatedID(t *testing.T) {
	for _, scenario := range []string{"rename-aba", "recreate", "state-aba", "missing-sidecar", "rename-archived"} {
		t.Run(scenario, func(t *testing.T) {
			s := operatorSetup(t)
			c := t.Context()
			p, e := s.PrepareOperator(c, "s", OperatorIntent{"p", "project.rename", "p", "new"})
			if e != nil {
				t.Fatal(e)
			}
			switch scenario {
			case "rename-aba":
				_, e = s.db.Exec(`UPDATE projects SET name='other' WHERE id='p'; UPDATE projects SET name='old' WHERE id='p'`)
			case "recreate":
				_, e = s.db.Exec(`DELETE FROM projects WHERE id='p'; INSERT INTO projects VALUES('p','old','private','[]','[]','kept','[]','[]','[]','budget','[]')`)
			case "missing-sidecar":
				_, e = s.db.Exec(`DELETE FROM project_catalog_lifecycle WHERE project_id='p'`)
			default:
				for i, v := range []string{"archived", "active"} {
					if scenario == "rename-archived" && i == 1 {
						break
					}
					kind := "project.archive"
					if i == 1 {
						kind = "project.restore"
					}
					q, err := s.PrepareOperator(c, "s", OperatorIntent{fmt.Sprintf("q%d", i), kind, "p", v})
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.DecideOperator(c, "s", OperatorDecision{fmt.Sprintf("r%d", i), q.OperationID, q.Digest, "confirm"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			out, e := s.DecideOperator(c, "s", OperatorDecision{"d", p.OperationID, p.Digest, "confirm"})
			if e != nil || out.State != "rejected" {
				t.Fatal(out, e)
			}
			if scenario == "rename-archived" {
				q, e := s.PrepareOperator(c, "s", OperatorIntent{"fresh", "project.rename", "p", "renamed"})
				if e != nil {
					t.Fatal(e)
				}
				if out, e = s.DecideOperator(c, "s", OperatorDecision{"fresh-d", q.OperationID, q.Digest, "confirm"}); e != nil || out.State != "committed" {
					t.Fatal(out, e)
				}
				var state string
				s.db.QueryRow(`SELECT state FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&state)
				if state != "archived" {
					t.Fatal(state)
				}
			}
		})
	}
}

func TestProjectCatalogAtomicRefusalCancellationAndRollback(t *testing.T) {
	for _, scenario := range []string{"cancel", "expired", "rollback", "foreign", "missing", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			s := operatorSetup(t)
			c := t.Context()
			p, e := s.PrepareOperator(c, "s", OperatorIntent{"p", "project.archive", "p", "archived"})
			if e != nil {
				t.Fatal(e)
			}
			d := OperatorDecision{"d", "p", p.Digest, "confirm"}
			now := time.Now()
			want := "rejected"
			switch scenario {
			case "cancel":
				d.Decision = "cancel"
			case "expired":
				now = time.UnixMilli(p.ExpiresAtMS + 1)
				want = "expired"
			case "rollback":
				_, e = s.db.Exec(`CREATE TEMP TRIGGER fail_catalog_audit BEFORE INSERT ON security_audit_log WHEN NEW.status='committed' BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "missing":
				_, e = s.db.Exec(`DELETE FROM projects WHERE id='p'`)
			case "foreign":
				if e = s.CreateSession(c, "other"); e != nil {
					t.Fatal(e)
				}
				if e = s.BindChatOnly(c, "other", ChatOnlyPolicy{}); e != nil {
					t.Fatal(e)
				}
				_, e = s.db.Exec(`INSERT INTO interaction_api_sessions VALUES('other','other','binding','chat_only')`)
				if _, e = s.OperatorOperation(c, "other", "p"); e == nil {
					t.Fatal("foreign lookup")
				}
			case "conflict":
				bad := p
				bad.Digest = "a" + p.Digest[1:]
				if bad.Digest == p.Digest {
					bad.Digest = "b" + p.Digest[1:]
				}
				d.Digest = bad.Digest
			}
			if e != nil && scenario != "foreign" {
				t.Fatal(e)
			}
			owner := "s"
			if scenario == "foreign" {
				owner = "other"
			}
			out, e := s.decideOperator(c, owner, d, now)
			if scenario == "rollback" || scenario == "foreign" || scenario == "conflict" {
				if e == nil {
					t.Fatal("expected refusal", out)
				}
				got, e := s.OperatorOperation(c, "s", "p")
				if e != nil || got.State != "pending" {
					t.Fatal(got, e)
				}
				var requests int
				if e = s.db.QueryRow(`SELECT count(*) FROM interaction_operator_requests WHERE request_id='d'`).Scan(&requests); e != nil || requests != 0 {
					t.Fatal(requests, e)
				}
			} else if e != nil || out.State != want {
				t.Fatal(out, e)
			}
			var state string
			var rev int
			if e = s.db.QueryRow(`SELECT state,revision FROM project_catalog_lifecycle WHERE project_id='p'`).Scan(&state, &rev); e != nil || state != "active" || rev != 1 {
				t.Fatal(state, rev, e)
			}
		})
	}
}

func TestProjectCatalogHistoricRevisionRefusedButTerminalPreserved(t *testing.T) {
	s := operatorSetup(t)
	c := t.Context()
	p, e := s.PrepareOperator(c, "s", OperatorIntent{"p", "project.rename", "p", "new"})
	if e != nil {
		t.Fatal(e)
	}
	var row int64
	if e = s.db.QueryRow(`SELECT rowid FROM projects WHERE id='p'`).Scan(&row); e != nil {
		t.Fatal(e)
	}
	p.OperationID = "historic"
	p.Revision = operatorHash([]any{"project.rename", "p", row, "old", ""})
	p.Digest = operatorDigest(p)
	raw, _ := json.Marshal(p)
	if _, e = s.db.Exec(`INSERT INTO interaction_operator_operations VALUES('historic','s',?,'pending',?)`, string(raw), string(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO interaction_operator_requests VALUES('historic','s','historic','historic-fingerprint','')`); e != nil {
		t.Fatal(e)
	}
	out, e := s.DecideOperator(c, "s", OperatorDecision{"d", "historic", p.Digest, "confirm"})
	if e != nil || out.State != "rejected" {
		t.Fatal(out, e)
	}
	for _, id := range []string{"historic", "d"} {
		got, e := NewStore(s.db).OperatorOperation(c, "s", id)
		if e != nil || got != out {
			t.Fatal(got, e)
		}
	}
}

func TestProjectCatalogPreservesWorkspacePolicyAndKnowledge(t *testing.T) {
	s, w := knowledgeSetup(t, ModeDefault)
	c := t.Context()
	if _, e := s.db.Exec(`INSERT INTO projects VALUES('p','name','private','[]','[]','legacy-business','[]','[]','[]','','[]')`); e != nil {
		t.Fatal(e)
	}
	k, e := s.PrepareKnowledge(c, "s", KnowledgeIntent{RequestID: "knowledge", Kind: "ingest", SourceKind: "text", Source: "owned", Content: "unique catalog seahorse"})
	if e != nil {
		t.Fatal(e)
	}
	knowledgeConfirm(t, s, k, "knowledge-d")
	rs := rag.NewSQLiteStore(s.db, rag.Chunker{})
	before, e := rs.ListProjectChunks(c, w.ScopeID)
	if e != nil || len(before) != 1 {
		t.Fatal(before, e)
	}
	binding, e := s.Workspace(c, "s")
	if e != nil {
		t.Fatal(e)
	}
	q, e := s.Enqueue(c, "s", "queue", Message{Content: "retained"})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.PrepareOperator(c, "s", OperatorIntent{"p", "project.archive", "p", "archived"})
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.DecideOperator(c, "s", OperatorDecision{"d", "p", p.Digest, "confirm"})
	if e != nil || out.State != "committed" {
		t.Fatal(out, e)
	}
	after, e := rs.ListProjectChunks(c, w.ScopeID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal(after, e)
	}
	again, e := s.Workspace(c, "s")
	if e != nil || !reflect.DeepEqual(binding, again) {
		t.Fatal(again, e)
	}
	var queue string
	if e = s.db.QueryRow(`SELECT state FROM interaction_queue WHERE id=?`, q.ID).Scan(&queue); e != nil || queue != "pending" {
		t.Fatal(queue, e)
	}
	var rows int
	if e = s.db.QueryRow(`SELECT count(*) FROM project_document_scopes WHERE project_id='p'`).Scan(&rows); e != nil || rows != 0 {
		t.Fatal(rows, e)
	}
}

func TestProjectCatalogLegacyStoreInsertAndPolicySemanticsUnaffected(t *testing.T) {
	s := operatorSetup(t)
	c := t.Context()
	legacy := project.Store{DB: s.db}
	p := project.Project{ID: "legacy", Name: "literal (archived 2020-01-01)", PrivacyClass: "private", MemoryScope: "business-only"}
	if e := legacy.Save(c, p); e != nil {
		t.Fatal(e)
	}
	var generation, state string
	var revision int
	if e := s.db.QueryRow(`SELECT generation,revision,state FROM project_catalog_lifecycle WHERE project_id='legacy'`).Scan(&generation, &revision, &state); e != nil || revision != 1 || state != "active" {
		t.Fatal(generation, revision, state, e)
	}
	before, e := legacy.Get(c, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !before.AllowsModel("any") || !before.AllowsSkill("any") || !before.AllowsCapability("any") || !before.AllowsCodingAgent("any") {
		t.Fatal("legacy empty sets changed semantics")
	}
	op, e := s.PrepareOperator(c, "s", OperatorIntent{"archive-legacy", "project.archive", p.ID, "archived"})
	if e != nil {
		t.Fatal(e)
	}
	if out, e := s.DecideOperator(c, "s", OperatorDecision{"archive-legacy-d", op.OperationID, op.Digest, "confirm"}); e != nil || out.State != "committed" {
		t.Fatal(out, e)
	}
	after, e := legacy.Get(c, p.ID)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal(before, after, e)
	}
	if e = legacy.Save(c, p); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT revision,state FROM project_catalog_lifecycle WHERE project_id='legacy'`).Scan(&revision, &state); e != nil || revision != 2 || state != "archived" {
		t.Fatal(revision, state, e)
	}
	p.Name = "changed"
	if e = legacy.Save(c, p); e != nil {
		t.Fatal(e)
	}
	if e = s.db.QueryRow(`SELECT revision,state FROM project_catalog_lifecycle WHERE project_id='legacy'`).Scan(&revision, &state); e != nil || revision != 3 || state != "archived" {
		t.Fatal(revision, state, e)
	}
}

func TestProjectCatalogActualUpgradeAndTerminalRecoveryAcrossReopen(t *testing.T) {
	c := t.Context()
	path := filepath.Join(t.TempDir(), "pre-catalog.db")
	db, e := storage.OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	if _, e = db.SQL.Exec(`CREATE TABLE schema_migrations (version text primary key, applied_at timestamp not null DEFAULT CURRENT_TIMESTAMP)`); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir("../storage/migrations")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Name() >= "0026" {
			break
		}
		raw, e := os.ReadFile(filepath.Join("../storage/migrations", entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		tx, e := db.SQL.BeginTx(c, nil)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(string(raw)); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if _, e = tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, entry.Name()); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	s := NewStore(db.SQL)
	if e = s.CreateSession(c, "s"); e != nil {
		t.Fatal(e)
	}
	if e = s.BindChatOnly(c, "s", ChatOnlyPolicy{}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec(`INSERT INTO interaction_api_sessions VALUES('s','s','binding','chat_only'); INSERT INTO projects VALUES('p','old','private','[]','[]','','[]','[]','[]','','[]')`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.SQL.BeginTx(c, nil)
	if e != nil {
		t.Fatal(e)
	}
	scope, policy, e := operatorAuthority(c, tx, "s")
	tx.Rollback()
	if e != nil {
		t.Fatal(e)
	}
	var row int64
	if e = db.SQL.QueryRow(`SELECT rowid FROM projects WHERE id='p'`).Scan(&row); e != nil {
		t.Fatal(e)
	}
	p := OperatorOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: "old-pending", SessionID: "s", ScopeID: scope, PolicyDigest: policy, Kind: "project.rename", TargetID: "p", OldValue: "old", NewValue: "new", Revision: operatorHash([]any{"project.rename", "p", row, "old", ""}), ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), State: "pending", Reason: "historic"}
	p.Digest = operatorDigest(p)
	historic := p
	historic.OperationID = "old-terminal"
	historic.Digest = operatorDigest(historic)
	terminal := historic
	terminal.State = "committed"
	terminal.Decision = "confirm"
	terminal.DecisionRequestID = "old-terminal-d"
	for _, pair := range [][2]OperatorOperation{{p, p}, {historic, terminal}} {
		preview, _ := json.Marshal(pair[0])
		result, _ := json.Marshal(pair[1])
		if _, e = db.SQL.Exec(`INSERT INTO interaction_operator_operations VALUES(?,?,?,?,?)`, pair[0].OperationID, "s", string(preview), pair[1].State, string(result)); e != nil {
			t.Fatal(e)
		}
		if _, e = db.SQL.Exec(`INSERT INTO interaction_operator_requests VALUES(?,?,?,?, '')`, pair[0].OperationID, "s", pair[0].OperationID, "historic"); e != nil {
			t.Fatal(e)
		}
	}
	if e = storage.Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	out, e := s.DecideOperator(c, "s", OperatorDecision{"new-decision", p.OperationID, p.Digest, "confirm"})
	if e != nil || out.State != "rejected" {
		t.Fatal(out, e)
	}
	a, e := s.PrepareOperator(c, "s", OperatorIntent{"archive", "project.archive", "p", "archived"})
	if e != nil {
		t.Fatal(e)
	}
	done, e := s.DecideOperator(c, "s", OperatorDecision{"archive-d", a.OperationID, a.Digest, "confirm"})
	if e != nil || done.State != "committed" {
		t.Fatal(done, e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = storage.ValidateSQLiteFile(c, path); e != nil {
		t.Fatal(e)
	}
	db, e = storage.OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	if e = storage.Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	s = NewStore(db.SQL)
	for id, want := range map[string]OperatorOperation{"old-terminal": terminal, "old-pending": out, "archive": done, "archive-d": done} {
		got, e := s.OperatorOperation(c, "s", id)
		if e != nil || got != want {
			t.Fatal(id, got, e)
		}
	}
	var state, name string
	var rev int
	if e = db.SQL.QueryRow(`SELECT p.name,l.state,l.revision FROM projects p JOIN project_catalog_lifecycle l ON l.project_id=p.id WHERE p.id='p'`).Scan(&name, &state, &rev); e != nil || name != "old" || state != "archived" || rev != 2 {
		t.Fatal(name, state, rev, e)
	}
}
