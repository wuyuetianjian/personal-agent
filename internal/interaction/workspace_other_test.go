//go:build !linux

package interaction

import (
	"errors"
	"strings"
	"testing"
)

func TestUnavailablePlatformFailsClosed(t *testing.T) {
	caps := NewWorkspaceBroker(nil).Capabilities()
	if caps.Files.Enabled || caps.Exec.Enabled || caps.Files.Reason == "" || caps.Exec.Reason == "" {
		t.Fatal(caps)
	}
	if len(NativeToolDefinitions()) != 0 {
		t.Fatal("unimplemented native tools advertised")
	}
	if _, err := workspaceIdentity(t.TempDir()); !errors.Is(err, ErrCapabilityUnavailable) || !strings.Contains(err.Error(), caps.Files.Reason) {
		t.Fatal(err)
	}
}
