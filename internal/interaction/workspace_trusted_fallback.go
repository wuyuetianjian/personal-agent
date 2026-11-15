//go:build !darwin && !windows

package interaction

import "context"

func trustedWorkspacePlatformCapability() Capability {
	return Capability{Reason: "Explicit trusted workspace adapter is available only on qualified macOS/Windows targets"}
}
func trustedWorkspaceIdentity(string) (string, error) { return "", ErrCapabilityUnavailable }
func readTrustedWorkspaceFile(context.Context, WorkspaceBinding, string) ([]byte, error) {
	return nil, ErrCapabilityUnavailable
}
