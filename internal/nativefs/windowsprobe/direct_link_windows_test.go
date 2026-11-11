//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"bytes"
	"context"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const directRootTokenMarker = "WINDOWSPROBE_DIRECT_ROOT_TOKEN_HELPER"

// Tagged qualification of the same public foundation route as the ordinary suite.
func TestNativeDirectLinkRootReducedToken(t *testing.T) {
	var current windows.Token
	defer directFixtureCloseToken(t, "current", &current)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, &current); err != nil {
		t.Fatal(err)
	}
	var child windows.Token
	defer directFixtureCloseToken(t, "child", &child)
	label := ""
	linked, err := current.GetLinkedToken()
	defer directFixtureCloseToken(t, "linked", &linked)
	t.Logf("token diagnostic: GetLinkedToken error=%v", err)
	if err == nil {
		elevated, queryErr := rootTokenElevation(linked)
		if queryErr != nil {
			directFixtureCloseToken(t, "linked", &linked)
			t.Fatal(queryErr)
		}
		if !elevated {
			child, label = linked, "linked-non-elevated"
			linked = 0 // ownership transferred, not duplicated
		} else {
			directFixtureCloseToken(t, "linked", &linked)
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
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeDirectLinkRootRestrictedTokenHelper$", "-test.v", "-test.timeout=30s")
	cmd.Dir = dir
	cmd.Env = []string{directRootTokenMarker + "=" + label, "WINDOWSPROBE_ROOT_FIXTURE=" + dir}
	// Go may append SystemRoot for Windows process startup. No inherited host
	// credential/environment variables are passed by this test.
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(child)}
	output, err := cmd.CombinedOutput()
	t.Logf("qualification=%s\n%s", label, output)
	if err != nil {
		t.Fatalf("%s child launch/qualification/read failed (no privileged retry): %v", label, err)
	}
	if !bytes.Contains(output, []byte("DIRECT_ROOT_TOKEN_READ_OK:"+label)) {
		t.Fatal("child exited without verified read marker")
	}
}

func TestNativeDirectLinkRootRestrictedTokenHelper(t *testing.T) {
	label := os.Getenv(directRootTokenMarker)
	if label == "" {
		return
	}
	if label != "linked-non-elevated" && label != "restricted-not-standard-user" {
		t.Fatal("invalid helper marker")
	}
	var token windows.Token
	defer directFixtureCloseToken(t, "helper", &token)
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
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
	baseline := captureHandleInventory(t)
	root, err := OpenLocalRoot(dir)
	if err != nil {
		if label == "restricted-not-standard-user" {
			rootTokenNamespaceDiagnostics(t, dir)
		}
		t.Fatalf("qualified %s OpenLocalRoot: %v", label, err)
	}
	defer func() {
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	}()
	data, err := root.Read(context.Background(), []string{"token-proof.txt"})
	if err != nil || string(data) != "root-token-fixture\n" {
		t.Fatalf("qualified token read: data=%q error=%v", data, err)
	}
	live := captureHandleInventory(t)
	if baseline.files[root.h] {
		t.Fatal("root positive control already present in baseline")
	}
	if !live.files[root.h] {
		t.Fatal("direct root disk-handle positive control missing")
	}
	if e := root.Close(); e != nil {
		t.Fatalf("direct root close: %v", e)
	}
	after := captureHandleInventory(t)
	if !sameDiskHandles(baseline, after) {
		logHandleDelta(t, baseline, after)
		t.Fatal("direct root disk handles did not recover baseline")
	}
	t.Log("DIRECT_ROOT_TOKEN_READ_OK:" + label)
}
