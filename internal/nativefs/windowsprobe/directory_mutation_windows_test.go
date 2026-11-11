//go:build windows

package windowsprobe

import (
	"os"
	"path/filepath"
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

// forceFixtureLastWrite is explicit test timestamp control, not evidence that
// every sibling mutation naturally exposes a timestamp change immediately.
// WRITE_ATTRIBUTES is metadata access, not content-write access. This trusted
// fixture handle is never passed into production acquisition.
func forceFixtureLastWrite(t *testing.T, path string, observed windows.Handle, before snapshot) snapshot {
	t.Helper()
	const oneSecond = int64(10_000_000) // FILETIME units are 100 ns
	if before.Basic.Write > int64(^uint64(0)>>1)-oneSecond {
		t.Fatal("fixture timestamp overflows")
	}
	desired := before.Basic.Write + oneSecond
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatalf("trusted timestamp-control handle: %v", err)
	}
	stamp := windows.Filetime{LowDateTime: uint32(desired), HighDateTime: uint32(uint64(desired) >> 32)}
	setErr := windows.SetFileTime(h, nil, nil, &stamp)
	closeErr := windows.CloseHandle(h)
	if setErr != nil || closeErr != nil {
		t.Fatalf("explicit fixture LastWrite control: set=%v close=%v", setErr, closeErr)
	}
	after, err := inspect(observed)
	if err != nil {
		t.Fatal(err)
	}
	if after.Basic.Write != desired {
		t.Fatalf("explicit LastWrite control not observed: want=%d got=%d", desired, after.Basic.Write)
	}
	if before.ID != after.ID || before.Basic.Creation != after.Basic.Creation || before.Basic.Attributes != after.Basic.Attributes || before.Tag != after.Tag {
		t.Fatal("timestamp-control fixture changed identity/creation/attributes/tag")
	}
	t.Logf("explicit test LastWrite control: before=%d after=%d; not a claim of naturally visible sibling timestamps", before.Basic.Write, after.Basic.Write)
	return after
}

func requireStableDirectoryIdentity(t *testing.T, before, after snapshot) {
	t.Helper()
	const forbidden = windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_DEVICE | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	if before.ID != after.ID || before.Basic.Creation != after.Basic.Creation || before.Basic.Attributes != after.Basic.Attributes || before.Standard.Directory != after.Standard.Directory || after.Standard.Directory == 0 || before.Tag != after.Tag || before.Standard.DeletePending != after.Standard.DeletePending || after.Standard.DeletePending != 0 {
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
	created := forceFixtureLastWrite(t, parent, guard, before)
	requireStableDirectoryIdentity(t, before, created)
	if unchanged(before, created) {
		t.Fatal("fixture did not establish controlled create-stage full-snapshot change; cannot claim unchanged rejection")
	}
	if err := os.Remove(sibling); err != nil {
		t.Fatalf("delete sibling while directory IO guard retained: %v", err)
	}
	deleted := forceFixtureLastWrite(t, parent, guard, created)
	requireStableDirectoryIdentity(t, created, deleted)
	if unchanged(created, deleted) {
		t.Fatal("fixture did not establish controlled delete-stage full-snapshot change; cannot claim unchanged rejection")
	}
	if got, err := os.ReadFile(filepath.Join(parent, "Existing")); err != nil || string(got) != "unchanged" {
		t.Fatalf("unrelated sibling mutation changed existing content: %q %v", got, err)
	}
}

func TestNativeDirectorySiblingMutationMetadataToIOIdentityAcceptance(t *testing.T) {
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
	after := forceFixtureLastWrite(t, parent, metadata, before)
	requireStableDirectoryIdentity(t, before, after)
	if unchanged(before, after) {
		t.Fatal("fixture did not establish changed metadata; cannot attribute refusal to snapshot comparison")
	}
	guard, actual, err := acquireIOGuard(root.h, "Guarded", before)
	if err != nil || guard == 0 {
		if guard != 0 {
			windows.CloseHandle(guard)
		}
		t.Fatalf("identity-qualified directory traversal rejected sibling mutation plus explicitly controlled LastWrite change: %v", err)
	}
	defer func() {
		if err := windows.CloseHandle(guard); err != nil {
			t.Error(err)
		}
	}()
	requireStableDirectoryIdentity(t, before, actual)
	// ObserveEntries' final consistency comparison must remain the stricter one.
	if unchanged(before, actual) {
		t.Fatal("strict enumeration endpoint comparison lost observed mutation")
	}
}
