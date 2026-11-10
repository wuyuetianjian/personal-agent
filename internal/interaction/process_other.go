//go:build !linux

package interaction

import "context"

func platformExecCapability() Capability {
	return Capability{Reason: "NON-SANDBOXED exec unavailable on this platform: no natively qualified executable identity and child cleanup implementation"}
}
func inspectExec(*ExecReview) error { return ErrCapabilityUnavailable }
func executePreparedExec(context.Context, WorkspaceBinding, preparedTool) (ToolResult, error) {
	return refused(ErrCapabilityUnavailable)
}
