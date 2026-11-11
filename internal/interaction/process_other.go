//go:build !linux

package interaction

import (
	"context"
	"runtime"
)

func platformExecCapability() Capability { return disabledNativeSupport(runtime.GOOS).Exec }
func inspectExec(review *ExecReview) error {
	return (unavailableNativeAdapter{target: runtime.GOOS}).inspectExec(review)
}
func executePreparedExec(ctx context.Context, w WorkspaceBinding, p preparedTool) (ToolResult, error) {
	return (unavailableNativeAdapter{target: runtime.GOOS}).executePreparedExec(ctx, w, p)
}
