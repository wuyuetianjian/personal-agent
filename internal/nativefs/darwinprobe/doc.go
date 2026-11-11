// Package darwinprobe is a deliberately disconnected experimental Darwin/APFS
// directory and metadata observation foundation. It is not a native file adapter
// and must not enable Files, Exec or MCP capabilities.
//
// The Darwin-only API starts with an explicitly supplied, already-open trusted
// directory, duplicates it, and retains every directory handle in the walk.
// Paths are validated single components. Directories are opened no-follow;
// leaves are inspected only with Fstatat(AT_SYMLINK_NOFOLLOW), never opened.
// There is no content read, search, replacement, undo or approval operation.
//
// Observations describe pinned directory objects rather than their current
// pathname ancestry. They do not promise atomic RESOLVE_BENEATH or NO_XDEV
// semantics under hostile rename or automount. Device and filesystem comparisons
// reject observed mismatches, not every possible namespace race. Leaf metadata
// can change immediately after inspection. Identity is only a process-lifetime
// observation, has no persistent anti-reuse guarantee, and is not an approval
// token. This package must remain unimported by production consumers until a
// separately reviewed and qualified adapter design exists.
package darwinprobe
