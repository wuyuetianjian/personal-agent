package skill

import (
	"context"
	"path/filepath"
	"testing"

	"agent/internal/storage"
)

func TestStoreImportsVersionsAndStatus(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	first := Manifest{ID: "skill.local", Version: "1.0.0", Name: "local", Description: "local lookup", Status: StatusDraft}
	if err := store.Import(ctx, first, "test"); err != nil {
		t.Fatalf("Import(first) error = %v", err)
	}
	second := first
	second.Version = "1.1.0"
	second.Status = StatusActive
	if err := store.Import(ctx, second, "test"); err != nil {
		t.Fatalf("Import(second) error = %v", err)
	}
	record, err := store.Get(ctx, "skill.local")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.ActiveVersion != "1.1.0" || record.Status != StatusActive {
		t.Fatalf("record=%#v, want active version 1.1.0", record)
	}
	versions, err := store.Versions(ctx, "skill.local")
	if err != nil {
		t.Fatalf("Versions() error = %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions=%#v, want 2", versions)
	}
	if err := store.Disable(ctx, "skill.local"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	record, err = store.Get(ctx, "skill.local")
	if err != nil {
		t.Fatalf("Get(disabled) error = %v", err)
	}
	if record.Status != StatusDisabled {
		t.Fatalf("record=%#v, want disabled", record)
	}
	if err := store.Enable(ctx, "skill.local", "1.0.0"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	manifest, err := store.GetManifest(ctx, "skill.local", "")
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}
	if manifest.Version != "1.0.0" || manifest.Status != StatusActive {
		t.Fatalf("manifest=%#v, want active 1.0.0", manifest)
	}
}
