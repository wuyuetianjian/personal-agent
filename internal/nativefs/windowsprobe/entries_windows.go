//go:build windows

package windowsprobe

import (
	"context"
	"errors"
	"sort"
	"unsafe"

	"golang.org/x/sys/windows"
)

var queryDirectory = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryDirectoryFileEx")

// ObserveEntries returns bounded, nonrecursive observations of one descendant
// directory. At least one component is required. Entries do not authorize reads,
// prove single links, or form a snapshot. Synchronous calls cannot enforce hard
// cancellation. Unsafe names/attributes and exhausted budgets return no entries.
func (r *Root) ObserveEntries(ctx context.Context, components []string) (entries []Entry, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A duplicate of r.h would share the caller's enumeration cursor and first
	// search expression. OpenDirectory's final NtCreateFile yields a fresh file
	// object, independently guarded and private to this operation.
	scan, err := r.OpenDirectory(components)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := scan.Close(); closeErr != nil {
			entries = nil
			err = errors.Join(err, closeErr)
		}
	}()
	if err := queryDirectory.Find(); err != nil {
		return nil, err
	}
	before, err := inspect(scan.h)
	if err != nil {
		return nil, err
	}
	// Explicit uint64 storage satisfies the documented native alignment.
	var storage [8192]uint64
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), 65536)
	seen := make(map[string]bool)
	rawCount := 0
	for call := 0; call < 16; call++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var io windows.IO_STATUS_BLOCK
		flags := uintptr(0)
		if call == 0 {
			flags = 1 // SL_RESTART_SCAN
		}
		status, _, _ := queryDirectory.Call(uintptr(scan.h), 0, 0, 0,
			uintptr(unsafe.Pointer(&io)), uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer)), 60, flags, 0) // FileIdExtdDirectoryInformation; no filter
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Nonpending routine status is authoritative even if IOSB.Status differs.
		length, done, err := directoryQueryCompletion(uint32(status), io.Information, len(buffer))
		if err != nil {
			return nil, err
		}
		if done {
			after, err := inspect(scan.h)
			if err != nil {
				return nil, err
			}
			if !unchanged(before, after) {
				return nil, errUnsafe
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			return entries, nil
		}
		page, raw, err := parseDirectoryRecords(buffer[:length])
		if err != nil {
			return nil, err
		}
		rawCount += raw
		if rawCount > 1024 || len(entries)+len(page) > 256 {
			return nil, errors.New("windowsprobe: directory observation limit exceeded")
		}
		for _, e := range page {
			if seen[e.Name] {
				return nil, errUnsafe
			}
			seen[e.Name] = true
		}
		entries = append(entries, page...)
	}
	return nil, errors.New("windowsprobe: directory query budget exhausted")
}
