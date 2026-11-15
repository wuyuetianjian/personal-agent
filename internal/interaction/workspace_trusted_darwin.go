//go:build darwin

package interaction

import (
	"context"
	"errors"
	"os"
)

var errTrustedDarwinUnimplemented = errors.New("trusted Darwin workspace explicit RED")

func trustedWorkspacePlatformCapability() Capability {
	return Capability{Reason: "trusted Darwin workspace implementation pending"}
}
func trustedWorkspaceIdentity(string) (string, error) { return "", errTrustedDarwinUnimplemented }
func readTrustedWorkspaceFile(context.Context, WorkspaceBinding, string) ([]byte, error) {
	return nil, errTrustedDarwinUnimplemented
}
func readTrustedDarwinWithClose(context.Context, WorkspaceBinding, string, func(*os.File) error) ([]byte, error) {
	return nil, errTrustedDarwinUnimplemented
}
