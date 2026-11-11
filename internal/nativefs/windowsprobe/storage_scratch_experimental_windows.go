//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Private experimental contract only. Deliberate refusal until native RED.
var errScratchNotImplemented = errors.New("owned scratch adapter not implemented")
var errScratchBusy = errors.New("scratch store busy")
var errScratchClosed = errors.New("scratch object closed")
var errScratchLimit = errors.New("scratch limit exceeded before IO")
var errScratchUncertain = errors.New("scratch IO outcome uncertain")

type scratchLimits struct {
	Files                 int
	FileBytes, TotalBytes int64
	IOBytes               int
}
type scratchHooks struct {
	afterCreate func(windows.Handle) error
	write       func(windows.Handle, []byte) (uint32, error)
	sync        func(windows.Handle) error
	truncate    func(windows.Handle, int64) error
	close       func(windows.Handle) error
}

// root is a fully independent duplicate including every original ancestor.
type scratchStore struct{ root *Root }
type scratchFile struct{ h windows.Handle }

func newScratchStore(*Root, scratchLimits, scratchHooks) (*scratchStore, error) {
	return nil, errScratchNotImplemented
}
func (*scratchStore) Create() (*scratchFile, error)     { return nil, errScratchNotImplemented }
func (*scratchStore) Close() error                      { return errScratchNotImplemented }
func (*scratchFile) ReadAt([]byte, int64) (int, error)  { return 0, errScratchNotImplemented }
func (*scratchFile) WriteAt([]byte, int64) (int, error) { return 0, errScratchNotImplemented }
func (*scratchFile) Truncate(int64) error               { return errScratchNotImplemented }
func (*scratchFile) Size() (int64, error)               { return 0, errScratchNotImplemented }
func (*scratchFile) Sync() error                        { return errScratchNotImplemented }
func (*scratchFile) Close() error                       { return errScratchNotImplemented }
