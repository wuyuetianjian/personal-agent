package project

import (
	"agent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestProjectRoundTrip(t *testing.T) {
	db, _ := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db.SQL}
	want := Project{ID: "p1", Name: "Private", PrivacyClass: "confidential", AllowedSkills: []string{"s1"}, AllowedCapabilities: []string{"memory.search"}}
	if err := s.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowsSkill("s1") || got.AllowsSkill("s2") || got.PrivacyClass != "confidential" {
		t.Fatalf("got=%#v", got)
	}
}
