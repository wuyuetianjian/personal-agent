//go:build darwin

package darwinprobe

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// DirectoryObservation owns a bounded lineage of pinned directories.
// Close is mandatory for acquired observations. The zero value has no owned
// descriptors and ObserveRegular returns os.ErrClosed. Do not copy a
// DirectoryObservation.
type DirectoryObservation struct {
	mu     sync.Mutex
	fds    []int
	dev    int32
	fsid   unix.Fsid
	closed bool
}

// RegularObservation is transient metadata, not a durable identity or approval
// token. No leaf descriptor or contents are acquired. FSID is the pinned parent
// filesystem observed during directory acquisition; Device is the leaf's
// reported device. Their agreement cannot eliminate concurrent namespace races.
type RegularObservation struct {
	Device int32
	Inode  uint64
	FSID   [2]int32
	Size   int64
}

// ObserveDirectory duplicates an explicitly trusted already-open directory and
// walks zero or more single directory components. The caller retains ownership
// of trusted. Actual APFS is required, with matching observed device and FSID
// for every retained directory. This is not atomic beneath/no-mount resolution.
func ObserveDirectory(trusted *os.File, components []string) (_ *DirectoryObservation, err error) {
	if err = validateComponents(components); err != nil {
		return nil, err
	}
	if trusted == nil {
		return nil, fmt.Errorf("nil trusted directory")
	}
	raw, err := trusted.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var dupErr error
	if err = raw.Control(func(source uintptr) { fd, dupErr = unix.FcntlInt(source, unix.F_DUPFD_CLOEXEC, 0) }); err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, dupErr
	}
	d := &DirectoryObservation{fds: []int{fd}}
	defer func() {
		if err != nil {
			err = errors.Join(err, d.Close())
		}
	}()
	var st unix.Stat_t
	var fs unix.Statfs_t
	if err = checkDirectory(fd, &st, &fs); err != nil {
		return nil, err
	}
	d.dev, d.fsid = st.Dev, fs.Fsid
	for _, part := range components {
		next, e := unix.Openat(d.fds[len(d.fds)-1], part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		d.fds = append(d.fds, next)
		if err = checkDirectory(next, &st, &fs); err != nil {
			return nil, err
		}
		if st.Dev != d.dev || fs.Fsid != d.fsid {
			return nil, fmt.Errorf("observed directory filesystem mismatch")
		}
	}
	return d, nil
}
func checkDirectory(fd int, st *unix.Stat_t, fs *unix.Statfs_t) error {
	if err := unix.Fstat(fd, st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("trusted handle is not a directory")
	}
	if err := unix.Fstatfs(fd, fs); err != nil {
		return err
	}
	if unix.ByteSliceToString(fs.Fstypename[:]) != "apfs" {
		return fmt.Errorf("directory observation requires APFS")
	}
	return nil
}

// ObserveRegular observes one leaf without opening it. It refuses symlinks,
// nonregular types, link counts other than one, negative sizes, and sizes above
// maxBytes. These checks describe one stat result only, not future file access.
func (d *DirectoryObservation) ObserveRegular(name string, maxBytes int64) (RegularObservation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || len(d.fds) == 0 {
		return RegularObservation{}, os.ErrClosed
	}
	if err := validateComponent(name); err != nil {
		return RegularObservation{}, err
	}
	if maxBytes < 0 {
		return RegularObservation{}, fmt.Errorf("negative size limit")
	}
	var st unix.Stat_t
	if err := unix.Fstatat(d.fds[len(d.fds)-1], name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return RegularObservation{}, err
	}
	if st.Dev != d.dev || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size < 0 || st.Size > maxBytes {
		return RegularObservation{}, fmt.Errorf("leaf observation refused: device, type, links or size")
	}
	return RegularObservation{Device: st.Dev, Inode: st.Ino, FSID: d.fsid.Val, Size: st.Size}, nil
}

// Close releases every retained descriptor, even if one close fails. It reports
// joined errors and never retries a descriptor close (the number may be reused).
// Operations and Close are serialized. Closing a zero value succeeds once as a
// no-op. A repeated Close returns os.ErrClosed, including for the zero value.
func (d *DirectoryObservation) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return os.ErrClosed
	}
	d.closed = true
	var errs []error
	for i := len(d.fds) - 1; i >= 0; i-- {
		if err := unix.Close(d.fds[i]); err != nil {
			errs = append(errs, err)
		}
	}
	d.fds = nil
	return errors.Join(errs...)
}
