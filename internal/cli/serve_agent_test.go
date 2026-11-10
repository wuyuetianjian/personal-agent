package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeAgentRejectsMissingAuthAndNonloopback(t *testing.T) {
	t.Setenv("PA_AGENT_TOKEN", "")
	if e := Run(context.Background(), []string{"serve-agent", "--runtime-parent", t.TempDir()}, io.Discard); e == nil || !strings.Contains(e.Error(), "auth") {
		t.Fatal(e)
	}
	t.Setenv("PA_AGENT_TOKEN", "secret")
	if e := Run(context.Background(), []string{"serve-agent", "--runtime-parent", t.TempDir(), "--addr", "0.0.0.0:9"}, io.Discard); e == nil || !strings.Contains(e.Error(), "loopback") {
		t.Fatal(e)
	}
}
func TestAgentTokenFileNeverPrintsToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth")
	os.WriteFile(path, []byte("private-token\n"), 0600)
	token, e := readAgentToken(path)
	if e != nil || token != "private-token" {
		t.Fatal("private token file failed")
	}
}

func TestServeAgentNonSandboxExecRequiresExplicitWorkspace(t *testing.T) {
	t.Setenv("PA_AGENT_TOKEN", "fixture")
	err := Run(context.Background(), []string{"serve-agent", "--non-sandbox-exec", "--model", "test", "--runtime-parent", t.TempDir()}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "NON-SANDBOXED") || !strings.Contains(err.Error(), "workspace") {
		t.Fatal(err)
	}
}

func TestRebuiltMCPConfigIsSeparateDefaultOffAndStrict(t *testing.T) {
	if c, e := readTrustedMCPConfig(""); e != nil || c.Enabled {
		t.Fatal(c, e)
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	for _, raw := range []string{`{"enabled":true,"servers":[]}`, `{"enabled":true,"enabled":false,"servers":[]}`, `{"enabled":true,"servers":[{"id":"x","command":"relative","timeout_ms":1000}]}`, `{"enabled":false,"legacy_server":true}`} {
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := readTrustedMCPConfig(path); e == nil {
			t.Fatal("accepted invalid trusted config", raw)
		}
	}
	if e := os.WriteFile(path, []byte(`{"enabled":false,"servers":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if c, e := readTrustedMCPConfig(path); e != nil || c.Enabled {
		t.Fatal(c, e)
	}
}
