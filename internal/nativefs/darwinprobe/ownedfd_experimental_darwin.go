//go:build darwin && nativeprobe_darwin_ownedfd

package darwinprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

var errOwnedFDUnimplemented = errors.New("explicit experimental trusted owned-FD read not implemented")

// ReadTrustedRegular consumes only a caller-authorized already-readable object.
// It accepts no path, obtains no source authorization and is not a snapshot API.
// RED: the experiment refuses before acquiring any descriptor or reading bytes.
func ReadTrustedRegular(ctx context.Context, source *os.File, maxBytes int64) ([]byte, error) {
	return readTrustedRegularWithOps(ctx, source, maxBytes, ownedFDSystemOps())
}

// Per-call, package-private fault seam; no global native overrides or authority.
type ownedFDOps struct {
	afterDup func(int) error
	pread    func(int, []byte, int64) (int, error)
	close    func(int) error
}

func ownedFDSystemOps() ownedFDOps { return ownedFDOps{pread: unix.Pread, close: unix.Close} }
func readTrustedRegularWithOps(context.Context, *os.File, int64, ownedFDOps) ([]byte, error) {
	return nil, errOwnedFDUnimplemented
}
