# Windows existing-object broker qualification

Status: implementation pending native qualification, disconnected experiment. No production import or capability
change. `nativeprobe_sqlite_identity` is required. No SQLite connection uses it.

## Decision and boundary

Preserve existing main/WAL/SHM bytes and external readers. Do not migrate, truncate,
create sidecars, change journal mode, emulate stock `winFile`, or reuse TEMP's
in-process-only locks. A future persistent VFS needs Windows byte-range locking,
shared-memory mappings and barriers, crash recovery and stock-reader interop.
Stock modernc v1.58.0 SHM acquisition bypasses VFS xOpen: it creates a mapping/DMS
handle plus per-connection local lock handles. Wrapping xOpen alone is insufficient.

This first broker owns three **existing** regular NTFS objects for a bound exact
basename: main, basename-wal and basename-shm. Missing sidecars refuse; this is not
a deployable opening policy. Each acquisition uses metadata then a separately
opened read/write handle, retains metadata through identity verification, and
never performs content IO. The read/write handle is future storage authority,
not an enabled write API. It is non-inheritable, excludes DELETE access and uses
SHARE_READ|SHARE_WRITE without SHARE_DELETE, allowing ordinary external readers.
No user-token, ACL, privilege, global SQLite or security-policy changes occur.

## Private API and ownership

`newPersistentBroker(root, basename, hooks)` owns independent lineage duplicates.
`acquireExisting(ctx, role)` allows exactly one object per fixed role; it qualifies
identity, regular type, exact name and same volume before registering/publishing.
No native handle escapes. Roles are bounded, no caller-supplied sidecar path.
`readWorkspace(ctx, root, parts)` is the experimental consumer: it refuses until
all three roles are published, and keeps all protected owned objects live while
using the existing pre-content-IO identity exclusion path. It is not general
storage isolation: future files, renames/hardlinks by other actors, and other
storage directories are outside this slice. Protection belongs to this broker
host lifetime, not a SQL connection lifetime. Closing the broker permanently
refuses its consumer; it must never fall back to unprotected Root.Read for old
files. A new host must reacquire/requalify storage before admitting any reads.
`close()` permanently revokes admission, drains admitted operations, consumes all
owned handles once and retains any cleanup error. Close is never retried after
an uncertain result, and errors never restore admission. Registration/read/close
serialize under a broker lock; lock order is broker then Root. Cancellation is
checked before acquisition and after waiting for this lock. Synchronous Windows
calls are not given a hard cancellation deadline.

Hooks are private experimental fault/ordering seams, not caller-facing authority.
After-metadata hook exercises real namespace swaps; after-registration hook
pauses publication while concurrent read/close waits. Close hooks must consume
fixture handles, permitting an error-after-close without a test-induced leak.

## Self-review and acceptance

- Data handles require WRITE sharing; reusing ordinary read guards would prevent
  external SQLite connections. Sharing alone is not a lock protocol.
- Metadata-only guards do not protect names: retain identity and recheck the
  separately opened candidate. A swap must fail without any content read/write.
- Holding a directory does not prevent reparse metadata changes: all relative
  opens and current directory qualifications must still reject unsafe state.
- File bytes are independently hashed/read outside the broker before/after.
- Tests require no-skip native execution, positive external-reader controls,
  ownership after source-root close, publication gate, cancellation, close races,
  terminal cleanup errors and exact disk-handle restoration.
- Initial RED stub deliberately refuses construction. Its native failures only
  prove missing broker behavior; downstream assertions are not yet evidence.

## Test-first evidence and implementation status

The first run at `259125a` failed during fixture root acquisition because the
runner temp path contained an 8.3 ancestor alias. That is not a feature RED.
The fixture-only revision `7153513` reuses the established handle-derived long
path helper without relaxing production exact-name checks. Its native run
`43207232128` reached the refusal stub: all 24 required top-level/subtest nodes
ran and failed three times at the missing broker or missing lexical refusal.

The candidate implementation now owns lineage duplicates, retains the metadata
handle until the separate read/write candidate passes exact identity/snapshot
checks, and closes metadata successfully before registration. Existing-file
opens use FILE_OPEN only. Read admission serializes with registration and final
close and requires all three existing roles; broker-owned acquisition/final-close
cleanup errors retain terminal refusal. The reused `Root.read` and its I/O guard
still ignore some temporary-handle close errors; this stage does not strengthen
that inherited cleanup contract or claim complete read-resource error reporting.
This is pending exact-head Windows qualification, not a claim of native
GREEN, complete private-storage isolation, WAL locks or standard-user support.
