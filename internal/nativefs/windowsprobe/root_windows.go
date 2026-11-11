//go:build windows

package windowsprobe

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OpenLocalRoot acquires a current directory object from a restricted absolute
// DOS path. The drive must map directly to a supported local NTFS volume name;
// forwarded paths, network roots and ancestor reparse points are not followed.
// This is acquisition, not approval or a persistent identity. Device names are
// nonpersistent; authority comes from the qualified handles retained here.
func OpenLocalRoot(path string) (*Root, error) {
	drive, components, err := parseLocalRootPath(path)
	if err != nil {
		return nil, err
	}
	device, err := windows.UTF16PtrFromString(drive)
	if err != nil {
		return nil, err
	}
	var targets [32768]uint16
	n, err := windows.QueryDosDevice(device, &targets[0], uint32(len(targets)))
	if err != nil {
		return nil, fmt.Errorf("windowsprobe: query drive mapping: %w", err)
	}
	if n == 0 || n > uint32(len(targets)) {
		return nil, errUnsafe
	}
	// Only the first string is the current mapping; later strings may describe
	// undeleted prior mappings. Do not try later mappings when the first refuses.
	end := 0
	for end < int(n) && targets[end] != 0 {
		end++
	}
	if end == 0 || end == int(n) {
		return nil, errUnsafe
	}
	anchor, err := physicalVolumeRoot(windows.UTF16ToString(targets[:end]))
	if err != nil {
		return nil, err
	}
	return openQualifiedAnchor(anchor, components)
}

// Shared acquisition body; caller has already qualified the transient locator.
func openQualifiedAnchor(anchor string, components []string) (*Root, error) {
	name, err := windows.NewNTUnicodeString(anchor)
	if err != nil {
		return nil, err
	}
	oa := windows.OBJECT_ATTRIBUTES{ObjectName: name}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var io windows.IO_STATUS_BLOCK
	// The trailing backslash selects the filesystem root directory, not a raw
	// volume. Use the existing type-neutral open flags, then NewRoot checks
	// actual directory type. Do not re-resolve the DOS drive, elevate, use backup intent, or
	// request DELETE/SHARE_DELETE. Trusted OS device namespace is a premise.
	err = windows.NtCreateFile(&handle,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&oa, &io, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("windowsprobe: open volume root: %w", err)
	}
	root, err := NewRoot(handle)
	if closeErr := windows.CloseHandle(handle); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		if root != nil {
			err = errors.Join(err, root.Close())
		}
		return nil, fmt.Errorf("windowsprobe: qualify volume root: %w", err)
	}
	if len(components) == 0 {
		return root, nil
	}
	selected, err := root.openDirectoryNamespace(components)
	if closeErr := root.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		if selected != nil {
			err = errors.Join(err, selected.Close())
		}
		return nil, fmt.Errorf("windowsprobe: acquire root ancestry: %w", err)
	}
	// Only initial host root selection starts a new relative-depth budget.
	// Keep all initial ancestors. A later OpenDirectory never resets depth:
	// at most volume + 32 initial + 32 relative handles belong to one lineage.
	selected.depth = 0
	return selected, nil
}
