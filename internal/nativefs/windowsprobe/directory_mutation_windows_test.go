//go:build windows

package windowsprobe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func inspectMutationDirectory(t *testing.T, h windows.Handle) snapshot {
	t.Helper()
	s, err := inspect(h)
	if err != nil {
		t.Fatalf("inspect mutation fixture: %v", err)
	}
	if s.Standard.Directory == 0 {
		t.Fatal("mutation fixture is not a directory")
	}
	return s
}

func requireStableDirectoryIdentity(t *testing.T, before, after snapshot) {
	t.Helper()
	const forbidden = windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_DEVICE | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	if before.ID != after.ID || before.Standard.Directory != after.Standard.Directory || after.Standard.Directory == 0 || before.Tag != after.Tag || before.Standard.DeletePending != after.Standard.DeletePending || after.Standard.DeletePending != 0 {
		t.Fatalf("directory identity/type/tag/delete state changed: %s", snapshotChanges(before, after))
	}
	if before.Basic.Attributes&forbidden != 0 || after.Basic.Attributes&forbidden != 0 || before.Tag.Attributes&forbidden != 0 || after.Tag.Attributes&forbidden != 0 {
		t.Fatal("mutation fixture acquired forbidden attributes")
	}
	// Log all mutable metadata, including unchanged values, without object IDs or paths.
	t.Logf("directory metadata categories: %s", snapshotChanges(before, after))
	t.Logf("before basic=%+v standard=%+v tag=%+v; after basic=%+v standard=%+v tag=%+v", before.Basic, before.Standard, before.Tag, after.Basic, after.Standard, after.Tag)
}

func TestNativeDirectorySiblingMutationWithIOGuard(t *testing.T) {
	dir, root := fixture(t)
	parent := filepath.Join(dir, "Guarded")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	// This file is never touched: only its unrelated sibling is created/deleted.
	if err := os.WriteFile(filepath.Join(parent, "Existing"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	metadata, err := openRelative(root.h, "Guarded", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(metadata); err != nil {
			t.Error(err)
		}
	}()
	expected := inspectMutationDirectory(t, metadata)
	guard, _, err := acquireIOGuard(root.h, "Guarded", expected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(guard); err != nil {
			t.Error(err)
		}
	}()
	before := inspectMutationDirectory(t, guard)
	sibling := filepath.Join(parent, "Unrelated")
	if err := os.WriteFile(sibling, []byte("new sibling"), 0600); err != nil {
		t.Fatalf("create sibling while directory IO guard retained: %v", err)
	}
	created := inspectMutationDirectory(t, guard)
	requireStableDirectoryIdentity(t, before, created)
	if unchanged(before, created) {
		t.Fatal("fixture did not establish creation-induced full-snapshot change; cannot claim unchanged rejection")
	}
	if err := os.Remove(sibling); err != nil {
		t.Fatalf("delete sibling while directory IO guard retained: %v", err)
	}
	deleted := inspectMutationDirectory(t, guard)
	requireStableDirectoryIdentity(t, created, deleted)
	if unchanged(created, deleted) {
		t.Fatal("fixture did not establish deletion-induced full-snapshot change; cannot claim unchanged rejection")
	}
	if got, err := os.ReadFile(filepath.Join(parent, "Existing")); err != nil || string(got) != "unchanged" {
		t.Fatalf("unrelated sibling mutation changed existing content: %q %v", got, err)
	}
}

func TestNativeDirectorySiblingMutationMetadataToIORefusal(t *testing.T) {
	dir, root := fixture(t)
	parent := filepath.Join(dir, "Guarded")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	metadata, err := openRelative(root.h, "Guarded", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(metadata); err != nil {
			t.Error(err)
		}
	}()
	before := inspectMutationDirectory(t, metadata)
	// Explicit mutation boundary, no scheduler timing, sleep, tolerance or retry.
	if err := os.WriteFile(filepath.Join(parent, "Unrelated"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	after := inspectMutationDirectory(t, metadata)
	requireStableDirectoryIdentity(t, before, after)
	if unchanged(before, after) {
		t.Fatal("fixture did not establish changed metadata; cannot attribute refusal to snapshot comparison")
	}
	guard, _, err := acquireIOGuard(root.h, "Guarded", before)
	if guard != 0 {
		windows.CloseHandle(guard)
		t.Fatal("full-snapshot helper returned usable directory guard after observed metadata mutation")
	}
	if !errors.Is(err, errUnsafe) || !strings.Contains(err.Error(), "metadata-to-io changed") {
		t.Fatalf("expected current full-snapshot consistency refusal, not unrelated acquisition failure: %v", err)
	}
	t.Logf("current helper rejects same-directory identity after sibling mutation: %v", err)
}
