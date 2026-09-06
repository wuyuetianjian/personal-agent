package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
