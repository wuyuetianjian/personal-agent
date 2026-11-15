package interaction

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TrustedWorkspaceWarning is the explicit narrower host contract, not a sandbox claim.
const TrustedWorkspaceWarning = "Explicit trusted workspace; read/search only. Not isolation from other programs maliciously replacing files, and not detection of all historical database copies. Windows non-administrator acceptance remains unverified."

// TrustedWorkspaceCapability describes the explicitly selected, read-only host
// mode, not the complete native file bundle or permission for any session.
func TrustedWorkspaceCapability() Capability { return trustedWorkspacePlatformCapability() }
func workspaceNativeSupport(p WorkspacePolicy) nativeSupport {
	if !p.TrustedWorkspace {
		return currentNativeSupport()
	}
	no := Capability{Reason: "Trusted workspace permits Read/Search only; replacement, undo, processes and MCP are unavailable"}
	read := trustedWorkspacePlatformCapability()
	if !p.trustedHostGranted {
		read = Capability{Reason: "Trusted workspace requires current host opt-in for this exact workspace root"}
	}
	return normalizeNativeSupport(nativeSupport{Read: read, Search: read, Replace: no, Undo: no, Exec: no, MCPStdio: no})
}
func workspaceIdentityForPolicy(p WorkspacePolicy) (string, error) {
	if p.TrustedWorkspace {
		if !p.trustedHostGranted {
			return "", ErrCapabilityUnavailable
		}
		return trustedWorkspaceIdentity(p.Root)
	}
	return workspaceIdentity(p.Root)
}
func safeTrustedWorkspacePath(name string) error {
	if e := safeWorkspacePath(name); e != nil {
		return e
	}
	for _, part := range strings.Split(name, "/") {
		if strings.Contains(part, ":") || strings.TrimSpace(part) != part || strings.HasSuffix(part, ".") || strings.EqualFold(part, "data") || strings.EqualFold(part, "node_modules") {
			return fmt.Errorf("protected trusted workspace path")
		}
	}
	return nil
}

// Excludes the reserved canonical names and currently known live objects only.
// It does not identify historical copies or protect against hostile replacement.
func trustedWorkspacePathProtected(w WorkspaceBinding, path string, info os.FileInfo) error {
	actual, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	actual = filepath.Clean(actual)
	for _, protected := range w.Policy.ProtectedPaths {
		if !filepath.IsAbs(protected) {
			return fmt.Errorf("invalid protected workspace binding")
		}
		if strings.EqualFold(actual, filepath.Clean(protected)) {
			return fmt.Errorf("agent storage is protected")
		}
		// A canonical string alone misses aliases of the parent directory when
		// a reserved sidecar is absent. Compare its existing parent identity too.
		if strings.EqualFold(filepath.Base(actual), filepath.Base(protected)) {
			parent, pe := os.Stat(filepath.Dir(actual))
			if pe != nil {
				return pe
			}
			reservedParent, pe := os.Stat(filepath.Dir(protected))
			if pe != nil {
				return pe
			}
			if os.SameFile(parent, reservedParent) {
				return fmt.Errorf("agent storage name is protected")
			}
		}
		known, e := os.Stat(protected)
		if e != nil {
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			return e
		}
		if info != nil && os.SameFile(known, info) {
			return fmt.Errorf("agent storage identity is protected")
		}
	}
	return nil
}
func workspaceContainsPath(root, path string) bool {
	root = strings.ToLower(filepath.Clean(root))
	path = strings.ToLower(filepath.Clean(path))
	rel, e := filepath.Rel(root, path)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func workspacePathForPolicy(p WorkspacePolicy, name string) error {
	if p.TrustedWorkspace {
		return safeTrustedWorkspacePath(name)
	}
	return safeWorkspacePath(name)
}
