//go:build linux

package interaction

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func platformExecCapability() Capability {
	return Capability{Enabled: true, Reason: NonSandboxExecWarning + " Linux pinned executable/cwd, PID-safe direct-child kill and wait; no descendant cleanup or containment."}
}

// Pin a regular executable before reading, without opening a device/FIFO for I/O.
func openExecFile(name string) (*os.File, string, string, error) {
	fd, err := unix.Open(name, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", "", err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return nil, "", "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0111 == 0 || st.Size > 128<<20 {
		return nil, "", "", ErrInvalid
	}
	f, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		return nil, "", "", err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, io.LimitReader(f, (128<<20)+1))
	if err != nil {
		f.Close()
		return nil, "", "", err
	}
	identity := fmt.Sprintf("linux:%d:%d:%d:%d:%d:%d:%d", st.Dev, st.Ino, st.Mode, st.Size, st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Nsec)
	return f, identity, fmt.Sprintf("%x", hash.Sum(nil)), nil
}
func inspectExec(r *ExecReview) error {
	program, err := filepath.EvalSymlinks(r.Program)
	if err != nil {
		return err
	}
	cwd, err := filepath.EvalSymlinks(r.Cwd)
	if err != nil {
		return err
	}
	f, identity, hash, err := openExecFile(program)
	if err != nil {
		return err
	}
	f.Close()
	dir, err := openExecDir(cwd)
	if err != nil {
		return err
	}
	defer dir.Close()
	st, err := dir.Stat()
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return ErrInvalid
	}
	dirID, err := rootIdentity(int(dir.Fd()))
	if err != nil {
		return err
	}
	r.Program = program
	r.Cwd = cwd
	r.ExecutableIdentity = identity
	r.ExecutableSHA256 = hash
	r.CwdIdentity = dirID
	return nil
}

func openExecDir(name string) (*os.File, error) {
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
