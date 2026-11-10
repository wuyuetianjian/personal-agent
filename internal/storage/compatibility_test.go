package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Capture catalog and every user-table record, not physical SQLite page layout.
func logicalSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out, tables []string
	rows, err := db.Query(`SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var typ, name, table, ddl string
		if err = rows.Scan(&typ, &name, &table, &ddl); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(typ, name, table, ddl))
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range tables {
		rows, err = db.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range values {
				ptr[i] = &values[i]
			}
			if err = rows.Scan(ptr...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(name, values))
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestMigrationRefusesUnrecognizedStateWithoutMutation(t *testing.T) {
	cases := map[string]string{
		"future":                  `INSERT INTO schema_migrations(version) VALUES('9999_future_private.sql')`,
		"renamed":                 `UPDATE schema_migrations SET version='0023_other_private.sql' WHERE version='0023_job_execution_policy.sql'`,
		"gap":                     `DELETE FROM schema_migrations WHERE version='0012_interaction.sql'`,
		"partial":                 `DELETE FROM schema_migrations WHERE version='0023_job_execution_policy.sql'`,
		"missing_table":           `DROP TABLE tasks`,
		"missing_index":           `DROP INDEX interaction_active_run`,
		"missing_trigger":         `DROP TRIGGER interaction_approval_immutable`,
		"changed_column":          `ALTER TABLE tasks ADD COLUMN unexpected TEXT`,
		"extra_schema":            `CREATE TABLE private_unrecognized(value TEXT); INSERT INTO private_unrecognized VALUES('sensitive fixture')`,
		"ledger_token_collision":  `CREATE TEMP TABLE copied_ledger AS SELECT * FROM schema_migrations; DROP TABLE schema_migrations; CREATE TABLE schema_migrations(version TEXTPRIMARYKEY, applied_at timestamp not null DEFAULT CURRENT_TIMESTAMP); INSERT INTO schema_migrations SELECT * FROM copied_ledger; DROP TABLE copied_ledger`,
		"ledger_shape":            `ALTER TABLE schema_migrations ADD COLUMN unexpected TEXT`,
		"invalid_timestamp":       `UPDATE schema_migrations SET applied_at='not a timestamp' WHERE version='0001_initial.sql'`,
		"unexpected_trigger":      `CREATE TRIGGER private_trigger AFTER INSERT ON tasks BEGIN DELETE FROM tasks; END`,
		"lookalike_internal_name": `CREATE TABLE sqliteXextra(value TEXT)`,
		"invalid_ledger":          `INSERT INTO schema_migrations(version) VALUES(NULL)`,
		"no_ledger":               `DROP TABLE schema_migrations`,
	}
	for name, mutation := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "fixture.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = Migrate(ctx, db.SQL); err != nil {
				t.Fatal(err)
			}
			if _, err = db.SQL.Exec(`INSERT INTO interaction_sessions(id) VALUES('retained')`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.SQL.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			before := logicalSnapshot(t, db.SQL)
			err = Migrate(ctx, db.SQL)
			if !errors.Is(err, ErrIncompatibleSchema) {
				t.Fatal("expected compatibility refusal", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "sensitive fixture") {
				t.Fatal("source details exposed", err)
			}
			if after := logicalSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
				t.Fatal("refusal changed logical source")
			}
		})
	}
}

func TestEveryEmbeddedPrefixUpgradesAndReopens(t *testing.T) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for prefix := 0; prefix <= len(entries); prefix++ {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			ctx := context.Background()
			db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "fixture.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if prefix > 0 {
				if _, err = db.SQL.Exec(`CREATE TABLE schema_migrations (version text primary key, applied_at timestamp not null DEFAULT CURRENT_TIMESTAMP)`); err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries[:prefix] {
					raw, err := migrationFiles.ReadFile("migrations/" + entry.Name())
					if err != nil {
						t.Fatal(err)
					}
					if err = applyMigration(ctx, db.SQL, entry.Name(), string(raw)); err != nil {
						t.Fatal(err)
					}
				}
				if err = db.CreateCompletedTask(ctx, Task{ID: "retained", Title: "fixture", Input: "owned input"}); err != nil {
					t.Fatal(err)
				}
			}
			if err = Migrate(ctx, db.SQL); err != nil {
				t.Fatal(err)
			}
			var applied int
			if err = db.SQL.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != len(entries) {
				t.Fatalf("upgrade ledger: %d, want %d: %v", applied, len(entries), err)
			}
			if _, err = db.SQL.Exec(`SELECT execution_policy FROM interaction_jobs LIMIT 1`); err != nil {
				t.Fatal("latest schema was not installed", err)
			}
			before := logicalSnapshot(t, db.SQL)
			if err = Migrate(ctx, db.SQL); err != nil {
				t.Fatal(err)
			}
			if after := logicalSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
				t.Fatal("latest reopen changed source")
			}
			if prefix > 0 {
				task, err := db.GetTask(ctx, "retained")
				if err != nil || task.Input != "owned input" {
					t.Fatal(task, err)
				}
			}
		})
	}
}

func TestApplyMigrationFailureRollsBackDDLDataAndLedger(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	before := logicalSnapshot(t, db.SQL)
	err = applyMigration(ctx, db.SQL, "owned-failed.sql", `CREATE TABLE partial(value TEXT); INSERT INTO interaction_sessions(id) VALUES('must rollback'); SELECT missing FROM absent`)
	if err == nil {
		t.Fatal("expected statement failure")
	}
	if after := logicalSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed source")
	}
	// Ledger failure occurs after successful DDL/DML and must also roll back.
	err = applyMigration(ctx, db.SQL, "0001_initial.sql", `CREATE TABLE partial(value TEXT); INSERT INTO interaction_sessions(id) VALUES('must rollback')`)
	if err == nil {
		t.Fatal("expected duplicate ledger failure")
	}
	if after := logicalSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
		t.Fatal("ledger failure changed source")
	}
}

func TestReadOnlyPreflightEscapesPathsAndDoesNotCreateMissingFiles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "owned # ? fixture.db")
	if err := ValidateSQLiteFile(ctx, path); !errors.Is(err, ErrIncompatibleSchema) {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight created missing file", err)
	}
	fixturePath := filepath.Join(filepath.Dir(path), "fixture.db")
	db, err := OpenSQLite(ctx, fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(fixturePath, path); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err = ValidateSQLiteFile(ctx, path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("preflight changed file permissions", err)
	}
}

func TestBlobMigrationIdentityRefusedWithoutMutation(t *testing.T) {
	// SQLite BLOB values can scan as Go strings but do not compare equal to the
	// TEXT identity used by migrationApplied. Reject, never cast/repair the row.
	for _, prefix := range []int{1, 23} {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			ctx := context.Background()
			db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "fixture.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.SQL.Exec(migrationLedgerDDL); err != nil {
				t.Fatal(err)
			}
			entries, err := migrationFiles.ReadDir("migrations")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries[:prefix] {
				raw, err := migrationFiles.ReadFile("migrations/" + entry.Name())
				if err != nil {
					t.Fatal(err)
				}
				if err = applyMigration(ctx, db.SQL, entry.Name(), string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.CreateCompletedTask(ctx, Task{ID: "retained", Title: "fixture", Input: "owned record"}); err != nil {
				t.Fatal(err)
			}
			if _, err = db.SQL.Exec(`UPDATE schema_migrations SET version=CAST(version AS BLOB) WHERE version=?`, entries[prefix-1].Name()); err != nil {
				t.Fatal(err)
			}
			before := logicalSnapshot(t, db.SQL)
			if err = ValidateSchema(ctx, db.SQL); !errors.Is(err, ErrIncompatibleSchema) {
				t.Errorf("BLOB identity passed validation: %v", err)
			}
			if err = Migrate(ctx, db.SQL); !errors.Is(err, ErrIncompatibleSchema) {
				t.Errorf("BLOB identity was not refused before migration: %v", err)
			}
			if after := logicalSnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
				t.Fatal("refusal changed BLOB ledger/schema/records")
			}
		})
	}
}
