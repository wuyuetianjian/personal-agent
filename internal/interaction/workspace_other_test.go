//go:build !linux

package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnavailablePlatformFailsClosed(t *testing.T) {
	want := disabledNativeSupport(runtime.GOOS)
	if got := currentNativeSupport(); got != want {
		t.Fatalf("%+v", got)
	}
	for _, optIn := range []bool{false, true} {
		b := NewWorkspaceBrokerWithOptions(nil, WorkspaceBrokerOptions{NonSandboxExec: optIn})
		defer b.CloseMCP()
		exec := commandCapability()
		if optIn {
			exec = want.Exec
		}
		if caps := b.Capabilities(); caps != (WorkspaceCapabilities{Files: nativeFilesCapability(want), Exec: exec}) {
			t.Fatal(caps)
		}
	}
	if NativeMCPStdioCapability() != want.MCPStdio || NativeMCPStdioCapability().Enabled || len(NativeToolDefinitions()) != 0 {
		t.Fatal("unqualified capability advertised")
	}
	if platformFileCapability() != want.Read || platformExecCapability() != want.Exec {
		t.Fatal("leaf capability changed")
	}
}

func TestUnavailablePlatformDirectRefusals(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sentinel.txt")
	marker := filepath.Join(root, "spawned")
	if err := os.WriteFile(file, []byte("private sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	review := &ExecReview{Program: binary, Cwd: root, Argv: []string{"-test.run=^TestNativeRefusalSpawnSentinel$"}, Environment: []EnvironmentChange{{"PA_NATIVE_REFUSAL_MARKER", marker}}, ExecutableIdentity: "unchanged", ExecutableSHA256: "not inspected", TimeoutMillis: 1000}
	before, _ := json.Marshal(review)
	w := WorkspaceBinding{Policy: WorkspacePolicy{Root: root}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	callbacks := 0
	for _, ctx := range []context.Context{context.Background(), cancelled} {
		for _, path := range []string{root, file, filepath.Join(root, "missing"), "../sentinel.txt", "bad\x00path", ""} {
			id, e := workspaceIdentity(path)
			if id != "" || !errors.Is(e, ErrCapabilityUnavailable) || e.Error() != "capability unavailable: "+NewWorkspaceBroker(nil).Capabilities().Files.Reason {
				t.Fatal(id, e)
			}
			data, e := readWorkspaceFile(ctx, w, path)
			if data != nil || e != ErrCapabilityUnavailable {
				t.Fatalf("%q %v", data, e)
			}
			patch := PatchArguments{Path: path, Content: "changed"}
			if e = replaceWorkspaceFile(ctx, w, patch); e != ErrCapabilityUnavailable {
				t.Fatal(e)
			}
			if e = replaceWorkspaceFileChecked(ctx, w, patch, func() error { callbacks++; return nil }); e != ErrCapabilityUnavailable {
				t.Fatal(e)
			}
		}
		for _, r := range []*ExecReview{nil, review} {
			if e := inspectExec(r); e != ErrCapabilityUnavailable {
				t.Fatal(e)
			}
		}
		result, e := executePreparedExec(ctx, w, preparedTool{ExecReview: review})
		if e != nil || !result.IsError || result.Content != "Tool refused: capability unavailable" {
			t.Fatal(result, e)
		}
	}
	after, _ := json.Marshal(review)
	data, e := os.ReadFile(file)
	if e != nil || string(data) != "private sentinel" || string(before) != string(after) || callbacks != 0 {
		t.Fatal("refusal had effects", string(data), e, callbacks)
	}
	if _, e = os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("process marker", e)
	}
}
