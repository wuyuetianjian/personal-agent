package interaction

import (
	"context"
	"fmt"
)

// This refusal implementation deliberately performs no I/O, validation,
// inspection or callbacks, even when the supplied target label is "linux".
type unavailableNativeAdapter struct{ target string }

func (a unavailableNativeAdapter) workspaceIdentity(root string) (string, error) {
	return "", fmt.Errorf("%w: %s", ErrCapabilityUnavailable, nativeFilesCapability(disabledNativeSupport(a.target)).Reason)
}
func (a unavailableNativeAdapter) readWorkspaceFile(ctx context.Context, w WorkspaceBinding, name string) ([]byte, error) {
	return nil, ErrCapabilityUnavailable
}
func (a unavailableNativeAdapter) replaceWorkspaceFile(ctx context.Context, w WorkspaceBinding, args PatchArguments) error {
	return ErrCapabilityUnavailable
}
func (a unavailableNativeAdapter) replaceWorkspaceFileChecked(ctx context.Context, w WorkspaceBinding, args PatchArguments, beforeRename func() error) error {
	return ErrCapabilityUnavailable
}
func (a unavailableNativeAdapter) inspectExec(review *ExecReview) error {
	return ErrCapabilityUnavailable
}
func (a unavailableNativeAdapter) executePreparedExec(ctx context.Context, w WorkspaceBinding, p preparedTool) (ToolResult, error) {
	return refused(ErrCapabilityUnavailable)
}
