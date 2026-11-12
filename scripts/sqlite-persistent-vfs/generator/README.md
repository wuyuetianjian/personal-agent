# AST inventory and review-only variants

This tool is implemented; an accepting VFS is **not**. It emits a `.go.txt`
review artifact without a package/import block or registration. It must NOT be
renamed into a compilable library file. The renamed stock open/close helpers
still contain unsafe pathname/cleanup paths. Extraction is not authorization.

The tool itself is a standard-library-only Go command in the agent module:

```
go test ./scripts/sqlite-persistent-vfs/generator
go run ./scripts/sqlite-persistent-vfs/generator -source "$VERIFIED_SQLITE_MODULE" -propose
```

`-propose` prints a candidate manifest only. Review it rather than automatically
accepting changes. The checked-in `manifest.json` fixes the present inputs,
closure, every function byte-span SHA, call/ref/syscall counts, AST rename counts,
per-function variant SHA and complete output SHA. Generation requires it:

```
go run ./scripts/sqlite-persistent-vfs/generator \
  -source "$VERIFIED_SQLITE_MODULE" \
  -manifest scripts/sqlite-persistent-vfs/generator/manifest.json \
  -cache-root "$VERIFIED_GOMODCACHE" \
  -out "$OWNED_TEMP_DIRECTORY/private-review.go.txt"
```

Use the existing runner's exact official download/checksum/module-selection and
`go mod verify` checks first. This tool supplements that verification with fixed
file hashes; it does not authenticate the entire module alone. Output must be a
new file, outside both the source directory and the entire supplied cache-root.
The runner must supply its physically resolved `go env GOMODCACHE`, and use its
owned temporary workspace, as the existing runner does; never point output
at any cache or production path. It neither edits original files nor selects a
VFS. The upstream BSD notice and disclaimer are retained verbatim as comments
in every artifact; LICENSE bytes are pinned. No downloaded code is executed.

## Scope and closure

Roots: `_winOpen`, `_winRead`, `_winFileSize`, `_winLock`, `_winUnlock`,
`_winCheckReservedLock`, `_winClose`. The pinned transitive `_win*` direct-call
closure contains 29 functions, including readonly `_winOpen` recursion,
`_winAccess`, conversions, error logging, retry policy, temp-name helpers and
`_winUnmapfile`. Declaration matching is unique, not text search. Function names
are rewritten only at AST identifier spans; comments and string contents are
not rewritten. Counts include declaration identifiers and selected calls.

The manifest inventories `_aSyscall` indirect dispatch indices separately.
It also includes `_win*` non-call references, exposing shared state such as
`_winIoMethod`, global retry parameters and mapping state. This closure is a
review inventory, not a proof that other SQLite/libc calls are harmless. Before
any live variant can be emitted, classify ALL remaining global references and
external calls, and supply exact reviewed transformations that replace resource
acquisition/cleanup and method-table selection. No such transformations exist
in this stage. Official default functions and `_winShmNodeList` are untouched.

## Required refusal boundary for the next linked stage

- SHM mapping/open/locking cannot fall back to stock: return explicit appropriate
  SQLite errors until owner-isolated nodes and qualified resources exist.
- Disable main-file mmap; do not return a stock mapping or map outside registry.
- Unsupported file controls, temp, ATTACH and journal roles explicitly refuse.
  `xAccess` must never fake not-exists for a hot journal to bypass recovery.
- `xOpen` stays refusing until real lease acquisition and real TwinFile
  construction are reviewed. Never plug the extracted `_paWinOpen` directly in.
- `_winClose` retries/ignored unmap errors must be replaced by consume-once owner
  cleanup. Do not call the extracted close to consume a broker-owned handle.
- Keep privateOpenHits separate from refusal counts when success is possible.

A held existing WAL fixture still requires genuine SHM/DMS behavior to read SQL.
This main-file inventory alone cannot make that SQL positive contract green.
No production, arbitrary legacy recovery, or external sidecar compatibility is
claimed. See `docs/release/windows-private-vfs-fork-plan.md` for later stages.

## Opaque bridge signature design (not implemented)

The eventual patch defines the interface in `modernc.org/sqlite/lib`; the
agent-internal adapter implements it. The library must never import
`agent/internal/nativefs/windowsprobe`. A minimal signature shape is:

```go
type OwnerID uint64
type ConnectionID uint64
type OperationID uint64
type ResourceID uint64
type Purpose uint8

type PrivateBridge interface {
    Begin(OwnerID, ConnectionID) (OperationID, error)
    End(OperationID) error
    Acquire(OperationID, Purpose) (ResourceID, error)
    WithHandle(OperationID, ResourceID, func(uintptr) error) error
    Consume(OperationID, ResourceID) error
    Revoke(OwnerID, error)
}
```

This is a design sketch, not a public hook, complete API or callable package.
IDs need generation and owner binding, no arbitrary filenames, no user DSN
capability. WithHandle is trusted internal temporary access, never a tool
interface; its operation permit is separate from the short registry mutex.
Lazy SHM acquisition inside a callback must not upgrade lease.borrow's RWMutex.
No registry mutex may span stock calls or SQLite node mutex acquisition.

The library retains the bridge/bindings until all callback/mapping references
drain. TLS is call-local, never retained in the adapter. C-style callback entry
recovers panic, releases operation permits, latches terminal error and maps it
to SQLite failure. Main IO wrappers require a live identity-qualified binding
before touching true TwinFile.Fh. Close/unmap uses owner accounting, not raw
CloseHandle. Direct stock helper reuse is permitted only after its full syscall
and state dependencies are classified. Write, map, delete and recovery add
separate purpose-specific contracts, not a generic arbitrary syscall hook.

## Next linkable artifact: exact constructor and method-table cut points

After this extraction review, generate a different, explicit linked artifact
rather than compiling the review dump. Coordinate with the independent
`main_read_bridge_windows.go.txt` refusing bridge contract. Proposed internal
symbols (not exported user APIs):

- `_paWinOpenBound`: real pinned constructor variant, host-bound existing MAIN_DB
  and READONLY only. Reject CREATE/DELETEONCLOSE/temp/URI authority and other roles.
- `_paBoundRead`, `_paBoundFileSize`, `_paBoundLock`, `_paBoundUnlock`,
  `_paBoundCheckReservedLock`: validate binding and acquire an operation/resource
  permit, then call the reviewed `_paWin*` algorithm; do not recurse into acquire
  while holding a lease read lock.
- `_paWinCloseBound`: consume the resource lease exactly once; never delegate to
  the extracted `_paWinClose` retry loop. Retain terminal close failure.

AST transformation anchors in the pinned `_winOpen` must each match exact
statement shape and expected count, then receive original/result span hashes:

1. Keep the actual stock zero-initialization statement and invalid-handle
   initialization of `TwinFile`. Verify stock allocation size against the true
   type, not a handcrafted compatible layout.
2. Remove the whole pathname conversion/temp/IsDir/access/CreateFile/readonly
   recursion region. Replace it with the bridge's fixed-purpose acquisition;
   any partial failure is reclaimed via the lease owner. Reuse of that original
   region is forbidden even when a supposedly trusted absolute path is present.
3. Preserve reviewed stock assignments for FpVfs, Fh, readonly control bit,
   FlastErrno, FzPath, FhMap, FpMapRegion and mmap-size fields. Override
   FmmapSizeMax to zero. Never use unchecked URI parameters to set authority or
   optional capability bits. Preserve SQLite-owned filename lifetime correctly.
4. Replace the stock FpAppData/default `_winIoMethod` selection with one private
   table. Publish FpMethods only after lease registration and binding succeeds;
   failure leaves it nil. Set pOutFlags to the actually granted READONLY flags.

Method slots must be assigned individually, not copied wholesale from stock:

| Slot | Main-read-stage policy |
| --- | --- |
| xClose | private consume-once close wrapper |
| xRead | private permit wrapper + stock short-read/zero-fill algorithm |
| xFileSize | private permit wrapper + stock size algorithm |
| xLock/xUnlock/xCheckReservedLock | private permit wrappers + reviewed real byte-range algorithm |
| xWrite/xTruncate | explicit read-only error |
| xSync | explicit unsupported-sync error; no hidden native flush |
| xFileControl | explicit allowlist of proven scalar-only queries; dangerous/unknown controls unsupported, never stock dispatch |
| xSectorSize/xDeviceCharacteristics | reviewed conservative scalar results; do not advertise unproved IO capabilities |
| xShmMap/xShmLock/xShmUnmap | explicit errors, no stock SHM path |
| xShmBarrier | no admitted SHM exists; unexpected use latches terminal failure; do not advertise this as a working WAL barrier |
| xFetch/xUnfetch | explicit unsupported mapping result; never stock map/unmap |

Constructor/method-table transformation must leave no `_winIoMethod`, CreateFile,
DeleteFile, stock close/unmap, `_winShmNodeList` or stock SHM helper reachable in
the linked main-only artifact. Only approved read/size/range-lock/error-helper
algorithms enter it. Logging conversions may remain only after their own syscall
inventory is approved; retry settings require an explicit private policy.

Native direct-VFS contracts can then exercise a real existing main-file header,
size, short-read zero fill, actual lock contention and consume-once close through
that genuine sqlite3_file allocation. This is the next linkable test boundary,
not proof of SQL WAL readability. The existing held-WAL SQL contract must remain
RED while SHM is explicitly unsupported; do not alter fixture journal mode or
skip SHM recovery to turn it green.
