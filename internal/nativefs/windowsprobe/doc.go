// Package windowsprobe is a disconnected experimental Windows/NTFS pinned-
// handle read foundation. It is not a production adapter, capability, or
// authorization front end. No Files, Exec, MCP, replacement, undo, or execution
// readiness is supplied here. Its Windows-only API accepts a trusted existing
// directory handle and exact component names, never an untrusted root path.
//
// The root caller must establish local NTFS directory authority and no-delete
// sharing before NewRoot. FILE_LIST_DIRECTORY, FILE_READ_ATTRIBUTES and
// SYNCHRONIZE granted rights are verified; metadata-only roots and roots with
// DELETE access are rejected.
// Duplication owns the handle but cannot verify/strengthen its original share mode. Drive letters, SUBST, UNC paths, namespace rebinding and
// caller approval persistence are intentionally outside this API. No approval
// may be restored from timestamps or a persisted file ID. Creation time is not
// a permanent anti-reuse token.
//
// Metadata opens use NtCreateFile, one validated basename relative to a retained
// parent, FILE_OPEN, read-attributes/synchronize access, OPEN_REPARSE_POINT and
// OPEN_NO_RECALL. There is no backup intent, privilege enablement, inheritance,
// case-insensitive object flag, path-prefix authority, or permissive retry.
// Type-neutral opens avoid the undocumented DIRECTORY_FILE/OPEN_REPARSE_POINT
// combination. Reparse tags, recall/offline/device flags, non-local/non-NTFS
// storage, delete-pending objects, volume changes, case-sensitive directories,
// nonexact normalized long names, ADS, reserved DOS names and protected storage
// components are rejected. OBJ_DONT_REPARSE is not asserted without runtime
// qualification; open-reparse metadata is classified before any data open.
//
// Metadata-only handles retain object lifetime but do not prevent rename.
// After metadata inspection, FILE_READ_DATA/FILE_LIST_DIRECTORY guards deny
// ordinary write/delete sharing. Each guard must match the metadata handle's
// full volume serial plus 128-bit file ID before traversal or reading.
// Metadata and qualified I/O parent/leaf handles remain open throughout. Pre/post checks are not a
// filesystem snapshot, a global-hardlink exclusion, or proof that mappings,
// kernel components, filters, or preexisting unusual handles cannot mutate
// bytes. Observed file link counts other than one are rejected before and after
// reads, but this is not a global or race-free hardlink exclusion guarantee.
// Synchronous calls cannot guarantee hard cancellation or zero filter effects.
//
// Modern POSIX rename/delete, mapped writers, cloud/filter drivers, hostile
// concurrent namespace transitions, 8.3 configuration variants, directory
// case-sensitivity transitions, restart/approval restoration and root-binding
// front ends remain unqualified. Native tests are targeted evidence only, not
// production/security qualification. Fixture failure is never a passing skip.
package windowsprobe
