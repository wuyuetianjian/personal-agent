//go:build !linux

package interaction

import (
	"context"
	"runtime"
)

func platformFileCapability() Capability { return disabledNativeSupport(runtime.GOOS).Read }
func workspaceIdentity(root string) (string, error) {
	return (unavailableNativeAdapter{target: runtime.GOOS}).workspaceIdentity(root)
}
func readWorkspaceFile(ctx context.Context, w WorkspaceBinding, name string) ([]byte, error) {
	return (unavailableNativeAdapter{target: runtime.GOOS}).readWorkspaceFile(ctx, w, name)
}
func replaceWorkspaceFile(ctx context.Context, w WorkspaceBinding, args PatchArguments) error {
	return (unavailableNativeAdapter{target: runtime.GOOS}).replaceWorkspaceFile(ctx, w, args)
}
func replaceWorkspaceFileChecked(ctx context.Context, w WorkspaceBinding, args PatchArguments, beforeRename func() error) error {
	return (unavailableNativeAdapter{target: runtime.GOOS}).replaceWorkspaceFileChecked(ctx, w, args, beforeRename)
}
