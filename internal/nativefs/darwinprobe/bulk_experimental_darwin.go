//go:build darwin && cgo && nativeprobe_darwin_bulk

package darwinprobe

import (
	"context"
	"errors"
)

// BulkEntry is transient metadata, never authorization for a content open.
type BulkEntry struct {
	Name      string
	Inode     uint64
	Directory bool
}
type BulkObservation struct {
	Entries           []BulkEntry
	Calls, RawRecords int
}

var errBulkNotImplemented = errors.New("experimental getattrlistbulk enumeration not implemented")

// ExperimentalObserveEntries is intentionally refusing at the native RED stage.
// Default CGO=0 builds do not contain this API.
func (d *DirectoryObservation) ExperimentalObserveEntries(context.Context) (BulkObservation, error) {
	return BulkObservation{}, errBulkNotImplemented
}
