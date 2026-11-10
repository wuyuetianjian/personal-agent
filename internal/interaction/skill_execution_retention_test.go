package interaction

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestSkillEventsBoundControlReserveAndDedup(t *testing.T) {
	s, id := admitTestSkillRun(t)
	r, _ := s.GetRun(t.Context(), id)
	s.db.Exec(`UPDATE interaction_skill_runs SET ordinary_events=512 WHERE run_id=?`, id)
	tx, _ := s.db.BeginTx(t.Context(), nil)
	if e := appendEngineEvent(t.Context(), tx, r, "tool.proposed", nil); !errors.Is(e, ErrBudget) {
		tx.Rollback()
		t.Fatal(e)
	}
	tx.Rollback()
	for i := 0; i < 3; i++ {
		tx, _ = s.db.BeginTx(t.Context(), nil)
		if e := appendEngineEvent(t.Context(), tx, r, "run.recovered", nil); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if e := tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='run.recovered'`, id).Scan(&n)
	if n != 1 {
		t.Fatal("recovery duplicated", n)
	}
	s.db.Exec(`UPDATE interaction_skill_runs SET control_events=60 WHERE run_id=?`, id)
	tx, _ = s.db.BeginTx(t.Context(), nil)
	if e := appendEngineEvent(t.Context(), tx, r, "run.recovered", "new"); !errors.Is(e, ErrBudget) {
		tx.Rollback()
		t.Fatal(e)
	}
	tx.Rollback()
	tx, _ = s.db.BeginTx(t.Context(), nil)
	if e := appendEngineEvent(t.Context(), tx, r, "run.cancelled", nil); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func TestSkillRetainedCheckpointBytesBoundInSQL(t *testing.T) {
	s, id := admitTestSkillRun(t)
	var before int
	s.db.QueryRow(`SELECT retained_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&before)
	if before <= 4096 {
		t.Fatal(before)
	}
	s.db.Exec(`UPDATE interaction_skill_runs SET retained_bytes=8388608 WHERE run_id=?`, id)
	if _, e := s.db.Exec(`UPDATE interaction_engine SET checkpoint=checkpoint||'extra bytes' WHERE run_id=?`, id); e == nil {
		t.Fatal("unaccounted checkpoint growth")
	}
}
func TestSkillEventSQLCannotBypassCategoryCap(t *testing.T) {
	s, id := admitTestSkillRun(t)
	s.db.Exec(`UPDATE interaction_skill_runs SET ordinary_events=512 WHERE run_id=?`, id)
	if _, err := s.db.Exec(`INSERT INTO interaction_events VALUES('s',?,999999,1,'tool.other','{}')`, id); err == nil {
		t.Fatal("SQL event bypassed durable quota")
	}
}
func TestSkillExecutionSQLLifetimeAndPoolBoundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		limit    int
		terminal bool
	}{{"live_pool", 32, false}, {"lifetime_count", 256, true}} {
		t.Run(test.name, func(t *testing.T) {
			s, id := admitTestSkillRun(t)
			if test.terminal {
				if _, e := s.db.Exec(`UPDATE interaction_skill_runs SET terminal=1,reserved_bytes=retained_bytes WHERE run_id=?`, id); e != nil {
					t.Fatal(e)
				}
			}
			seed := func(tx *sql.Tx, n int) error {
				run := fmt.Sprintf("retained-%d", n)
				if _, e := tx.Exec(`INSERT INTO interaction_runs(id,session_id,request_id,message,state,version,execution_kind) VALUES(?,'s',?,'{}','failed',1,'skill_readonly_v1')`, run, run); e != nil {
					return e
				}
				_, e := tx.Exec(`INSERT INTO interaction_skill_runs(run_id,session_id,catalog_id,revision_id,pin,pin_hash,inputs,plan,terminal,retained_bytes,reserved_bytes) SELECT ?,session_id,catalog_id,revision_id,pin,pin_hash,inputs,plan,terminal,retained_bytes,reserved_bytes FROM interaction_skill_runs WHERE run_id=?`, run, id)
				return e
			}
			tx, e := s.db.BeginTx(t.Context(), nil)
			if e != nil {
				t.Fatal(e)
			}
			// Historical ledger rows are storage-only fixtures, never execution inputs.
			for i := 1; i < test.limit; i++ {
				if e = seed(tx, i); e != nil {
					tx.Rollback()
					t.Fatal(i, e)
				}
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			tx, e = s.db.BeginTx(t.Context(), nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = seed(tx, test.limit); e == nil {
				tx.Rollback()
				t.Fatal("capacity exceeded")
			}
			tx.Rollback()
			var count int
			s.db.QueryRow(`SELECT count(*) FROM interaction_skill_runs`).Scan(&count)
			if count != test.limit {
				t.Fatal(count)
			}
		})
	}
}
