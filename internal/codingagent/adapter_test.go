package codingagent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCLIAdaptersMapRequests(t *testing.T) {
	binary := fakeBinary(t)
	for _, tc := range []struct {
		name    string
		adapter *CLIAdapter
		want    string
	}{
		{
			name: "codex",
			adapter: NewCodexAdapter(BackendConfig{
				ID: "codex", Enabled: true, Binary: binary, Capabilities: []Capability{CapabilityCoding},
			}, nil),
			want: "exec implement feature",
		},
		{
			name: "claude",
			adapter: NewClaudeAdapter(BackendConfig{
				ID: "claude", Enabled: true, Binary: binary, Capabilities: []Capability{CapabilityCoding},
			}, nil),
			want: "-p implement feature",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.adapter.Run(context.Background(), Request{
				Prompt:         "implement feature",
				Required:       []Capability{CapabilityCoding},
				RepositoryPath: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !strings.Contains(result.Summary, tc.want) {
				t.Fatalf("summary %q missing %q", result.Summary, tc.want)
			}
		})
	}
}

func fakeBinary(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// Reuse the native test binary as a child process, without a shell or Go toolchain.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "exec", "-p":
			fmt.Print(strings.Join(os.Args[1:], " "))
			os.Exit(0)
		case "--executor-ok":
			fmt.Print("ok")
			os.Exit(0)
		case "--executor-wait":
			time.Sleep(30 * time.Second)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}
