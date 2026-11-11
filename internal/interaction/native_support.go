package interaction

import (
	"fmt"
	"runtime"
)

// Readiness describes compiled implementations, never host or session permission.
type nativeSupport struct {
	Read, Search, Replace, Undo, Exec, MCPStdio Capability
}

func normalizeNativeSupport(s nativeSupport) nativeSupport {
	if s.Search.Enabled && !s.Read.Enabled {
		s.Search = Capability{Reason: "Search unavailable: native Read support required"}
	}
	if s.Replace.Enabled && !s.Read.Enabled {
		s.Replace = Capability{Reason: "Replace unavailable: native Read support required"}
	}
	if s.Undo.Enabled && (!s.Read.Enabled || !s.Replace.Enabled) {
		s.Undo = Capability{Reason: "Undo unavailable: native Read and Replace support required"}
	}
	return s
}

func disabledNativeSupport(target string) nativeSupport {
	label := fmt.Sprintf("unsupported target %q", target)
	switch target {
	case "darwin":
		label = "macOS"
	case "windows":
		label = "Windows"
	}
	files := Capability{Reason: "Native file adapter unavailable on " + label + ": file identity, containment, read, replacement and undo are not natively qualified"}
	return nativeSupport{
		Read: files, Search: files, Replace: files, Undo: files,
		Exec:     Capability{Reason: "NON-SANDBOXED exec unavailable on " + label + ": executable identity and direct-child cleanup are not natively qualified"},
		MCPStdio: Capability{Reason: "MCP stdio unavailable on " + label + ": native transport and direct-child cleanup are not natively qualified"},
	}
}

func currentNativeSupport() nativeSupport {
	if runtime.GOOS != "linux" {
		return normalizeNativeSupport(disabledNativeSupport(runtime.GOOS))
	}
	files := platformFileCapability()
	return normalizeNativeSupport(nativeSupport{Read: files, Search: files, Replace: files, Undo: files, Exec: platformExecCapability(), MCPStdio: Capability{Enabled: true, Reason: "Linux stdio transport available; explicit host configuration and session opt-in required; direct-child cleanup only"}})
}

func nativeFilesCapability(s nativeSupport) Capability {
	s = normalizeNativeSupport(s)
	for i, c := range []Capability{s.Read, s.Search, s.Replace, s.Undo} {
		if !c.Enabled {
			reason := c.Reason
			if reason == "" {
				reason = []string{"Read", "Search", "Replace", "Undo"}[i] + " unavailable"
			}
			return Capability{Reason: "Native file bundle unavailable (Read, Search, Replace, Undo required): " + reason}
		}
	}
	return s.Read
}

func nativeToolCapability(s nativeSupport, name string) Capability {
	s = normalizeNativeSupport(s)
	switch name {
	case "read":
		return s.Read
	case "search":
		return s.Search
	case "apply_patch":
		return s.Replace
	case "exec":
		return s.Exec
	}
	return Capability{Reason: "Unknown native tool"}
}

func nativeMCPCapability(s nativeSupport) Capability {
	s = normalizeNativeSupport(s)
	if !s.MCPStdio.Enabled {
		return s.MCPStdio
	}
	if files := nativeFilesCapability(s); !files.Enabled {
		return files
	}
	return s.MCPStdio
}

// NativeMCPStdioCapability is implementation readiness only, not permission or
// the Files intersection. Host configuration and session gates remain required.
func NativeMCPStdioCapability() Capability { return currentNativeSupport().MCPStdio }
