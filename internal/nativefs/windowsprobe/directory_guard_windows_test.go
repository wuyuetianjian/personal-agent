//go:build windows

package windowsprobe

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func directoryGuardSnapshot() snapshot {
	return snapshot{
		ID:       idInfo{Volume: 1, ID: [16]byte{2}},
		Basic:    basicInfo{Creation: 10, Access: 20, Write: 30, Change: 40, Attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
		Standard: standardInfo{Allocation: 4096, Size: 0, Links: 1, Directory: 1},
		Tag:      tagInfo{Attributes: windows.FILE_ATTRIBUTE_DIRECTORY},
	}
}

func TestNativeSameDirectoryGuardMutableMetadata(t *testing.T) {
	base := directoryGuardSnapshot()
	if !sameDirectoryGuard(base, base) {
		t.Fatal("valid identical directory rejected")
	}
	mutations := map[string]func(*snapshot){
		"access":          func(s *snapshot) { s.Basic.Access++ },
		"write":           func(s *snapshot) { s.Basic.Write++ },
		"change":          func(s *snapshot) { s.Basic.Change++ },
		"allocation":      func(s *snapshot) { s.Standard.Allocation += 4096 },
		"size":            func(s *snapshot) { s.Standard.Size = 128 },
		"directory_links": func(s *snapshot) { s.Standard.Links++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			other := base
			mutate(&other)
			if !sameDirectoryGuard(base, other) || !sameDirectoryGuard(other, base) {
				t.Fatal("directory guard rejected allowed mutable metadata")
			}
			// Last access is intentionally ignored by the old comparison already.
			if name != "access" && unchanged(base, other) {
				t.Fatal("strict endpoint comparison stopped detecting mutation")
			}
		})
	}
}

func TestNativeSameDirectoryGuardRejectsIdentityAndPolicyChanges(t *testing.T) {
	base := directoryGuardSnapshot()
	mutations := map[string]func(*snapshot){
		"volume":                      func(s *snapshot) { s.ID.Volume++ },
		"id_high_byte":                func(s *snapshot) { s.ID.ID[15]++ },
		"not_directory":               func(s *snapshot) { s.Standard.Directory = 0 },
		"delete_pending":              func(s *snapshot) { s.Standard.DeletePending = 1 },
		"creation":                    func(s *snapshot) { s.Basic.Creation++ },
		"safe_basic_attribute_change": func(s *snapshot) { s.Basic.Attributes |= windows.FILE_ATTRIBUTE_HIDDEN },
		"safe_tag_attribute_change":   func(s *snapshot) { s.Tag.Attributes |= windows.FILE_ATTRIBUTE_HIDDEN },
		"nonzero_tag":                 func(s *snapshot) { s.Tag.Tag = 1 },
		"negative_size":               func(s *snapshot) { s.Standard.Size = -1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			other := base
			mutate(&other)
			if sameDirectoryGuard(base, other) || sameDirectoryGuard(other, base) {
				t.Fatal("directory guard accepted identity/policy change")
			}
		})
	}
}

func TestNativeSameDirectoryGuardRejectsIdenticalInvalidSnapshots(t *testing.T) {
	cases := map[string]func(*snapshot){
		"file":           func(s *snapshot) { s.Standard.Directory = 0 },
		"delete_pending": func(s *snapshot) { s.Standard.DeletePending = 1 },
		"nonzero_tag":    func(s *snapshot) { s.Tag.Tag = 1 },
		"negative_size":  func(s *snapshot) { s.Standard.Size = -1 },
	}
	forbidden := map[string]uint32{
		"reparse":        windows.FILE_ATTRIBUTE_REPARSE_POINT,
		"offline":        windows.FILE_ATTRIBUTE_OFFLINE,
		"device":         windows.FILE_ATTRIBUTE_DEVICE,
		"recall_on_open": windows.FILE_ATTRIBUTE_RECALL_ON_OPEN,
		"recall_on_data": windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS,
	}
	for name, bit := range forbidden {
		cases["basic_"+name] = func(s *snapshot) { s.Basic.Attributes |= bit }
		cases["tag_"+name] = func(s *snapshot) { s.Tag.Attributes |= bit }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := directoryGuardSnapshot()
			mutate(&s)
			if sameDirectoryGuard(s, s) {
				t.Fatal("identical invalid snapshots were accepted by equality alone")
			}
		})
	}
}

func TestNativeFileSnapshotConsistencyRemainsStrict(t *testing.T) {
	base := directoryGuardSnapshot()
	base.Standard.Directory = 0
	base.Basic.Attributes = windows.FILE_ATTRIBUTE_NORMAL
	base.Tag.Attributes = windows.FILE_ATTRIBUTE_NORMAL
	mutations := map[string]func(*snapshot){
		"write":      func(s *snapshot) { s.Basic.Write++ },
		"change":     func(s *snapshot) { s.Basic.Change++ },
		"allocation": func(s *snapshot) { s.Standard.Allocation++ },
		"size":       func(s *snapshot) { s.Standard.Size++ },
		"links":      func(s *snapshot) { s.Standard.Links++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			other := base
			mutate(&other)
			if unchanged(base, other) {
				t.Fatal("file comparison accepted content-associated metadata change")
			}
			if sameDirectoryGuard(base, other) {
				t.Fatal("directory comparator admitted file snapshots")
			}
		})
	}
}

func TestNativeFileMutationMetadataToIOStillRefuses(t *testing.T) {
	dir, root := fixture(t)
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := openRelative(root.h, "file", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	}()
	before, err := inspect(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after with different size"), 0600); err != nil {
		t.Fatal(err)
	}
	after := forceFixtureLastWrite(t, path, h, before)
	if before.ID != after.ID || before.Standard.Directory != 0 || after.Standard.Directory != 0 {
		t.Fatal("file fixture lost same-object identity")
	}
	if unchanged(before, after) {
		t.Fatal("file fixture did not establish metadata change")
	}
	guard, _, err := acquireIOGuard(root.h, "file", before)
	if guard != 0 {
		windows.CloseHandle(guard)
		t.Fatal("file mutation returned usable IO guard")
	}
	if err == nil {
		t.Fatal("file mutation passed IO guard consistency check")
	}
}
