# Private Windows VFS fork: implementation plan, not qualification

## Status and invariants

**Not implemented.** The non-default private VFS remains a refusing `xOpen`
stub. Its real SQL positive contract is still RED: an independent stock process
creates and holds a synthetic committed WAL; parent default-stock reads succeed
before and after private SQL reaches the refusing callback. This is evidence of
fixture validity and private dispatch, not a working fork. Independent resource
leases are a separate test-first stage. Neither stage qualifies production.

Preserve existing database bytes, WAL mode and external stock-reader semantics.
Do not migrate legacy databases, substitute copies/in-memory databases, switch
journal mode, force PERSIST_WAL, prohibit mixed-directory neighbors, or change
ACLs to make the experiment pass. The present host-long no-share-delete guards
are an explicit sidecar retirement blocker, not a final lifecycle policy.

Dedicated runtime-directory tool exclusions and storage namespace integrity are
separate properties. Directory handles do not prevent WRITE_ATTRIBUTES junction
mutation. Absolute stock pathname opens can follow redirected ancestors, even
with ABA restoration between checks. A dedicated directory does not remove the
need for handle-relative acquisition under the hostile namespace threat model.
External processes can also create SHM without entering the in-process registry.
That confidentiality/admission problem needs its own proof.

## 1. Reproducible fixed-source variant generation

Use official `modernc.org/sqlite v1.58.0`, its exact module/go.mod checksums and
the temporary module-copy runner. Never edit the shared module cache or the
production module/driver. Keep the official files unchanged in the copy; emit
additional same-package `sqlite3` variant files behind experimental Windows tags.
Initially support amd64/arm64 only, with an explicit rejection of other layouts.

The source manifest must cover:

- `sqlite.go` (driver), `lib/sqlite.go` (real types and callback machinery),
  `lib/sqlite_windows.go`, and `lib/sqlite_g_0000000000060000.go`.
- Exact SHA256 of every input file, in both verified module source and copy.
- Each extracted function's declaring file, unique declaration count **1**, and
  SHA256 of its exact original source byte span.
- For every transformation: function, AST node kind, original identifier/call,
  expected occurrence count, replacement, and resulting function hash.
- Exact generated-file hashes, generator revision and formatting toolchain.
  Pin patch/manifest checkout bytes with `text eol=lf` and test actual Git
  checkout with `core.autocrlf=true`; do not normalize unexpected bytes to pass.

Parse with Go AST/token positions. Extract complete declarations; use reviewed
node-specific edits for renaming and explicit owner arguments. Do not globally
regex-replace names, comments, string literals or unrelated default functions.
Keep original `TwinFile`, `TwinShm`, `TwinShmNode` layouts and `__ccgo_ts` offsets
from that same pinned source. A synthetic compatible-looking `winFile` is not
acceptable. Default method tables, registration, `_winShmNodeList` and `_aSyscall`
remain unchanged. New `_paWin*` variants use a separate private method table.

After generation, audit all calls and global references, including indirect
`_aSyscall[index]` dispatch: every retained stock helper and syscall index must
have an explicit classification. A new/unclassified call, absent anchor,
changed count, changed hash or altered original source fails the build. Hashes
are reproducibility checks, not substitutes for reviewing the transformations.
Native tests must select the private VFS explicitly while exercising ordinary
stock connections in the same process to detect accidental global effects.

## 2. Owner-specific state without changing stock globals

Generate one reviewed variant implementation, not new source per runtime owner.
Each non-default VFS instance carries an opaque owner ID and its own method table
binding. Maintain side tables for:

- actual `winFile` allocation -> owner, connection, resource lease, generation;
- private SHM node -> owner, qualified file identity, mapping/DMS resource;
- each `winShm` connection -> independently opened SHM locking resource;
- mapping objects and views -> owning node, lifecycle and cleanup ledger.

Use allocation generations as well as pointer/handle values to reject reuse.
A private SHM node list and mutex must never attach to stock `_winShmNodeList`
or another owner's nodes. Within one owner, node sharing depends on qualified
object identity, not stock's case-insensitive filename match. Preserve the
actual shared-memory layout and OS byte-range protocol for external readers.
Mapping/DMS and per-connection locking resources are distinct independent opens,
not arbitrary DuplicateHandle substitutes. Private node isolation is necessary,
but does not itself prove cross-process locking or sidecar deletion compatibility.

## 3. Go module boundary, bridge and callback lifetime

The patched `modernc.org/sqlite/lib` cannot import
`agent/internal/nativefs/windowsprobe`. Define the minimal opaque-ID/callback
interface in the patched library. An agent-internal adapter implements it and
imports the library, with no reverse dependency or import cycle. The adapter
retains actual owner/connection/lease objects. Registration is an internal host
operation, not an arbitrary user hook or DSN-supplied authority.

Library bindings keep bridge objects alive until all SQLite references, admitted
callbacks, SHM nodes and mappings drain. Never retain a TLS pointer for later or
cross-goroutine use. A missing, foreign, stale or consumed binding refuses IO.
Raw handles are available only temporarily to trusted internal stock-variant
code, never to tools or externally callable file-control interfaces. If the real
`winFile.Fh` stores a native handle, that integer alone confers no admission:
every use must be covered by its live binding and resource permit.

At each SQLite C-style callback boundary, recover internal panics, release permits
with defer, latch the owner terminal error and return the appropriate SQLite
error. Panics must not unwind through the translated SQLite callback machinery.
The lower-level trusted lease callback may rethrow after releasing admission;
the outer bridge is responsible for conversion. Void destructor/barrier paths
need an out-of-band terminal error ledger; they cannot silently discard failures.

Host shutdown first blocks new admission, drains admitted operations, consumes
resources once, then releases binding state and eventually VFS registration/name
storage. Do not free VFS or side-table memory while SQLite can still refer to it.
A shutdown error cannot restore workspace admission. Unknown native close/unmap
outcomes are terminal; no retry of an already-consumed handle number.

## 4. Operation permits are not registry locks

The current lease-stage borrow holds an owner RWMutex read lock; acquire requires
the write lock. Wrapping all of `xShmMap` in that borrow is invalid:

`xShmMap -> _winOpenSharedMemory -> acquire(mapping/DMS or connection-lock)`

would upgrade the same read lock and self-deadlock. Before accepting callbacks,
separate the lifetime admission/drain permit from the short registry mutex, or
use explicitly proved phased acquisition. Do not simulate a reentrant lock.

A callback's operation permit remains live across lazy resource acquisition and
stock work. Registry locks protect membership/state only and are released before
stock calls, bridge callbacks or SQLite node mutex acquisition. Establish and
audit a lock order covering owner state, SQLite node/list mutexes, broker and
workspace Root. No path may hold registry lock while waiting for a node that
will call back to acquire registry lock. Resource close waits for outstanding
resource borrows; owner close waits for operation permits. Multi-resource SHM
operations validate every resource under the same owner and operation lifetime.

## 5. Transitive resource closure to intercept

The names below are from the pinned sources, not a claim that replacing `xOpen`
alone is sufficient. Inspect direct `_aSyscall` calls inside every variant,
including allocation/open/map failure cleanup and readonly fallback recursion.

| Entry or helper | Transitive boundary requiring private control |
| --- | --- |
| `_winOpen` | `_winIsDir`, `_winGetTempname`, `_winAccess`, path CreateFile and readonly retry; retain true stock construction but replace acquisition with fixed-purpose qualified leases. |
| `_winOpenSharedMemory` | Global filename-keyed node lookup; `_winHandleOpen` for mapping/DMS and each connection-lock handle; direct failure-path CloseHandle; `_winCloseSharedMemory`. |
| `_winHandleOpen` | Path attributes, CreateFile OPEN_ALWAYS and readonly recursion bypass ordinary VFS xOpen. |
| `_winCloseSharedMemory` | Direct per-connection handle close; list removal; `_winShmPurge`; node and connection allocations. |
| `_winShmPurge` | Mapping-view unmap, mapping-handle close, `_winHandleClose`, optional `_winDelete`; stock ignores several errors. |
| `_winShmMap` | Lazy SHM open, `_winLockSharedMemory`, `_winHandleSize`, `_winHandleTruncate`, direct CreateFileMapping/MapViewOfFile and failure cleanup. |
| `_winLockSharedMemory` | Real DMS locking, legitimate truncate-to-zero after exclusive acquisition, then shared DMS; preserve recovery ordering. |
| `_winClose` | `_winUnmapfile`, retrying CloseHandle loop; replace with ledger-backed consume-once close. |
| `_winFileControl` | `_winTruncate`, `_winMapfile`, `_winUnmapfile`, `_winGetTempname`; audit every control, especially NULL_IO, WIN32_GET_HANDLE, SIZE_HINT, MMAP_SIZE, TEMPFILENAME, PERSIST_WAL and WIN32_AV_RETRY. |
| `_winFetch`, `_winUnfetch` | `_winMapfile` / `_winUnmapfile`; cannot bypass mapping registry. |
| `_winMapfile`, `_winTruncate` | Size changes, map creation and unmap/re-map; nested cleanup must retain owner accounting. |
| `_winShmUnmap` | `_winCloseSharedMemory`, including optional sidecar deletion. |
| `_winDelete`, `_winAccess`, full-path helpers | Path-based metadata/deletion must not become uncontrolled stock fallback. |

Preserve the actual stock lock algorithms under qualified live-resource permits:
`_winLock -> _winGetReadLock/_winLockFile/_winUnlockFile`,
`_winUnlock -> _winUnlockReadLock/_winGetReadLock`, `_winCheckReservedLock`, and
`_winShmLock -> _winHandleLockTimeout/_winHandleUnlock`. These operate on handles,
but still require correct owner/node association and lifetime coverage. Preserve
real pending/reserved/shared lock ranges, SHM lock offsets, DMS and busy behavior;
do not replace them with process-local locks.

Read/Write/Sync/Size can retain reviewed stock handle-IO algorithms. Their full
helper closure still needs auditing: `_winRetryIoerr` reads global retry policy,
which ordinary WIN32_AV_RETRY controls can change. Use private retry policy if
isolation requires private behavior not to change with an ordinary connection.
`_winShmBarrier` must remain a real memory-ordering barrier using an appropriate
private mutex, not a no-op. Read-only platform geometry (`_winSysInfo`) and normal
SQLite allocators/configuration can be explicitly permitted shared infrastructure;
that permission does not extend to shared SHM membership or resource authority.

## 6. xAccess and unsupported routes

Unsupported does **not** mean absent. Never answer false/not-exists for an
unsupported rollback journal, ATTACH target, temp file or sidecar merely to let
SQLite continue. In particular, concealing a hot rollback journal can suppress
required recovery and expose inconsistent database contents.

For supported, host-bound roles, use qualified handle-relative existence and
access checks with correct SQLite flag semantics. An absent result requires a
proved absence at that supported boundary. Unsupported, ambiguous or unsafe
checks return an explicit SQLite error. Acquisition must independently qualify
the object: an xAccess result is not an authority token against subsequent races.
No arbitrary filename/URI is admitted by the private host interface. Dynamic
loading and syscall replacement remain refused. Unsupported file controls,
ATTACH, journals, temp, mmap and network paths fail explicitly until covered.

## 7. Smallest accepting read slice and later compatibility contracts

Before opening any private file, replace the misleading future use of
`ExperimentalPersistentRefusals` with an independent **privateOpenHits** counter.
Every private xOpen entry increments hits; refusal has its own counter. Tests
must require private hits on both success and failure, so a default-VFS fallback
cannot turn the positive contract green. Successful IO must not increment refusal
just to satisfy an old test.

The first accepting slice is a fixed, host-created synthetic fixture: an
independent stock process has created the table, committed its row into WAL and
kept a connection live. Its qualified main/WAL/SHM identities already exist.
Private SQL reads that actual committed value; default-stock reads work before
and after private registration/use in the same process. Do not substitute a
copied database or make the private connection read from a stock connection.

"Read slice" describes SQL behavior, not an assurance of no native writes:
SQLite WAL readers may initialize/map SHM and take locks. Implement the required
real SHM/DMS behavior and mapping lifetime, or explicitly refuse conditions that
require unsupported mutation/recovery. Do not fake absent journals or skip DMS
to obtain a read. Disable unsupported main-file mmap explicitly, without silently
substituting unsupported SHM behavior. Record actual opened identities, registry
publication, maps and consumption; missing boundary coverage fails the slice.

This constrained fixture does **not** prove arbitrary legacy DB opening, hot
journal recovery, missing/stale SHM recovery, sidecar retirement, or writes.
Native amd64/arm64 compile plus real Windows execution is required; cross-compile
alone is not acceptance. Follow with:

1. Stock reader holds an old snapshot; private writer commits; held snapshot
   remains old and a fresh stock transaction observes the new committed row.
2. Writer contention/busy results, checkpoint while readers hold snapshots,
   connection close/reopen and two private owners without node/lease crossover.
3. Legitimate stock sidecar deletion/recreation after final-reader release,
   absence of permanent no-share-delete blockers, and retained tool protection.
4. Committed WAL after process death, missing/stale SHM reconstruction with real
   DMS ordering, reopen recovery and integrity_check against expected rows.
5. Explicitly supported rollback/hot-journal, ATTACH, subjournal/superjournal,
   temp and mmap routes, each with acquisition/cleanup and failure contracts.
6. Concurrent namespace mutation and workspace admission/drain tests, including
   external stock-created sidecars. Do not infer these from SQL correctness.

Keep each stage's complete source backup and exact native evidence. Passing one
stage is not permission to enable production or claim the remaining contracts.
