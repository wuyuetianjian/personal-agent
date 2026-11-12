# Private existing-WAL + readonly-SHM SQL slice

Design and RED contracts only, 2026-11-12. No changes to existing GREEN
candidates. Builds require a new explicit `nativeprobe_sqlite_private_shm` tag
in addition to the persistent/main-read/existing-WAL experimental tags.
Production capabilities remain disabled. This slice must not migrate databases,
change journal mode, copy WAL into an alternate database, force immutable mode,
use a heap-index substitute, or break external stock-reader compatibility.

Source authority: [pinned SHM review](windows-shm-source-review.md). This plan
supports existing local-file SHM while an independent stock connection holds a
valid committed WAL/index. It does **not** implement first-opener initialization,
SHM creation/growth, write transactions, recovery, checkpoint or deletion.
Those branches remain explicit failures and release blockers.

## 1. What blocks real SQL before SHM

The current existing-WAL bridge accepts main READONLY|MAIN_DB (0x101), whereas
URI-based database/sql selection of the private VFS can deliver main
READONLY|URI|MAIN_DB (0x141). Its WAL branch expects 0x80006 and reports RO.
Main factory still installs refusing `persistentAccess`; the independently
qualified metadata VFS has another registration/binding. Consequently, adding
only xShmMap cannot yield truthful SQL success.

Create one new prepared registration with all of these ready **before** its
single VFS publication:

1. Main open accepts exact 0x101 or 0x141 for the canonical bound main only.
   URI is a transport flag, not expanded path authority. Reuse the existing
   main initializer by stripping only SQLITE_OPEN_URI after exact validation,
   without changing old mode's accepted inputs. Preserve the original SQLite
   filename allocation and URI lifetime. Reject other roles, writable/create
   main flags and unqualified names. Do not fabricate a shortened C filename.
2. WAL open reuses the qualified existing readonly WAL path and initializer.
   A CREATE bit in SQLite's 0x80006 request never authorizes file creation.
3. xAccess calls this **same binding's** owner-qualified metadata authority,
   not the separate Access VFS or a stock callback. Support precisely the
   owner's canonical main/WAL/journal roles and SQLite EXISTS/READ/READWRITE
   semantics already proved in metadata. Return false only for qualified
   absence. A present unsupported/hot journal must cause a failure, not be
   concealed to suppress recovery. Positive readwrite probing must represent
   the authority's actual access policy, not path existence.
4. The main file gets a distinct private-SHM method table; WAL retains the
   existing readonly byte-I/O table. xShm callbacks are valid only on a live
   main role belonging to this binding/generation. Do not alter shared default
   method tables after registration or share SHM nodes with ordinary stock.

Canonical-path checks must remain within this owner's authority. Test URI
variants with extra parameters (`immutable`, `nolock`, alternate `vfs`, alternate
mode) rather than letting them silently bypass mapping/locks. The test fixture
uses only the approved private-VFS URI; any supported URI surface needs an
explicit allowlist and qualification. Do not rely on URI stripping to neutralize
SQLite core options it already parsed.

## 2. Resource and state model

One private node per owner generation plus qualified main and SHM identities.
Use an owner-private node registry, not stock's case-insensitive filename list.
Each attached main connection has its own independently opened SHM lock lease;
node owns a separate SHM mapping/DMS lease. Existing-only, handle-relative,
regular-file qualified acquisition; no `_winHandleOpen`, pathname reopen,
process-wide syscall override or DuplicateHandle substitution.

Suggested node state: acquiring -> DMS-held -> mapped -> draining -> closed,
with terminal poisoned state. Store node mutex, connection set, readonly flag,
geometry, mapping objects/base views/returned pointers, DMS state and cleanup
ledger. Each connection stores shared/exclusive slot masks and generation.
No registry mutex is held across broker acquisition or node/native operations.
The lock order and shutdown ownership must be reviewed before implementation.

A mapping is retained authority, not an ordinary callback borrow. SQLite reads
mapped memory between xShm callbacks. Retain the owner and all resources while
any connection can retain that pointer. Owner close blocks new SQL/admission,
waits for active SQL/statement users and connection teardown, then unmaps and
closes. A callback counter reaching zero is **not** sufficient. Cancellation
may request sqlite3_interrupt but must not release views until execution and
statement teardown complete. A timeout leaves resources safely retained and
reports blocked shutdown; it must not force-unmap under a reader.

## 3. Minimal real SHM algorithms

### DMS

On first private node map, use the node lease to attempt exclusive immediate
LockFileEx at byte128 length1. If successful, this narrow slice has no proven
other holder: release the lock and return terminal SQLITE_IOERR_SHMOPEN. Never
truncate. Failed release poisons the owner. A true lock-conflict permits shared
DMS acquisition at the same byte; retain shared DMS until node teardown.
Unexpected native errors are IOERR, not evidence of an external reader.
The stock `_winHandleLockTimeout` conflates every native failure with BUSY, so
this admission classifier must inspect the native result/error explicitly.

This is the stock DMS transition with initialization refused, not a new mutex
protocol. The parent test process must keep its stock fixture holder alive;
private DMS retains protection after successful attachment. Shared DMS does not
prove header validity; SQLite's header checks remain mandatory.

### Readonly xShmMap

Validate region >=0, size exactly32768, extend=0 for this slice, consistent
geometry/region sizes, checked products and native-width limits. Obtain current
size from the qualified node handle; missing/short regions return terminal
SQLITE_IOERR_SHMMAP rather than NULL+OK. No growth or writable map.
Map real file pages with PAGE_READONLY=2, FILE_MAP_READ=4. Align offset down to
allocation granularity, retain the base view and mapping object; return
base+shift. A second 32-KiB region on 64-KiB geometry must be tested.

On successful readonly mapping return **SQLITE_READONLY (8) with nonzero pp**,
as pinned `_winShmMap` does. `_walIndexPageRealloc` records WAL_SHM_RDONLY and
turns plain READONLY into OK. Returning OK would falsely advertise a writable
index. Return NULL on failures. Never return READONLY_CANTINIT (1288) as a
refusal: core can enter heap-index recovery. Invalid mapped headers may lead to
READONLY_RECOVERY (264); treat this as expected unsupported recovery, not success.
Instrument actual mapping/DMS/lock operations so SQL cannot pass via heap index.

### xShmLock / barrier

Validate exact flags 5/6/9/10, 0<=offset<8, 1<=n<=8-offset, and the SQLite
shared-lock range constraints before any shift or native call. Use OS byte
120+offset for n bytes; slots write0, checkpoint1, recovery2, readers3..7.
Each local connection uses its independent lock handle. Update masks only after
successful lock/unlock. Preserve BUSY on real contention; unexpected errors
and failed unlocks enter the failure ledger. No process-local mutex substitutes
for these cross-process locks. UNC/network paths are explicitly unsupported.

Main-database readonly, WAL-file readonly and SHM readonly are different
properties. In pinned `lib/sqlite_windows.go:107665-107668`, WAL open outflags
READONLY sets `WAL_RDONLY=1`; successful readonly xShmMap sets the separate
`WAL_SHM_RDONLY=2` bit at 119256-119261. In `_walTryBeginRead` at 120233-120253,
exclusive reader-slot acquisition to update a readmark is explicitly gated on
`(FreadOnly & WAL_SHM_RDONLY) == 0`. Thus a main-readonly connection with writable
SHM can take this path, but our genuinely readonly SHM path skips it. Full
exclusive-slot protocol qualification remains an independent stage-C API goal,
**not a necessary observed step of held-readmark readonly SQL**. Never make a
readonly map writable merely to satisfy that goal.

The stock fixture must establish an appropriate existing readmark. If `mxI==0`,
`_walTryBeginRead` at 120259-120266 returns WAL_RETRY when rc is BUSY, otherwise
READONLY_CANTINIT. This is a core-generated failure, not permission for our VFS
to return CANTINIT as a refusal. The Windows64 `_walBeginReadTransaction` in
`lib/sqlite_g_000000000003fea8.go:2517-2594` retries only rc==-1 and otherwise
propagates this result; `lib/sqlite.go:38076` and the pager wrapper in
`lib/sqlite_g_0000000000063fff.go:25-46` propagate it without switching to heap.
By contrast, `_walIndexReadHdr` in `lib/sqlite_g_0000000000060000.go:113018-113026`
turns CANTINIT **from index-page mapping** into `bShmUnreliable`/heap-index mode.
`_sqlite3WalOpen` also has a separate `bNoShm` heap-mode route at
`lib/sqlite_windows.go:107658-107663`. Merely never returning CANTINIT from the
private VFS therefore does not independently prove every possible SQL path is
free of heap substitution. Stage D must demonstrate real shared-index use on
success, and SQL failure rather than accidental heap success when no suitable
readmark exists. No core hook or behavior change is proposed here.

Barrier uses a real owner-private synchronization primitive, with the same
cross-callback ordering intent as stock's mutex barrier. It must validate main
ownership and record errors out-of-band because callback return type is void.

### xShmUnmap / close

Remove only the calling main's node attachment; consume its connection lock
lease exactly once. Other attached connections retain their mappings/DMS.
Last attachment, after SQL pointer use drains, unmaps each saved **base** view,
closes its mapping object, then releases node handle/DMS. Record every native
cleanup result. Do not use stock `_winShmPurge`, which scans unrelated nodes and
may pathname-delete. DeleteFlag=1 is unsupported in this slice; perform safe
local detach cleanup while reporting/latching unsupported deletion, without
unlinking storage. Review how core observes that result and assert bytes survive.
Main close must coordinate SHM detach before main lease teardown. Repeated close
returns the saved result; no retry on consumed handle numbers after uncertainty.

## 4. Staged RED contracts and implementation order

Each stage gets a separately pinned overlay/manifest and independent test gate.
Do not edit previously qualified artifacts just to broaden old tests.

**A: same-binding SQL admission.** Add exact URI main and owner xAccess contracts.
Tests call the actual registered VFS, both direct callback and SQLite URI entry.
Positive SQL-to-WAL traversal is initially blocked by explicit SHM stub, not by
xAccess/main flags. Require evidence of main0x141, same-owner access, WAL0x80006,
and SHM callback refusal. Wrong-owner/foreign path/flags/hot-journal paths fail
without touching stock callbacks. This removes setup blockers before SHM RED.

**B: real mapped-pointer resources.** Direct main-method tests require nonzero
mapped pointers with READONLY result, DMS shared retention and real map counters.
Start with refusing xShmMap, then implement node resources and 2-region mapping.
Test two connections, owner close while a retained pointer is leased to SQL,
partial acquisition rollback, alignment and size rejection, first-opener DMS
refusal without truncate, and foreign/stale main pointers. No SQL success claim.

**C: cross-process locks and cleanup.** A stock child probes each real lock range;
exercise conflict, shared coexistence, exclusive reader-slot lock, unlock and
last-close release. Fault-inject each mapping/lock/unlock/unmap/close operation;
assert poison/cleanup ledger and no stale-handle retry. All attempts must leave
original stock data readable. Verify default stock global tables/node list intact.

**D: held-stock SQL positive RED.** Independent stock child creates the synthetic
WAL, holds a transaction/readmark and answers lifecycle probes. Private URI SQL
must read committed rows absent from base-only DB, with real xAccess/main/WAL/
SHM/DMS/reader-lock counters, READONLY-map result and no heap fallback. Stock
reads before/during/after must agree. Private write attempts fail; no database,
WAL or SHM mutation attributable to private connection. Separate source state
checks from any legitimate fixture-holder activity. Test missing SHM, malformed
index, exhausted readmarks and external holder death as explicit failures or
safe retained-DMS behavior, not automatic migration/recovery.

Native gates require exact named test events, three repetitions each, no
fail/build-fail/skip and no printed-PASS substitute. Setup success alone is not
RED. Preserve official-source/module verification, old main/access/WAL gates,
default CGO strategy, complete stage backups and production capability refusal.

Completion of D qualifies only held-stock readonly existing-WAL SQL on the
tested local filesystem. First-opener initialization/recovery, broader readonly
startup, writable WAL, sidecar confidentiality, retirement and release packaging
remain separate work. Do not advertise this slice as full production WAL support.

## Appendix: readonly handle and local lock qualification

Independent review, before implementation. Official documentation establishes
that exclusive locking does not inherently require write access. However the
existing RO lease requests a **subset** of generic read rights; qualification
must exercise that exact handle, not a broader conveniently opened fixture.

### Official evidence and limits

- [LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex),
  documentation dated 2021-10-13, supported since Windows XP/Server 2003:
  a GENERIC_READ handle suffices, including for EXCLUSIVE requests. Separate
  opens in one process are distinct lock owners. Shared locks may overlap;
  exclusive locks cannot overlap existing locks, except that a same-handle
  shared request may overlap its exclusive lock, requiring two unlocks.
  Locks do not protect mapped-memory accesses. Use FAIL_IMMEDIATELY and inspect
  GetLastError, not asynchronous pending/wait assumptions.
- [File access mappings](https://learn.microsoft.com/en-us/windows/win32/fileio/file-security-and-access-rights),
  dated 2025-07-09: generic read includes FILE_READ_DATA, FILE_READ_ATTRIBUTES,
  FILE_READ_EA, STANDARD_RIGHTS_READ and SYNCHRONIZE. Current WAL RO acquisition
  uses only READ_DATA|READ_ATTRIBUTES|SYNCHRONIZE (0x100081). The generic-right
  documentation alone does not prove this reduced mask supports every API.
  Do not silently add write access if qualification fails. Test the exact mask;
  if needed, a separately reviewed full generic-read mask remains readonly,
  but is a different authority contract and requires requalification.
- [UnlockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-unlockfileex),
  dated 2024-02-22: unlock offset/length must match one acquired range. Adjacent
  independently acquired ranges cannot be released as one combined range.
  Track native acquisition ranges as well as SQLite bitmasks; reject invalid
  callback sequences before corrupting masks or unlocking another grant.
- [CreateFileMappingW](https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-createfilemappingw):
  PAGE_READONLY is compatible with generic-read access; PAGE_READWRITE needs
  read and write access. Existing low-rights SHM handles still need a real test.
- [MapViewOfFile](https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-mapviewoffile):
  use FILE_MAP_READ, not FILE_MAP_COPY. Copy-on-write would diverge from shared
  wal-index state. Independent mapping objects backed by the same local file
  provide coherent views; Windows byte locks are SQLite coordination, not
  enforcement against arbitrary mapped-memory writers.

### Fixed SQLite implementation consequences

W:126392 `_winShmLock` suppresses a repeated already-held shared lock using the
connection shared mask. Without that guard, repeated native shared acquisitions
could outlive a single logical unlock. Core is documented in this function as
not requesting an exclusive lock it already holds; treat such a forged callback
as invalid rather than introducing a recursive-lock extension. Also reject
same-connection shared/exclusive overlap instead of exploiting the Windows
same-handle exception. This preserves a single logical ownership model.

For local paths, stock per-connection handles let the kernel resolve sibling
conflicts. Its sibling-list suppression is **UNC-only**. Therefore separate
private and stock node registries do not remove local OS lock conflicts when
both reference the same qualified file. Private DMS has its own independently
opened handle and must conflict with the same-process stock DMS holder too.
Test same-process stock+private and two-process stock+private separately; neither
is a proxy for the other. Never reuse one handle for multiple logical connections
or apply the UNC shared-handle algorithm accidentally. Network filesystems remain
outside the qualified scope.

### Small standalone native preflight, before a large SHM implementation

Use one synthetic local file and handle-relative independent opens through the
actual RO acquisition mechanism. Assert every handle's granted rights are exactly
the reviewed mask, with no WRITE_DATA/APPEND_DATA/DELETE/WRITE_DAC/WRITE_OWNER.
No permission or ACL changes are needed.

1. On RO handle A, acquire exclusive immediate byte123/length1 and unlock it;
   also acquire/release shared DMS128. Record real Win32 return/error codes.
   Map one existing page readonly through the exact same rights profile. This
   proves the reduced-rights hypothesis, not just generic-read documentation.
2. A exclusive123 blocks independent B shared123 and B exclusive123 in the same
   process; after A unlock, B succeeds. A shared123 and B shared123 coexist;
   A unlock alone must not let independent C acquire exclusive until B unlocks.
3. Repeat the conflict/coexistence matrix with B in a child process. Require
   ERROR_LOCK_VIOLATION (33) for the expected immediate conflict on qualified
   local NTFS; log and fail unexpected errors rather than normalizing them BUSY.
4. At the bridge boundary, repeat a shared acquisition: native-call counter must
   stay unchanged. One logical unlock releases it. Repeated EX, shared-over-own-EX,
   invalid flags and unmatched combined unlock are rejected without native work.
5. Acquire two adjacent native ranges independently; attempted combined unlock
   fails and the child still observes both locks. Release their exact original
   ranges and prove both available. This test guards a masks-only implementation.
6. Hold DMS through stock in the same process and then a child; private exclusive
   DMS must conflict, private shared DMS must succeed. Release stock while private
   remains: a third independent opener still cannot take exclusive DMS. Release
   private last, then verify acquisition succeeds. No truncation in any test.
7. Close one independent connection handle and show another's locks remain.
   Record failures/cleanup outcomes; do not use delayed process-exit cleanup as
   proof that explicit unlock succeeded. Mapping resources and DMS remain owned
   until retained SQLite pointer use is drained as specified above.

These are native qualification contracts, not results already obtained. A failure
in the minimal rights/locking matrix blocks the SHM implementation from claiming
compatibility; it does not authorize broadening rights or weakening the contract.
