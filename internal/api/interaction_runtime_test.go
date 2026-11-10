package api

import (
	"agent/internal/storage"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInteractionRuntimeExclusiveFreshNamespace(t *testing.T) {
	root := t.TempDir()
	r, err := OpenInteractionRuntime(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenInteractionRuntime(context.Background(), root); err == nil {
		t.Fatal("second writer")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = OpenInteractionRuntime(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	unknown := t.TempDir()
	os.Mkdir(filepath.Join(unknown, "runtime-rebuild-v1"), 0700)
	os.WriteFile(filepath.Join(unknown, "runtime-rebuild-v1", "agent.db"), []byte("old preview"), 0600)
	if _, err = OpenInteractionRuntime(context.Background(), unknown); err == nil {
		t.Fatal("unknown DB adopted")
	}
	b, _ := os.ReadFile(filepath.Join(unknown, "runtime-rebuild-v1", "agent.db"))
	if string(b) != "old preview" {
		t.Fatal("old DB changed")
	}
}
func TestProviderBindingExcludesCredentialRotationButPinsDestination(t *testing.T) {
	a := testSelection()
	b := a
	b.Model.Provider.APIKeyEnv = "ROTATED"
	if a.Binding() != b.Binding() {
		t.Fatal("credential rotation blocked")
	}
	b.Endpoint = "http://127.0.0.1:9999/v1"
	if a.Binding() == b.Binding() {
		t.Fatal("destination not pinned")
	}
	b = a
	b.Model.Provider.RequirePrivacyGateway = true
	if a.Binding() == b.Binding() {
		t.Fatal("trust policy not pinned")
	}
}
func TestRuntimeCanonicalParentAliasStillHasOneLock(t *testing.T) {
	parent := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(parent, alias); e != nil {
		t.Skip(e)
	}
	r, e := OpenInteractionRuntime(context.Background(), alias)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if _, e = OpenInteractionRuntime(context.Background(), parent); e == nil {
		t.Fatal("alias bypassed exclusive lock")
	}
}

func TestRuntimeRefusesUnknownSchemaBeforeWritableOpen(t *testing.T) {
	for name, mutation := range map[string]string{
		"future": `INSERT INTO schema_migrations(version) VALUES('9999_private_future.sql')`,
		"blob":   `UPDATE schema_migrations SET version=CAST(version AS BLOB) WHERE version='0023_job_execution_policy.sql'`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			r, err := OpenInteractionRuntime(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.Directory, "agent.db")
			r.Close()
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`PRAGMA journal_mode=DELETE; INSERT INTO interaction_sessions(id) VALUES('retained'); ` + mutation); err != nil {
				t.Fatal(err)
			}
			db.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if r, err = OpenInteractionRuntime(ctx, root); err == nil {
				r.Close()
				t.Fatal("future database accepted")
			}
			if !errors.Is(err, storage.ErrIncompatibleSchema) {
				t.Error("wrong refusal", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("read-only refusal changed database bytes (including journal mode)")
			}
			for _, suffix := range []string{"-wal", "-shm"} {
				if _, err = os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refusal created sidecar", suffix, err)
				}
			}
			// A refused open must release the process lock, not permanently quarantine it.
			if _, err = OpenInteractionRuntime(ctx, root); !errors.Is(err, storage.ErrIncompatibleSchema) {
				t.Fatal(err)
			}
		})
	}
}

// Simulate a terminated owner after committed WAL writes, with no concurrent
// writer. os.Exit intentionally bypasses SQLite close/checkpoint.
func TestRuntimeWALPreflightHelper(t *testing.T) {
	root := os.Getenv("AGENT_OWNED_WAL_FIXTURE")
	if root == "" {
		return
	}
	r, err := OpenInteractionRuntime(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.DB.SQL.Exec(`PRAGMA wal_autocheckpoint=0; INSERT INTO interaction_sessions(id) VALUES('wal-retained')`); err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("AGENT_OWNED_WAL_UNKNOWN") {
	case "blob":
		if _, err = r.DB.SQL.Exec(`UPDATE schema_migrations SET version=CAST(version AS BLOB) WHERE version='0023_job_execution_policy.sql'`); err != nil {
			t.Fatal(err)
		}
	case "1":
		if _, err = r.DB.SQL.Exec(`INSERT INTO schema_migrations(version) VALUES('9999_future.sql')`); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(0)
}

func TestRuntimeWALPreflightIncludesCommittedLedger(t *testing.T) {
	for _, tc := range []struct {
		identity   string
		missingSHM bool
	}{{"", false}, {"1", false}, {"", true}, {"1", true}, {"blob", false}, {"blob", true}} {
		unknown := tc.identity != ""
		t.Run(fmt.Sprintf("identity=%s/missing_shm=%t", tc.identity, tc.missingSHM), func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeWALPreflightHelper$")
			cmd.Env = append(os.Environ(), "AGENT_OWNED_WAL_FIXTURE="+root)
			if unknown {
				cmd.Env = append(cmd.Env, "AGENT_OWNED_WAL_UNKNOWN="+tc.identity)
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("owned crash fixture: %v %s", err, output)
			}
			path := filepath.Join(root, "runtime-rebuild-v1", "agent.db")
			mainBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			walBefore, err := os.ReadFile(path + "-wal")
			if err != nil || len(walBefore) == 0 {
				t.Fatal("missing committed WAL", err)
			}
			if tc.missingSHM {
				if err = os.Remove(path + "-shm"); err != nil {
					t.Fatal(err)
				}
			}
			r, err := OpenInteractionRuntime(context.Background(), root)
			if unknown {
				if err == nil {
					r.Close()
					t.Fatal("future WAL ledger accepted")
				}
				if !errors.Is(err, storage.ErrIncompatibleSchema) {
					t.Fatal(err)
				}
				mainAfter, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(mainBefore, mainAfter) {
					t.Fatal("refusal checkpointed/changed main database", e)
				}
				walAfter, e := os.ReadFile(path + "-wal")
				if e != nil || !bytes.Equal(walBefore, walAfter) {
					t.Fatal("refusal changed committed WAL", e)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				var id string
				if err = r.DB.SQL.QueryRow(`SELECT id FROM interaction_sessions WHERE id='wal-retained'`).Scan(&id); err != nil {
					t.Fatal("committed WAL lost", err)
				}
			}
		})
	}
}
