//go:build !linux

package interaction

import (
	"context"
	"fmt"
)

// Go os.Root is available on these platforms. This implementation has not yet
// supplied/verified the extra no-mount/no-hardlink and identity guarantees.
func platformFileCapability() Capability {
	return Capability{Reason: "native file adapter not implemented/verified on this platform; Linux openat2 guarantees required"}
}
func workspaceIdentity(string) (string, error) {
	return "", fmt.Errorf("%w: %s", ErrCapabilityUnavailable, platformFileCapability().Reason)
}
func readWorkspaceFile(context.Context, WorkspaceBinding, string) ([]byte, error) {
	return nil, ErrCapabilityUnavailable
}
func replaceWorkspaceFile(context.Context, WorkspaceBinding, PatchArguments) error {
	return ErrCapabilityUnavailable
}

func replaceWorkspaceFileChecked(context.Context, WorkspaceBinding, PatchArguments, func() error) error {
	return ErrCapabilityUnavailable
}
