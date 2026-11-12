//go:build darwin && nativeprobe_darwin_ownedfd

package darwinprobe

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Retained only to distinguish historical RED evidence from actual failures.
var errOwnedFDUnimplemented = errors.New("explicit experimental trusted owned-FD read not implemented")

const ownedFDMaxBytes = 1 << 20
const ownedFDChunk = 64 << 10
const ownedFDMaxCalls = 32

// ReadTrustedRegular consumes only a caller-authorized already-readable object.
// It accepts no path, obtains no source authorization and is not a snapshot API.
// Bytes are returned only after the owned duplicate has been closed successfully.
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

func ownedFDPanic(p any) error {
	e := errors.New("trusted owned-FD operation panicked")
	if cause, ok := p.(error); ok {
		return errors.Join(e, cause)
	}
	return e
}
func ownedFDClose(fd int, closeFD func(int) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = ownedFDPanic(p)
		}
	}()
	return closeFD(fd) // One attempt, including when its result is uncertain.
}
func readTrustedRegularWithOps(ctx context.Context, source *os.File, maxBytes int64, ops ownedFDOps) (out []byte, err error) {
	if ctx == nil || source == nil || maxBytes < 0 || maxBytes > ownedFDMaxBytes || ops.pread == nil || ops.close == nil {
		return nil, errors.New("invalid trusted owned-FD input or limit")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	fd := -1
	defer func() {
		if p := recover(); p != nil {
			err = errors.Join(err, ownedFDPanic(p))
		}
		if fd >= 0 {
			err = errors.Join(err, ownedFDClose(fd, ops.close))
			fd = -1
		}
		err = errors.Join(err, ctx.Err())
		if err != nil {
			out = nil
		}
	}()
	raw, err := source.SyscallConn()
	if err != nil {
		return nil, err
	}
	var dupErr error
	err = raw.Control(func(sourceFD uintptr) { fd, dupErr = unix.FcntlInt(sourceFD, unix.F_DUPFD_CLOEXEC, 0) })
	if err = errors.Join(err, dupErr); err != nil {
		return nil, err
	}
	if fd < 0 {
		return nil, errors.New("dup returned no owned descriptor")
	}
	// Control's source reference has ended. A hook may now wait for caller Close.
	if ops.afterDup != nil {
		if err = ops.afterDup(fd); err != nil {
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	before, fs, err := ownedFDObserve(fd, maxBytes)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	data := make([]byte, int(before.Size))
	offset, calls := 0, 0
	for offset < len(data) {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if calls >= ownedFDMaxCalls {
			return nil, errors.New("owned-FD pread budget exhausted")
		}
		end := min(offset+ownedFDChunk, len(data))
		calls++
		n, readErr := ops.pread(fd, data[offset:end], int64(offset))
		if readErr != nil {
			return nil, readErr
		} // No hidden EINTR retry.
		if n < 0 || n > end-offset {
			return nil, errors.New("invalid pread length")
		}
		if n == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		offset += n
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	after, fsAfter, err := ownedFDObserve(fd, maxBytes)
	if err != nil {
		return nil, err
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Nlink != after.Nlink || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || before.Flags != after.Flags || fs != fsAfter {
		return nil, errors.New("trusted owned-FD observation changed")
	}
	return data, nil // Deferred cleanup/cancellation check precedes caller delivery.
}
func ownedFDObserve(fd int, maxBytes int64) (st unix.Stat_t, fsid unix.Fsid, err error) {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return st, fsid, err
	}
	if flags != unix.O_RDONLY {
		return st, fsid, errors.New("owned-FD requires exact readonly status flags")
	}
	if err = unix.Fstat(fd, &st); err != nil {
		return st, fsid, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size < 0 || st.Size > maxBytes {
		return st, fsid, errors.New("owned-FD type, links or size refused")
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(fd, &fs); err != nil {
		return st, fsid, err
	}
	if unix.ByteSliceToString(fs.Fstypename[:]) != "apfs" {
		return st, fsid, errors.New("owned-FD requires APFS")
	}
	return st, fs.Fsid, nil
}
