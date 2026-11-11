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
