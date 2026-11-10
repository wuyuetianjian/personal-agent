package interaction

import (
	"agent/internal/project"
	"agent/internal/storage"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func createPreview(t *testing.T, s *Store, id string) OperatorOperation {
	t.Helper()
	p, e := s.PrepareOperator(t.Context(), "s", OperatorIntent{id, "project.create", "", "New <script>\u202e\x00 project"})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func createCount(t *testing.T, s *Store, table, id string) int {
	t.Helper()
	key := "project_id"
	if table == "projects" {
		key = "id"
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+key+`=?`, id).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestProjectCreatePreviewCommit(t *testing.T) {
	s := operatorSetup(t)
	c := t.Context()
	in := OperatorIntent{"create", "project.create", "", "New <script>\u202e\x00 project"}
	p := createPreview(t, s, in.RequestID)
	if p.TargetID == "" || p.TargetID == p.OperationID || p.OldValue != "" || !strings.Contains(p.Reason, "metadata_only") || !strings.Contains(p.Reason, "no tools/models/workflows/knowledge isolation enabled") {
		t.Fatal(p)
	}
	for _, table := range []string{"projects", "project_catalog_lifecycle", "project_execution_classification"} {
		if createCount(t, s, table, p.TargetID) != 0 {
			t.Fatal("preview created", table)
		}
	}
	if _, e := (project.Store{DB: s.db}).Get(c, p.TargetID); e == nil {
		t.Fatal("pending project exists")
	}
	again, e := s.PrepareOperator(c, "s", in)
	if e != nil || p != again {
		t.Fatal(again, e)
	}
	in.NewValue = "changed"
	if _, e = s.PrepareOperator(c, "s", in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	d := OperatorDecision{"confirm", p.OperationID, p.Digest, "confirm"}
	done, e := s.DecideOperator(c, "s", d)
	if e != nil || done.State != "committed" {
		t.Fatal(done, e)
	}
	for _, id := range []string{p.OperationID, d.RequestID} {
		got, e := s.OperatorOperation(c, "s", id)
		if e != nil || got != done {
			t.Fatal(got, e)
		}
	}
	if replay, e := s.DecideOperator(c, "s", d); e != nil || replay != done {
		t.Fatal(replay, e)
	}
	row, e := (project.Store{DB: s.db}).Get(c, p.TargetID)
	if e != nil || row.Name != p.NewValue || row.ExecutionClass != "metadata_only" || row.MemoryScope != "" || row.PrivacyClass != "local_private" || row.BudgetPolicy != "{}" || row.ExecutionAllowed() || len(row.RepositoryRefs)+len(row.KnowledgeScopes)+len(row.AllowedSkills)+len(row.AllowedCapabilities)+len(row.AllowedCodingAgents)+len(row.AllowedModels) != 0 {
		t.Fatal(row, e)
	}
	if e = (project.Store{DB: s.db}).Save(c, row); !errors.Is(e, project.ErrProjectWriteDenied) {
		t.Fatal(e)
	}
	var audits int
	s.db.QueryRow(`SELECT count(*) FROM security_audit_log WHERE target=? AND status='committed'`, p.TargetID).Scan(&audits)
	if audits != 1 {
		t.Fatal(audits)
	}
	for i, step := range []struct{ k, v string }{{"rename", "new name"}, {"archive", "archived"}, {"restore", "active"}} {
		q, e := s.PrepareOperator(c, "s", OperatorIntent{fmt.Sprint("next", i), "project." + step.k, p.TargetID, step.v})
		if e != nil {
			t.Fatal(e)
		}
		r, e := s.DecideOperator(c, "s", OperatorDecision{fmt.Sprint("done", i), q.OperationID, q.Digest, "confirm"})
		if e != nil || r.State != "committed" {
			t.Fatal(r, e)
		}
	}
}
func TestProjectCreateRefusalRollbackAndConcurrent(t *testing.T) {
	for _, scenario := range []string{"cancel", "expiry", "collision", "tombstone", "lifecycle-tombstone", "rollback", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			s := operatorSetup(t)
			p := createPreview(t, s, "create")
			d := OperatorDecision{"confirm", p.OperationID, p.Digest, "confirm"}
			now := time.Now()
			want := "rejected"
			switch scenario {
			case "cancel":
				d.Decision = "cancel"
			case "expiry":
				now = time.UnixMilli(p.ExpiresAtMS + 1)
				want = "expired"
			case "collision", "tombstone":
				if _, e := s.db.Exec(`INSERT INTO project_execution_classification VALUES(?,'metadata_only')`, p.TargetID); e != nil {
					t.Fatal(e)
				}
				if scenario == "collision" {
					if _, e := s.db.Exec(`INSERT INTO projects VALUES(?,'existing','local_private','[]','[]','','[]','[]','[]','{}','[]')`, p.TargetID); e != nil {
						t.Fatal(e)
					}
				}
			case "lifecycle-tombstone":
				s.db.Exec(`INSERT INTO project_catalog_lifecycle VALUES(?,'retained',1,'active')`, p.TargetID)
			case "rollback":
				s.db.Exec(`CREATE TEMP TRIGGER fail_create_audit BEFORE INSERT ON security_audit_log WHEN NEW.status='committed' BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "concurrent":
				var wg sync.WaitGroup
				for range 6 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r, e := s.DecideOperator(t.Context(), "s", d)
						if e != nil || r.State != "committed" {
							t.Error(r, e)
						}
					}()
				}
				wg.Wait()
				want = "committed"
			}
			r, e := s.decideOperator(t.Context(), "s", d, now)
			if scenario == "rollback" {
				if e == nil {
					t.Fatal("missing injected rollback")
				}
				for _, table := range []string{"projects", "project_execution_classification", "project_catalog_lifecycle"} {
					if createCount(t, s, table, p.TargetID) != 0 {
						t.Fatal("partial", table)
					}
				}
				got, e := s.OperatorOperation(t.Context(), "s", p.OperationID)
				if e != nil || got.State != "pending" {
					t.Fatal(got, e)
				}
				return
			}
			if e != nil || r.State != want {
				t.Fatal(r, e)
			}
			if scenario != "collision" && scenario != "concurrent" && createCount(t, s, "projects", p.TargetID) != 0 {
				t.Fatal("unexpected project")
			}
		})
	}
}
func TestProjectCreateStrictInputAndPendingBound(t *testing.T) {
	s := operatorSetup(t)
	for i, in := range []OperatorIntent{{"", "project.create", "caller", "name"}, {"", "project.create", " ", "name"}, {"", "project.create", "", " "}, {"", "project.create", "", strings.Repeat("x", 2049)}, {"", "project.create", "", "\xff"}, {"", "project.rename", "", "name"}} {
		in.RequestID = fmt.Sprint("bad", i)
		if _, e := s.PrepareOperator(t.Context(), "s", in); !errors.Is(e, ErrInvalid) {
			t.Fatal(in, e)
		}
	}
	for i := range 64 {
		createPreview(t, s, fmt.Sprint("pending", i))
	}
	if _, e := s.PrepareOperator(t.Context(), "s", OperatorIntent{"overflow", "project.create", "", "name"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestProjectCreateCapacityAndExpiryEvidence(t *testing.T) {
	for _, which := range []string{"catalog-preview", "catalog-confirm", "retained", "expired"} {
		t.Run(which, func(t *testing.T) {
			s := operatorSetup(t)
			c := t.Context()
			var p OperatorOperation
			if which == "catalog-confirm" {
				p = createPreview(t, s, "before-full")
			}
			switch which {
			case "catalog-preview", "catalog-confirm":
				_, e := s.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<9999) INSERT INTO project_execution_classification SELECT 'retained-'||x,'metadata_only' FROM n`)
				if e != nil {
					t.Fatal(e)
				}
			case "retained":
				_, e := s.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10000) INSERT INTO interaction_operator_operations SELECT 'retained-'||x,'s','{"kind":"project.create","expires_at_ms":1}','expired','{}' FROM n`)
				if e != nil {
					t.Fatal(e)
				}
			case "expired":
				p, e := s.prepareOperator(c, "s", OperatorIntent{"expired-create", "project.create", "", "name"}, time.Now().Add(-time.Hour))
				if e != nil {
					t.Fatal(e)
				}
				var before, after int
				s.db.QueryRow(`SELECT total_changes()`).Scan(&before)
				got, e := s.OperatorOperation(c, "s", p.OperationID)
				s.db.QueryRow(`SELECT total_changes()`).Scan(&after)
				if e != nil || got.State != "expired" || before != after {
					t.Fatal(got, e, before, after)
				}
				var state string
				s.db.QueryRow(`SELECT state FROM interaction_operator_operations WHERE id=?`, p.OperationID).Scan(&state)
				if state != "pending" {
					t.Fatal("expiry read erased evidence")
				}
				for i := range 64 {
					createPreview(t, s, fmt.Sprint("fresh", i))
				}
				return
			}
			if which == "catalog-confirm" {
				got, e := s.DecideOperator(c, "s", OperatorDecision{"decline", p.OperationID, p.Digest, "confirm"})
				if e != nil || got.State != "rejected" || createCount(t, s, "projects", p.TargetID) != 0 {
					t.Fatal(got, e)
				}
				return
			}
			if _, e := s.PrepareOperator(c, "s", OperatorIntent{"full", "project.create", "", "name"}); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
		})
	}
}

func TestProjectCreateDiskRestartAndReadOwnership(t *testing.T) {
	c := t.Context()
	path := filepath.Join(t.TempDir(), "create.db")
	db, e := storage.OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	if e = storage.Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	s := NewStore(db.SQL)
	for _, id := range []string{"s", "foreign"} {
		if e = s.CreateSession(c, id); e != nil {
			t.Fatal(e)
		}
		if e = s.BindChatOnly(c, id, ChatOnlyPolicy{}); e != nil {
			t.Fatal(e)
		}
		if _, e = db.SQL.Exec(`INSERT INTO interaction_api_sessions VALUES(?,?,?,'chat_only')`, id, id, "binding"); e != nil {
			t.Fatal(e)
		}
	}
	p := createPreview(t, s, "pending-restart")
	reopen := func() {
		t.Helper()
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
	}
	reopen()
	if got, e := s.OperatorOperation(c, "s", p.OperationID); e != nil || got != p {
		t.Fatal(got, e)
	}
	if createCount(t, s, "projects", p.TargetID) != 0 {
		t.Fatal("reopen created pending target")
	}
	if _, e = s.OperatorOperation(c, "foreign", p.OperationID); e == nil {
		t.Fatal("foreign read")
	}
	d := OperatorDecision{"decision-restart", p.OperationID, p.Digest, "confirm"}
	if _, e = s.DecideOperator(c, "foreign", d); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	done, e := s.DecideOperator(c, "s", d)
	if e != nil || done.State != "committed" {
		t.Fatal(done, e)
	}
	reopen()
	var before, after int
	db.SQL.QueryRow(`SELECT total_changes()`).Scan(&before)
	for _, request := range []string{p.OperationID, d.RequestID} {
		if got, e := s.OperatorOperation(c, "s", request); e != nil || got != done {
			t.Fatal(got, e)
		}
	}
	db.SQL.QueryRow(`SELECT total_changes()`).Scan(&after)
	if before != after {
		t.Fatal("recovery wrote")
	}
	if got, e := s.PrepareOperator(c, "s", OperatorIntent{p.OperationID, "project.create", "", p.NewValue}); e != nil || got != done {
		t.Fatal(got, e)
	}
	if got, e := s.DecideOperator(c, "s", d); e != nil || got != done {
		t.Fatal(got, e)
	}
	if createCount(t, s, "projects", p.TargetID) != 1 {
		t.Fatal("duplicate")
	}
	var n int
	db.SQL.QueryRow(`SELECT count(*) FROM security_audit_log WHERE status='committed'`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestProjectCreateRollbackAtEachWrite(t *testing.T) {
	for _, table := range []string{"project_execution_classification", "projects", "project_catalog_lifecycle", "interaction_operator_requests", "interaction_operator_operations", "security_audit_log"} {
		t.Run(table, func(t *testing.T) {
			s := operatorSetup(t)
			p := createPreview(t, s, "rollback")
			event := "INSERT"
			if table == "interaction_operator_operations" {
				event = "UPDATE"
			}
			if _, e := s.db.Exec(`CREATE TEMP TRIGGER injected BEFORE ` + event + ` ON ` + table + ` BEGIN SELECT RAISE(ABORT,'injected'); END`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.DecideOperator(t.Context(), "s", OperatorDecision{"rollback-d", p.OperationID, p.Digest, "confirm"}); e == nil {
				t.Fatal("expected rollback")
			}
			for _, target := range []string{"projects", "project_execution_classification", "project_catalog_lifecycle"} {
				if createCount(t, s, target, p.TargetID) != 0 {
					t.Fatal("partial", target)
				}
			}
			got, e := s.OperatorOperation(t.Context(), "s", p.OperationID)
			if e != nil || got != p {
				t.Fatal(got, e)
			}
			var count int
			s.db.QueryRow(`SELECT count(*) FROM interaction_operator_requests WHERE request_id='rollback-d'`).Scan(&count)
			if count != 0 {
				t.Fatal("partial request")
			}
			s.db.QueryRow(`SELECT count(*) FROM security_audit_log WHERE target=?`, p.TargetID).Scan(&count)
			if count != 1 {
				t.Fatal("partial audit", count)
			}
		})
	}
}

func TestProjectCreateUnicodeAndExactByteLimit(t *testing.T) {
	s := operatorSetup(t)
	for i, name := range []string{"\ufeff", strings.Repeat("界", 682) + "ab"} {
		p, e := s.PrepareOperator(t.Context(), "s", OperatorIntent{fmt.Sprint("unicode", i), "project.create", "", name})
		if e != nil || p.NewValue != name {
			t.Fatal(p, e)
		}
	}
	for i, name := range []string{"\u0085", strings.Repeat("界", 683)} {
		if _, e := s.PrepareOperator(t.Context(), "s", OperatorIntent{fmt.Sprint("invalid-unicode", i), "project.create", "", name}); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
}
