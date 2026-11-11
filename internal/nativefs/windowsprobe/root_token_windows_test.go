//go:build windows

package windowsprobe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const rootTokenMarker = "WINDOWSPROBE_ROOT_TOKEN_HELPER"

// This test does not elevate, alter the host token, or change machine policy.
// A restricted administrator token is explicitly NOT a standard-user proof.
func TestNativeOpenLocalRootReducedToken(t *testing.T) {
	var current windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, &current); err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	var child windows.Token
	label := ""
	linked, err := current.GetLinkedToken()
	t.Logf("token diagnostic: GetLinkedToken error=%v", err)
	if err == nil {
		elevated, queryErr := rootTokenElevation(linked)
		if queryErr != nil {
			linked.Close()
			t.Fatal(queryErr)
		}
		if !elevated {
			child, label = linked, "linked-non-elevated"
		} else {
			linked.Close()
		}
	}
	if child == 0 {
		// Disable the administrator SID and remove every privilege except Windows'
		// documented DISABLE_MAX_PRIVILEGE exception, SeChangeNotifyPrivilege.
		admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
		if err != nil {
			t.Fatal(err)
		}
		disabled := windows.SIDAndAttributes{Sid: admin}
		proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")
		if err := proc.Find(); err != nil {
			t.Fatal(err)
		}
		const disableMaxPrivilege = 1
		ok, _, callErr := proc.Call(uintptr(current), disableMaxPrivilege, 1, uintptr(unsafe.Pointer(&disabled)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&child)))
		runtime.KeepAlive(admin)
		if ok == 0 {
			t.Fatalf("CreateRestrictedToken (no elevation attempted): %v", callErr)
		}
		label = "restricted-not-standard-user"
	}
	defer child.Close()
	rootTokenDiagnostics(t, "parent", current)
	rootTokenDiagnostics(t, "selected-child", child)
	parentAuth, parentErr := rootTokenAuthID(current)
	childAuth, childErr := rootTokenAuthID(child)
	t.Logf("token diagnostic: parent/selected-child authentication ID equal=%v parent_error=%v child_error=%v", parentErr == nil && childErr == nil && parentAuth == childAuth, parentErr, childErr)
	if err := rootTokenLeastPrivilege(child); err != nil {
		t.Fatalf("%s token qualification: %v", label, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token-proof.txt"), []byte("root-token-fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Fixture setup is trusted. Expand any runner TEMP 8.3 spelling before handing
	// the path to the acquisition API; do not normalize production input here.
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("long fixture path: length=%d error=%v", n, err)
	}
	dir = windows.UTF16ToString(buf[:n])
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeOpenLocalRootRestrictedTokenHelper$", "-test.v", "-test.timeout=30s")
	cmd.Dir = dir
	cmd.Env = []string{rootTokenMarker + "=" + label, "WINDOWSPROBE_ROOT_FIXTURE=" + dir}
	// Go may append SystemRoot for Windows process startup. No inherited host
	// credential/environment variables are passed by this test.
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(child)}
	output, err := cmd.CombinedOutput()
	t.Logf("qualification=%s\n%s", label, output)
	if err != nil {
		t.Fatalf("%s child launch/qualification/read failed (no privileged retry): %v", label, err)
	}
	if !bytes.Contains(output, []byte("ROOT_TOKEN_READ_OK:"+label)) {
		t.Fatal("child exited without verified read marker")
	}
}

func TestNativeOpenLocalRootRestrictedTokenHelper(t *testing.T) {
	label := os.Getenv(rootTokenMarker)
	if label == "" {
		return
	}
	if label != "linked-non-elevated" && label != "restricted-not-standard-user" {
		t.Fatal("invalid helper marker")
	}
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	rootTokenDiagnostics(t, "running-child", token)
	if err := rootTokenLeastPrivilege(token); err != nil {
		t.Fatal(err)
	}
	if label == "linked-non-elevated" {
		elevated, err := rootTokenElevation(token)
		if err != nil || elevated {
			t.Fatalf("linked token elevated=%v error=%v", elevated, err)
		}
	}
	dir := os.Getenv("WINDOWSPROBE_ROOT_FIXTURE")
	if dir == "" {
		t.Fatal("missing fixture path")
	}
	root, err := OpenLocalRoot(dir)
	if err != nil {
		if label == "restricted-not-standard-user" {
			rootTokenNamespaceDiagnostics(t, dir)
		}
		t.Fatalf("qualified %s OpenLocalRoot: %v", label, err)
	}
	defer root.Close()
	data, err := root.Read(context.Background(), []string{"token-proof.txt"})
	if err != nil || string(data) != "root-token-fixture\n" {
		t.Fatalf("qualified token read: data=%q error=%v", data, err)
	}
	t.Log("ROOT_TOKEN_READ_OK:" + label)
}

func rootTokenElevation(token windows.Token) (bool, error) {
	var value, size uint32
	err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&value)), 4, &size)
	if err != nil {
		return false, err
	}
	if size != 4 {
		return false, fmt.Errorf("invalid elevation size %d", size)
	}
	return value != 0, nil
}

func rootTokenLeastPrivilege(token windows.Token) error {
	groups, err := token.GetTokenGroups()
	if err != nil {
		return err
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			return fmt.Errorf("administrator membership remains enabled")
		}
	}
	var size uint32
	err = windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size)
	if err != windows.ERROR_INSUFFICIENT_BUFFER || size < 4 || size > 65536 {
		return fmt.Errorf("privilege size=%d error=%v", size, err)
	}
	buf := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buf[0], uint32(len(buf)), &size); err != nil {
		return err
	}
	privileges := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	offset := unsafe.Offsetof(privileges.Privileges)
	if uint64(offset)+uint64(privileges.PrivilegeCount)*uint64(unsafe.Sizeof(windows.LUIDAndAttributes{})) > uint64(len(buf)) {
		return fmt.Errorf("invalid privilege buffer")
	}
	name, _ := windows.UTF16PtrFromString("SeChangeNotifyPrivilege")
	var allowed windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &allowed); err != nil {
		return err
	}
	for _, p := range privileges.AllPrivileges() {
		if p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 && p.Luid != allowed {
			return fmt.Errorf("unexpected enabled privilege LUID=%v (only traverse permitted)", p.Luid)
		}
	}
	runtime.KeepAlive(buf)
	return nil
}

// Diagnostics are observations only: their failures neither skip nor relax the
// original qualification assertion. Never print raw SID, user or device names.
func rootTokenInfo(token windows.Token, class uint32) ([]byte, error) {
	var size uint32
	err := windows.GetTokenInformation(token, class, nil, 0, &size)
	if err != windows.ERROR_INSUFFICIENT_BUFFER || size == 0 || size > 65536 {
		return nil, fmt.Errorf("token class %d sizing: size=%d error=%v", class, size, err)
	}
	b := make([]byte, size)
	if err := windows.GetTokenInformation(token, class, &b[0], uint32(len(b)), &size); err != nil {
		return nil, err
	}
	if size == 0 || size > uint32(len(b)) {
		return nil, fmt.Errorf("invalid token result size")
	}
	return b[:size], nil
}

func rootTokenAuthID(token windows.Token) (windows.LUID, error) {
	b, err := rootTokenInfo(token, windows.TokenStatistics)
	// TOKEN_STATISTICS starts with TokenId followed by AuthenticationId.
	if err != nil {
		return windows.LUID{}, err
	}
	if len(b) < 2*int(unsafe.Sizeof(windows.LUID{})) {
		return windows.LUID{}, fmt.Errorf("short token statistics")
	}
	id := *(*windows.LUID)(unsafe.Pointer(&b[unsafe.Sizeof(windows.LUID{})]))
	runtime.KeepAlive(b)
	return id, nil
}

func rootTokenDiagnostics(t *testing.T, label string, token windows.Token) {
	t.Helper()
	for _, class := range []uint32{windows.TokenOwner, windows.TokenElevationType, windows.TokenRestrictedSids} {
		b, err := rootTokenInfo(token, class)
		if err != nil {
			t.Logf("token diagnostic %s class=%d: %v", label, class, err)
			continue
		}
		switch class {
		case windows.TokenOwner:
			if len(b) >= int(unsafe.Sizeof(uintptr(0))) {
				sid := *(**windows.SID)(unsafe.Pointer(&b[0]))
				t.Logf("token diagnostic %s owner_is_admin=%v", label, sid != nil && sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
			} else {
				t.Logf("token diagnostic %s short owner result", label)
			}
		case windows.TokenElevationType, windows.TokenRestrictedSids:
			if len(b) >= 4 {
				t.Logf("token diagnostic %s class=%d value=%d (18=elevationType,11=restrictedSIDCount)", label, class, *(*uint32)(unsafe.Pointer(&b[0])))
			} else {
				t.Logf("token diagnostic %s short class=%d result", label, class)
			}
		}
		runtime.KeepAlive(b)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Logf("token diagnostic %s groups: %v", label, err)
		return
	}
	logonPresent, logonEnabled, adminDenyOnly := false, false, false
	for _, g := range groups.AllGroups() {
		if g.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			logonPresent = true
			logonEnabled = logonEnabled || g.Attributes&windows.SE_GROUP_ENABLED != 0
		}
		if g.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			adminDenyOnly = g.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY != 0
		}
	}
	t.Logf("token diagnostic %s logon_sid_present=%v logon_sid_enabled=%v admin_deny_only=%v", label, logonPresent, logonEnabled, adminDenyOnly)
}

func rootTokenNamespaceDiagnostics(t *testing.T, fixture string) {
	t.Helper()
	// Inspect ONLY the same local DOS-device context. No GLOBAL?? fallback,
	// device anchor transfer, ACL modification, impersonation or retry of Read.
	if len(fixture) < 3 || fixture[1] != ':' || fixture[2] != '\\' {
		t.Log("namespace diagnostic: fixture is not drive absolute")
		return
	}
	dll := windows.NewLazySystemDLL("ntdll.dll")
	openDir := dll.NewProc("NtOpenDirectoryObject")
	openLink := dll.NewProc("NtOpenSymbolicLinkObject")
	queryLink := dll.NewProc("NtQuerySymbolicLinkObject")
	for _, p := range []*windows.LazyProc{openDir, openLink, queryLink} {
		if err := p.Find(); err != nil {
			t.Logf("namespace diagnostic API unavailable: %v", err)
			return
		}
	}
	name, err := windows.NewNTUnicodeString(`\??`)
	if err != nil {
		t.Logf("namespace diagnostic name: %v", err)
		return
	}
	oa := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), ObjectName: name, Attributes: windows.OBJ_CASE_INSENSITIVE}
	var directory windows.Handle
	status, _, _ := openDir.Call(uintptr(unsafe.Pointer(&directory)), 1 /* DIRECTORY_QUERY */, uintptr(unsafe.Pointer(&oa)))
	runtime.KeepAlive(name)
	t.Logf("namespace diagnostic local directory query-open NTSTATUS=0x%08x", uint32(status))
	if uint32(status) != 0 {
		return
	}
	defer windows.CloseHandle(directory)
	drive, err := windows.NewNTUnicodeString(fixture[:2])
	if err != nil {
		t.Logf("namespace diagnostic drive: %v", err)
		return
	}
	oa.RootDirectory, oa.ObjectName = directory, drive
	var link windows.Handle
	status, _, _ = openLink.Call(uintptr(unsafe.Pointer(&link)), 1 /* SYMBOLIC_LINK_QUERY */, uintptr(unsafe.Pointer(&oa)))
	runtime.KeepAlive(drive)
	t.Logf("namespace diagnostic local drive link query-open NTSTATUS=0x%08x", uint32(status))
	if uint32(status) != 0 {
		return
	}
	defer windows.CloseHandle(link)
	buf := make([]uint16, 32767)
	target := windows.NTUnicodeString{MaximumLength: uint16(len(buf) * 2), Buffer: &buf[0]}
	var returned uint32
	status, _, _ = queryLink.Call(uintptr(link), uintptr(unsafe.Pointer(&target)), uintptr(unsafe.Pointer(&returned)))
	runtime.KeepAlive(buf)
	t.Logf("namespace diagnostic local drive link query NTSTATUS=0x%08x returned_bytes=%d", uint32(status), returned)
}
