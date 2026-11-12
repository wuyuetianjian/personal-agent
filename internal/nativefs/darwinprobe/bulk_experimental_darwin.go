//go:build darwin && cgo && nativeprobe_darwin_bulk

package darwinprobe

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Kept as a historical RED sentinel so qualification tests explicitly reject
// accidental restoration of the earlier unimplemented refusal stub.
var errBulkNotImplemented = errors.New("experimental getattrlistbulk enumeration not implemented")

type bulkNativeOps struct {
	query func(int, []byte) (int, error)
	close func(int) error
}

// ExperimentalObserveEntries returns only bounded directory-entry metadata.
// It cannot authorize a leaf open and is absent from default CGO=0 builds.
// No partial observations are returned on any reported error.
func (d *DirectoryObservation) ExperimentalObserveEntries(ctx context.Context) (BulkObservation, error) {
	return d.observeBulkWith(ctx, bulkNativeOps{query: queryNativeBulk, close: unix.Close})
}

// Injection is package-private and used to qualify error/cleanup boundaries.
// The exported experiment always uses the actual SDK query and unix.Close.
func (d *DirectoryObservation) observeBulkWith(ctx context.Context, ops bulkNativeOps) (BulkObservation, error) {
	if d == nil {
		return BulkObservation{}, os.ErrClosed
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || len(d.fds) == 0 {
		return BulkObservation{}, os.ErrClosed
	}
	if ctx == nil {
		return BulkObservation{}, errBulkRefused
	}
	if e := ctx.Err(); e != nil {
		return BulkObservation{}, e
	}
	endpoint := d.fds[len(d.fds)-1]
	var before unix.Stat_t
	var fs unix.Statfs_t
	if e := checkDirectory(endpoint, &before, &fs); e != nil {
		return BulkObservation{}, e
	}
	if before.Dev != d.dev || fs.Fsid != d.fsid || before.Flags&unix.SF_FIRMLINK != 0 {
		return BulkObservation{}, errBulkRefused
	}
	// Opening "." creates an independent cursor; dup would share scan state.
	fd, e := unix.Openat(endpoint, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return BulkObservation{}, e
	}
	// From here collectBulk owns cleanup, including initial qualification failure.
	check := func() error {
		for _, h := range []int{endpoint, fd} {
			var st unix.Stat_t
			var currentFS unix.Statfs_t
			if e := checkDirectory(h, &st, &currentFS); e != nil {
				return e
			}
			if currentFS.Fsid != d.fsid || !sameBulkDirectory(before, st) {
				return errBulkRefused
			}
		}
		return nil
	}
	return collectBulk(ctx, bulkOps{query: func(b []byte) (int, error) { return ops.query(fd, b) }, check: check, close: func() error { return ops.close(fd) }})
}

// Reading directory metadata can update atime. All other observation fields
// below remain strict, but equality is not a snapshot/absence-of-ABA proof.
func sameBulkDirectory(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Uid == b.Uid && a.Gid == b.Gid && a.Rdev == b.Rdev && a.Mtim == b.Mtim && a.Ctim == b.Ctim && a.Btim == b.Btim && a.Size == b.Size && a.Blocks == b.Blocks && a.Flags == b.Flags && a.Gen == b.Gen
}
