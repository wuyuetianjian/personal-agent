//go:build windows

package interaction

import (
	"context"
	"fmt"
)

func trustedWorkspacePlatformCapability() Capability {
	return Capability{Enabled: false, Reason: "Windows trusted workspace native qualification and ordinary-user validation pending; no hostile external replacement guarantee"}
}
func trustedWorkspaceIdentity(root string) (string, error) {
	return "", fmt.Errorf("%w: trusted Windows workspace explicit RED", ErrCapabilityUnavailable)
}
func readTrustedWorkspaceFile(ctx context.Context, w WorkspaceBinding, name string) ([]byte, error) {
	return nil, fmt.Errorf("%w: trusted Windows workspace explicit RED", ErrCapabilityUnavailable)
}
