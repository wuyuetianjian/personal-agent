//go:build linux

package interaction

import "testing"

func TestNativeLinuxReadinessAndBrokerPermissions(t *testing.T) {
	files := Capability{Enabled: true, Reason: "Linux openat2 required at operation time; symlinks, mount crossings, hardlinks and nonregular files rejected"}
	exec := Capability{Enabled: true, Reason: "NON-SANDBOXED: runs with current OS-user access, including files, network and credentials available to that user. Cwd is not confinement; a minimal environment is not secret isolation. Cancellation may leave descendants running; direct-child exit does not prove all descendants stopped. Linux pinned executable/cwd, PID-safe direct-child kill and wait; no descendant cleanup or containment."}
	mcp := Capability{Enabled: true, Reason: "Linux stdio transport available; explicit host configuration and session opt-in required; direct-child cleanup only"}
	want := nativeSupport{Read: files, Search: files, Replace: files, Undo: files, Exec: exec, MCPStdio: mcp}
	got := currentNativeSupport()
	if got != want || platformFileCapability() != files || platformExecCapability() != exec || NativeMCPStdioCapability() != mcp {
		t.Fatalf("%+v", got)
	}
	off := Capability{Reason: "NON-SANDBOXED exec disabled by default; explicit immutable host and session opt-in required"}
	if caps := NewWorkspaceBroker(nil).Capabilities(); caps != (WorkspaceCapabilities{Files: files, Exec: off}) || commandCapability() != off {
		t.Fatal(caps)
	}
	b := NewWorkspaceBrokerWithOptions(nil, WorkspaceBrokerOptions{NonSandboxExec: true})
	defer b.CloseMCP()
	if caps := b.Capabilities(); caps != (WorkspaceCapabilities{Files: files, Exec: exec}) {
		t.Fatal(caps)
	}
	got.Read.Enabled = false
	got.MCPStdio.Reason = "changed"
	got.Exec.Enabled = false
	if currentNativeSupport() != want {
		t.Fatal("readiness escaped by reference")
	}
}
