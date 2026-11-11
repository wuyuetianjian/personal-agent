package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRefusalSpawnSentinel(t *testing.T) {
	if marker := os.Getenv("PA_NATIVE_REFUSAL_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("spawned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// These observations guard effects; inspection of the deliberately effect-free
// adapter also matters: unchanged bytes alone cannot prove that no read occurred.
func TestUnsupportedNativeRefusalContract(t *testing.T) {
	for _, target := range []string{"darwin", "windows", "freebsd", "", "linux"} {
		t.Run(target, func(t *testing.T) {
			a := unavailableNativeAdapter{target: target}
			root := t.TempDir()
			sentinel := filepath.Join(root, "sentinel.txt")
			marker := filepath.Join(root, "spawned")
			secret := []byte("must never be read by an unavailable adapter")
			if err := os.WriteFile(sentinel, secret, 0600); err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			review := &ExecReview{Program: binary, Argv: []string{"-test.run=^TestNativeRefusalSpawnSentinel$"}, Cwd: root, Environment: []EnvironmentChange{{"PA_NATIVE_REFUSAL_MARKER", marker}}, TimeoutMillis: 1000, ExecutableIdentity: "do not mutate", ExecutableSHA256: "do not hash"}
			before, _ := json.Marshal(review)
			w := WorkspaceBinding{RootIdentity: "sentinel-identity", Policy: WorkspacePolicy{Root: root}}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			calls := 0
			for _, ctx := range []context.Context{context.Background(), cancelled} {
				for _, path := range []string{sentinel, root, filepath.Join(root, "missing"), "../sentinel.txt", "bad\x00path", ""} {
					identity, e := a.workspaceIdentity(path)
					if identity != "" || !errors.Is(e, ErrCapabilityUnavailable) || e.Error() != "capability unavailable: "+nativeFilesCapability(disabledNativeSupport(target)).Reason {
						t.Fatalf("identity: %q %v", identity, e)
					}
					data, e := a.readWorkspaceFile(ctx, w, path)
					if data != nil || e != ErrCapabilityUnavailable {
						t.Fatalf("read: %q %v", data, e)
					}
					patch := PatchArguments{Path: path, Content: "changed"}
					if e = a.replaceWorkspaceFile(ctx, w, patch); e != ErrCapabilityUnavailable {
						t.Fatal(e)
					}
					if e = a.replaceWorkspaceFileChecked(ctx, w, patch, func() error { calls++; return nil }); e != ErrCapabilityUnavailable {
						t.Fatal(e)
					}
				}
				for _, r := range []*ExecReview{nil, review} {
					if e := a.inspectExec(r); e != ErrCapabilityUnavailable {
						t.Fatal(e)
					}
				}
				result, e := a.executePreparedExec(ctx, w, preparedTool{ExecReview: review})
				if e != nil || !result.IsError || result.Content != "Tool refused: capability unavailable" {
					t.Fatalf("execute: %+v %v", result, e)
				}
			}
			after, _ := json.Marshal(review)
			data, e := os.ReadFile(sentinel)
			if e != nil || string(data) != string(secret) || string(before) != string(after) || calls != 0 {
				t.Fatalf("mutation: %q %v callbacks=%d review=%s", data, e, calls, after)
			}
			if _, e = os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
				t.Fatalf("process marker: %v", e)
			}
		})
	}
}
