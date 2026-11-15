//go:build windows

package interaction

import (
	"agent/internal/nativefs/windowsprobe"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func trustedWorkspacePlatformCapability() Capability {
	return Capability{Enabled: true, Reason: "Explicit trusted NTFS Read/Search preview available; host and session opt-in required; Windows non-administrator acceptance unverified; no hostile external replacement guarantee"}
}

// This metadata identity is checked around each operation, not a promise of
// atomic namespace acquisition against hostile external replacement. Actual
// content reads use the foundation's qualified handle-relative traversal.
func trustedWindowsRootIdentity(root string) (id string, err error) {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, windows.CloseHandle(h))
		if err != nil {
			id = ""
		}
	}()
	kind, err := windows.GetFileType(h)
	if err != nil {
		return "", err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	if kind != windows.FILE_TYPE_DISK || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", fmt.Errorf("unsupported trusted workspace root")
	}
	return fmt.Sprintf("trusted-win-v1:%08x:%08x%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, info.CreationTime.HighDateTime, info.CreationTime.LowDateTime), nil
}
func trustedWorkspaceIdentity(root string) (id string, err error) {
	r, err := windowsprobe.OpenLocalRoot(root)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, r.Close())
		if err != nil {
			id = ""
		}
	}()
	return trustedWindowsRootIdentity(root)
}

func readTrustedWorkspaceFile(ctx context.Context, w WorkspaceBinding, name string) ([]byte, error) {
	return readTrustedWindowsWithClose(ctx, w, name, func(r *windowsprobe.Root) error { return r.Close() })
}

// The close function is per-call and private; production always consumes the real root.
func readTrustedWindowsWithClose(ctx context.Context, w WorkspaceBinding, name string, closeRoot func(*windowsprobe.Root) error) (data []byte, err error) {
	if !w.Policy.TrustedWorkspace || !w.Policy.trustedHostGranted {
		return nil, ErrCapabilityUnavailable
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = safeTrustedWorkspacePath(name); err != nil {
		return nil, err
	}
	// Reject DOS device spellings before any path-based metadata call. The
	// foundation independently validates its relative components as well.
	for _, part := range strings.Split(name, "/") {
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || (len([]rune(stem)) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", []rune(stem)[3])) {
			return nil, fmt.Errorf("reserved Windows path")
		}
	}
	r, err := windowsprobe.OpenLocalRoot(w.Policy.Root)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, closeRoot(r), ctx.Err())
		if err != nil {
			data = nil
		}
	}()
	id, err := trustedWindowsRootIdentity(w.Policy.Root)
	if err != nil {
		return nil, err
	}
	if id != w.RootIdentity {
		return nil, fmt.Errorf("workspace root identity changed")
	}
	candidate := filepath.Join(w.Policy.Root, filepath.FromSlash(name))
	info, err := os.Lstat(candidate)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("trusted workspace requires regular file")
	}
	if err = trustedWorkspacePathProtected(w, candidate, info); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	data, err = r.Read(ctx, strings.Split(name, "/"))
	if errors.Is(err, windowsprobe.ErrTooLarge) {
		err = errors.Join(ErrBudget, err)
	}
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(candidate)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, after) {
		return nil, fmt.Errorf("workspace file identity changed")
	}
	if err = trustedWorkspacePathProtected(w, candidate, after); err != nil {
		return nil, err
	}
	id, err = trustedWindowsRootIdentity(w.Policy.Root)
	if err != nil {
		return nil, err
	}
	if id != w.RootIdentity {
		return nil, fmt.Errorf("workspace root identity changed")
	}
	return data, nil
}
