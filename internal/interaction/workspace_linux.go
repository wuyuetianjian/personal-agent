//go:build linux

package interaction

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func platformFileCapability() Capability {
	return Capability{Enabled: true, Reason: "Linux openat2 required at operation time; symlinks, mount crossings, hardlinks and nonregular files rejected"}
}
func rootIdentity(fd int) (string, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return "", err
	}
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME|unix.STATX_INO, &sx); err != nil {
		return "", fmt.Errorf("%w: persistent root identity needs statx birth time: %v", ErrCapabilityUnavailable, err)
	}
	if sx.Mask&unix.STATX_BTIME == 0 {
		return "", fmt.Errorf("%w: filesystem does not attest directory birth time", ErrCapabilityUnavailable)
	}
	return fmt.Sprintf("linux:%d:%d:%d:%d", st.Dev, st.Ino, sx.Btime.Sec, sx.Btime.Nsec), nil
}
func workspaceIdentity(name string) (string, error) {
	root, err := os.OpenRoot(name)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	identity, err := rootIdentity(int(f.Fd()))
	if err != nil {
		return "", err
	}
	// Probe the stronger containment primitive rather than assuming kernel support.
	probe, err := openRelative(int(f.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return "", err
	}
	unix.Close(probe)
	return identity, nil
}
func openRelative(fd int, name string, flags int) (int, error) {
	if flags&unix.O_PATH == 0 {
		flags |= unix.O_NONBLOCK
	}
	out, err := unix.Openat2(fd, name, &unix.OpenHow{Flags: uint64(flags | unix.O_CLOEXEC), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
		return -1, fmt.Errorf("%w: kernel openat2 containment unavailable: %v", ErrCapabilityUnavailable, err)
	}
	return out, err
}
func withWorkspaceRoot(w WorkspaceBinding) (*os.Root, *os.File, error) {
	root, err := os.OpenRoot(w.Policy.Root)
	if err != nil {
		return nil, nil, err
	}
	f, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	identity, err := rootIdentity(int(f.Fd()))
	if err == nil && identity != w.RootIdentity {
		err = fmt.Errorf("workspace root identity changed")
	}
	if err != nil {
		f.Close()
		root.Close()
		return nil, nil, err
	}
	return root, f, nil
}
func readRegularAt(ctx context.Context, dir int, name string) ([]byte, unix.Stat_t, error) {
	var st unix.Stat_t
	if err := ctx.Err(); err != nil {
		return nil, st, err
	}
	// O_PATH does not invoke a device driver's open method. Inspect before
	// opening for I/O, then use the pinned descriptor rather than racing the name.
	pinned, err := openRelative(dir, name, unix.O_PATH)
	if err != nil {
		return nil, st, err
	}
	defer unix.Close(pinned)
	if err = unix.Fstat(pinned, &st); err != nil {
		return nil, st, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return nil, st, fmt.Errorf("only singly-linked regular files allowed")
	}
	if st.Size > MaxWorkspaceFileBytes {
		return nil, st, ErrBudget
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", pinned), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, st, fmt.Errorf("%w: pinned regular-file reopen requires procfs: %v", ErrCapabilityUnavailable, err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var opened unix.Stat_t
	if err = unix.Fstat(fd, &opened); err != nil {
		return nil, st, err
	}
	if opened.Dev != st.Dev || opened.Ino != st.Ino || opened.Nlink != 1 || opened.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, st, fmt.Errorf("regular file identity changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxWorkspaceFileBytes+1))
	if err != nil {
		return nil, st, err
	}
	if len(b) > MaxWorkspaceFileBytes {
		return nil, st, ErrBudget
	}
	if err = ctx.Err(); err != nil {
		return nil, st, err
	}
	return b, st, nil
}
func readWorkspaceFile(ctx context.Context, w WorkspaceBinding, name string) ([]byte, error) {
	if err := safeWorkspacePath(name); err != nil {
		return nil, err
	}
	root, f, err := withWorkspaceRoot(w)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	defer f.Close()
	b, _, err := readRegularAt(ctx, int(f.Fd()), name)
	return b, err
}

// An uncooperative external writer can still race the last check and rename.
// There is no portable filesystem compare-and-swap; no global locking claim is
// made. Parent handles and NO_SYMLINKS prevent path redirection. Concurrent edits
// visible at either check are refused, and replacement is atomic for readers.
var workspaceEditMu sync.Mutex

func replaceWorkspaceFile(ctx context.Context, w WorkspaceBinding, a PatchArguments) error {
	return replaceWorkspaceFileChecked(ctx, w, a, nil)
}
func replaceWorkspaceFileChecked(ctx context.Context, w WorkspaceBinding, a PatchArguments, before func() error) error {
	// Single-process executor deployment: our edits cannot race one another.
	// External writers still have the explicitly documented last-check boundary.
	workspaceEditMu.Lock()
	defer workspaceEditMu.Unlock()
	if err := safeWorkspacePath(a.Path); err != nil {
		return err
	}
	root, f, err := withWorkspaceRoot(w)
	if err != nil {
		return err
	}
	defer root.Close()
	defer f.Close()
	parent, err := openRelative(int(f.Fd()), path.Dir(a.Path), unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	base := path.Base(a.Path)
	old, st, err := readRegularAt(ctx, parent, base)
	if err != nil {
		return err
	}
	if contentHash(old) != a.Preimage {
		return fmt.Errorf("preimage mismatch; file changed since proposal")
	}
	temp := ".agent-edit-" + uuid.NewString()
	fd, err := unix.Openat(parent, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(parent, temp, 0)
	out := os.NewFile(uintptr(fd), temp)
	if _, err = out.WriteString(a.Content); err == nil {
		err = out.Chmod(os.FileMode(st.Mode & 0777))
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Any durable lineage reservation must finish before the last byte/inode
	// check, so database latency does not widen the filesystem race boundary.
	if before != nil {
		if err = before(); err != nil {
			return err
		}
	}
	current, now, err := readRegularAt(ctx, parent, base)
	if err != nil {
		return err
	}
	if st.Dev != now.Dev || st.Ino != now.Ino || contentHash(current) != a.Preimage {
		return fmt.Errorf("preimage mismatch; concurrent file replacement")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unix.Renameat(parent, temp, parent, base); err != nil {
		return err
	}
	// Rename success is an observed edit. A subsequent durability failure is not
	// a safe refusal or retry; the engine must preserve reconciliation.
	if err = unix.Fsync(parent); err != nil {
		return &uncertainFileError{err}
	}
	return nil
}
