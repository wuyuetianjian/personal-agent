package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestHooksStorageMigration(t *testing.T) {
	referenceSchemas.Do(loadReferenceSchemas)
	if referenceSchemas.err != nil {
		t.Fatal(referenceSchemas.err)
	}
	db, e := OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "hooks.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Migrate(t.Context(), db.SQL); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSchema(t.Context(), db.SQL); e != nil {
		t.Fatal(e)
	}
}

func TestHooksStorageSessionAndOperationSQLLimits(t *testing.T) {
	db, e := OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "bounds.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Migrate(t.Context(), db.SQL); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 129; i++ {
		id := fmt.Sprint("s", i)
		if _, e = db.SQL.Exec(`INSERT INTO interaction_sessions VALUES(?,0)`, id); e != nil {
			t.Fatal(e)
		}
		_, e = db.SQL.Exec(`INSERT INTO interaction_hook_sessions VALUES(?,0,'disabled','')`, id)
		if (i < 128) != (e == nil) {
			t.Fatal(i, e)
		}
	}
	for _, query := range []string{`DELETE FROM interaction_hook_sessions WHERE session_id='s0'`, `INSERT OR REPLACE INTO interaction_hook_sessions VALUES('s0',0,'disabled','')`, `UPDATE interaction_hook_sessions SET generation=0,state='enabled',revision_id='foreign' WHERE session_id='s0'`} {
		if _, e = db.SQL.Exec(query); e == nil {
			t.Fatal(query)
		}
	}
	// Historical operation fixtures exercise SQL guards directly, without API
	// admission or an invocation. Expired previews still spend lifetime capacity.
	seed := func(session, id string) error {
		raw := fmt.Sprintf(`{"operation_id":%q,"session_id":%q,"kind":"disable","revision_id":"","expires_at_ms":0,"state":"pending"}`, id, session)
		_, e := db.SQL.Exec(`INSERT INTO interaction_hook_operations VALUES(?,?,'disable','',0,?,'pending',?,65536)`, id, session, raw, raw)
		return e
	}
	for j := 0; j < 4; j++ {
		for i := 0; i < 64; i++ {
			if e = seed(fmt.Sprint("s", j), fmt.Sprintf("p%d-%d", j, i)); e != nil {
				t.Fatal(j, i, e)
			}
		}
		if e = seed(fmt.Sprint("s", j), fmt.Sprint("local-over", j)); e == nil {
			t.Fatal("65th operation")
		}
	}
	if e = seed("s4", "global-over"); e == nil {
		t.Fatal("257th operation")
	}
	if _, e = db.SQL.Exec(`UPDATE interaction_hook_operations SET reserved_bytes=0 WHERE id='p0-0'`); e == nil {
		t.Fatal("reservation refund")
	}
	if _, e = db.SQL.Exec(`DELETE FROM interaction_hook_operations WHERE id='p0-0'`); e == nil {
		t.Fatal("purge")
	}
}
