//go:build darwin

package interaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Kept for attribution of the preceding native RED contract.
var errTrustedDarwinUnimplemented = errors.New("trusted Darwin workspace explicit RED")

func trustedWorkspacePlatformCapability() Capability {
	return Capability{Reason: "Trusted APFS read/search adapter awaits native qualification; no hostile namespace isolation"}
}

func trustedDarwinOpenRoot(root string) (*os.File, error) {
	if !filepath.IsAbs(root) {
		return nil, ErrInvalid
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), root), nil
}
func trustedDarwinRootIdentity(f *os.File) (string, error) {
	var st unix.Stat_t
	var fs unix.Statfs_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return "", err
	}
	if err := unix.Fstatfs(int(f.Fd()), &fs); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || unix.ByteSliceToString(fs.Fstypename[:]) != "apfs" {
		return "", ErrCapabilityUnavailable
	}
	return fmt.Sprintf("trusted-apfs:%v:%d:%d:%d:%d", fs.Fsid, st.Dev, st.Ino, st.Btim.Sec, st.Btim.Nsec), nil
}
func trustedWorkspaceIdentity(root string) (id string, err error) {
	f, err := trustedDarwinOpenRoot(root)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			id = ""
		}
	}()
	return trustedDarwinRootIdentity(f)
}
func readTrustedWorkspaceFile(ctx context.Context, w WorkspaceBinding, name string) ([]byte, error) {
	return readTrustedDarwinWithClose(ctx, w, name, (*os.File).Close)
}
func trustedDarwinRegular(st *unix.Stat_t) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Nlink == 1 && st.Size >= 0 && st.Size <= MaxWorkspaceFileBytes
}
func trustedDarwinStable(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim && a.Flags == b.Flags
}

// The explicitly trusted workspace excludes malicious namespace replacement by
// other programs. Precheck/open/recheck is not an atomic regular-only open, and
// the metadata observations are not a content snapshot.
func readTrustedDarwinWithClose(ctx context.Context, w WorkspaceBinding, name string, closeFile func(*os.File) error) (out []byte, err error) {
	if !w.Policy.TrustedWorkspace || !w.Policy.trustedHostGranted {
		return nil, ErrCapabilityUnavailable
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = safeTrustedWorkspacePath(name); err != nil {
		return nil, err
	}
	if closeFile == nil {
		return nil, ErrInvalid
	}
	var owned []*os.File
	defer func() {
		for i := len(owned) - 1; i >= 0; i-- {
			err = errors.Join(err, closeFile(owned[i]))
		}
		err = errors.Join(err, ctx.Err())
		if err != nil {
			out = nil
		}
	}()
	root, err := trustedDarwinOpenRoot(w.Policy.Root)
	if err != nil {
		return nil, err
	}
	owned = append(owned, root)
	id, err := trustedDarwinRootIdentity(root)
	if err != nil {
		return nil, err
	}
	if id != w.RootIdentity {
		return nil, ErrConflict
	}
	var rootStat unix.Stat_t
	if err = unix.Fstat(int(root.Fd()), &rootStat); err != nil {
		return nil, err
	}
	parent := root
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		fd, e := unix.Openat(int(parent.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		parent = os.NewFile(uintptr(fd), part)
		owned = append(owned, parent)
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil {
			return nil, e
		}
		if st.Dev != rootStat.Dev || st.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil, ErrCapabilityUnavailable
		}
	}
	leaf := parts[len(parts)-1]
	var before unix.Stat_t
	if err = unix.Fstatat(int(parent.Fd()), leaf, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if !trustedDarwinRegular(&before) || before.Dev != rootStat.Dev {
		return nil, ErrCapabilityUnavailable
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), leaf)
	owned = append(owned, f)
	var opened unix.Stat_t
	if err = unix.Fstat(fd, &opened); err != nil {
		return nil, err
	}
	if !trustedDarwinRegular(&opened) || !trustedDarwinStable(before, opened) {
		return nil, ErrConflict
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err = trustedWorkspacePathProtected(w, filepath.Join(w.Policy.Root, name), info); err != nil {
		return nil, err
	}
	// Read one additional byte to detect growth, with a fixed syscall budget.
	data := make([]byte, int(opened.Size)+1)
	used := 0
	for attempts := 0; ; attempts++ {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if attempts == 64 {
			return nil, fmt.Errorf("trusted read short-read budget exhausted")
		}
		end := min(len(data), used+64*1024)
		n, e := unix.Pread(fd, data[used:end], int64(used))
		if e != nil {
			return nil, e
		}
		used += n
		if used > int(opened.Size) {
			return nil, ErrConflict
		}
		if n == 0 {
			break
		}
	}
	var after unix.Stat_t
	if err = unix.Fstat(fd, &after); err != nil {
		return nil, err
	}
	if used != int(opened.Size) || !trustedDarwinStable(opened, after) {
		return nil, ErrConflict
	}
	// Recheck the selected root pathname as well as the retained root descriptor.
	current, e := trustedWorkspaceIdentity(w.Policy.Root)
	if e != nil {
		return nil, e
	}
	if current != w.RootIdentity {
		return nil, ErrConflict
	}
	return data[:used], nil
}
