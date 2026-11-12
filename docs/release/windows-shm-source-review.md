# Pinned Windows SHM implementation review

Read-only source review, 2026-11-12. This document adds no implementation or
qualification. Main-file methods and metadata GREEN do not qualify WAL SQL.
The complete private WAL SQL positive contract remains RED at this review.
Preserve database/WAL bytes, WAL mode, stock external readers, and production
capability defaults. A held-stock read-only stage is narrower than full WAL.

## Reproducible source and layout

Reviewed `modernc.org/sqlite@v1.58.0`, module checksum
`h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=`, go.mod checksum
`h1:rsD2CckafgObKC4DhBlGBf+RiHxkc3hINGt1Xw32tVY=`.
All line numbers below are generated Go source, not embedded original C line
numbers. W = `lib/sqlite_windows.go`; G =
`lib/sqlite_g_0000000000060000.go`; C = `lib/sqlite.go`.

| File | SHA256 |
|---|---|
| W | `414fac0347bda229e81decf8b765a276dc59f382cde33542f7246862e7022d98` |
| G | `8416138b3519a8ad4a2e4a76928c34781333915f09ef5d5444eb75d5cc4eabe7` |
| C | `8210957d4836c361efb18808fb6fd4dc5f2a7ca362fd83881d6018e9913ee4d6` |

W selects Windows amd64/arm64. G also includes Windows 386, but this review's
allocation sizes and offsets are **64-bit only**. Reviewing only W and C misses
DMS, barrier, unmap, node-list and helper implementations in G.

- G:77786 `TwinShm`: node pointer, shared/exclusive uint16 masks, connection
  locking HANDLE, readonly flag, next connection. Size 40 in W's allocations.
- G:77796 `TwinShmNode`: mutex, filename, shared mapping/DMS HANDLE,
  shared-lock-handle flag, unlocked/readonly flags, region size/count, region
  array, last errno, connection list, next node. Size 80 in W's allocations.
- G:72353 `TShmRegion`: mapping HANDLE plus base view pointer; size 16.
- G:114784 `_winShmNodeList` is process-global. G:113843 `_winBigLock` is
  process-global; W:6021 initializes it with `SQLITE_MUTEX_STATIC_VFS1=11`.
- G:114821 `_winSysInfo` provides allocation granularity, initialized by
  GetSystemInfo at W:6015-6016. It is geometry, not owner authority.

## Actual algorithm closure

| Function | Location | Nontrivial dependencies / authority |
|---|---|---|
| `_winOpenSharedMemory` | W:126134 | allocators, filename formatting/comparison, `_winIsUNCPath`, `_winHandleOpen`, global-list mutex, per-node SQLite mutex, `_winCloseSharedMemory` rollback |
| `_winLockSharedMemory` | G:114506 | shared node handle, `_winHandleLockTimeout`, `_winHandleTruncate`, `_winUnlockFile`; DMS protocol |
| `_winShmMap` | W:126539 | lazy open, node mutex, DMS, size/truncate, realloc, CreateFileMappingW/MapViewOfFile, geometry, logging |
| `_winShmLock` | W:126392 | node/connection masks, local/UNC handle choice, timeout lock/unlock helpers |
| `_winShmBarrier` | G:114762 | `_winShmEnterMutex`/`LeaveMutex` at G:114769/114773, hence global `_winBigLock` |
| `_winShmUnmap` | G:114792 | directly calls `_winCloseSharedMemory` |
| `_winCloseSharedMemory` | W:124921 | closes connection handle, removes connection under node/list mutexes, calls purge, frees connection |
| `_winShmPurge` | W:126657 | sweeps **all** empty global nodes; unmaps views, closes mapping and node handles, optional pathname `_winDelete`, frees arrays/nodes/mutexes |
| `_winHandleOpen` | W:125480 | UTF conversion, `_winIsDir`, CreateFileW, readonly fallback |
| `_winHandleLockTimeout` | W:125443 | LockFileEx, immediate failure; `nMs` unused in this generated build |
| `_winHandleUnlock` | G:114344 | `_winUnlockFile` at W:126804, UnlockFileEx |
| `_winHandleSize` | G:114303 | GetFileSize, GetLastError |
| `_winHandleTruncate` | G:114326 | `_winHandleSeek` at G:114270, SetEndOfFile |
| `_winHandleClose` | W:125425 | CloseHandle |

`_winOpenSharedMemory` builds the main pathname plus `-shm`, matches existing
nodes with `_sqlite3StrICmp`, and contains a TODO for better file-ID matching.
It allocates/publishes a node with one shared mapping/DMS open, and links each
connection. For local paths it independently opens an additional SHM handle
for each connection's locks. UNC instead selects the shared node handle and
uses sibling masks under the node mutex to emulate in-process ownership.
A duplicated handle is not established as equivalent to these independent opens.
Private local-only implementation need not implement UNC, but must reject it
rather than silently take the local algorithm.

The stock pathname helper checks directory attributes, then opens with
GENERIC_READ plus GENERIC_WRITE unless readonly, FILE_SHARE_READ|FILE_SHARE_WRITE
(no SHARE_DELETE), OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL and no OVERLAPPED flag.
RW open failure retries readonly. Neither pre-open pathname checks nor
case-insensitive name matching qualifies owner-bound identity. Replace this
acquisition path with existing handle-relative leases; do not globally replace
`_aSyscall` or reuse stock node membership.

### Indirect native call inventory

G:91946 `_aSyscall` has 81 entries. Relevant active indexes are:
3 CloseHandle; 5 CreateFileW; 7 CreateFileMappingW; 23 GetFileSize;
26 GetLastError; 48 LockFileEx; 49 MapViewOfFile; 53 SetEndOfFile;
54 SetFilePointer; 58 UnlockFileEx; 59 UnmapViewOfFile.
W's dead platform branches also reference 4 CreateFileA, 6 CreateFileMappingA
elsewhere, 47 LockFile and 57 UnlockFile; distinguish unreachable branches from
active dispatch. Acquisition must not retain the pathname route merely because
its final handle operations are familiar. Logging helpers have additional
formatting/system-call dependencies; either audit their closure or replace
private diagnostics, without granting them authority to reopen objects.

Stock `_aSyscall[].FpCurrent` is mutable through VFS system-call controls.
Using it directly is a deliberate shared-dispatch dependency, not private
isolation. Private SHM native operations must use the reviewed private dispatch
policy or expressly test the retained shared behavior. Keep original default
method table and globals unchanged.

## Exact byte-range protocol and DMS recovery

C:4278-4286: SHM_EXCLUSIVE=8, LOCK=2, NLOCK=8, SHARED=4, UNLOCK=1.
Valid combinations are 6 shared-lock, 10 exclusive-lock, 5 shared-unlock,
9 exclusive-unlock. `_winShmLock` constructs the uint16 mask
`((1 << (ofst+n)) - (1 << ofst))`.

- OS lock base `(22+8)*4 = 120`: SHM slots 0..7 use bytes 120..127.
- WRITE slot 0, CKPT slot 1, RECOVER slot 2, reader slots 3..7.
  The source's blocking-order comment specifies recovery, checkpoint, write,
  then readers, not increasing numerical order.
- DMS is byte `(22+8)*4+8 = 128`, length 1, on the node shared handle.
- LockFileEx flags: FAIL_IMMEDIATELY=1, EXCLUSIVE_LOCK=2. Zeroed 32-byte
  OVERLAPPED, low offset specified, high offset/length zero. The actual pinned
  timeout helper never waits/retries according to `nMs`; any failed native call
  becomes SQLITE_BUSY=5. A stricter private error classifier is a reviewed
  behavioral difference and must distinguish contention from native failure.
- UnlockFileEx reserved=0, same offset/range. `_winHandleUnlock` maps failure
  to SQLITE_IOERR_UNLOCK=2058; masks change only after successful native work.

`_winLockSharedMemory` first attempts an immediate exclusive DMS lock:

1. Success means first opener under the SQLite protocol. A writable node
   truncates SHM to **zero**. A readonly node returns
   SQLITE_READONLY_CANTINIT=1288 instead. It releases exclusive DMS afterward.
2. SQLITE_BUSY from that attempt is normalized to OK, because another opener
   holds DMS. Other errors propagate.
3. If still OK, acquire shared DMS and clear `isUnlocked` only on success.
   Shared DMS remains until node handle closure, not merely until xShmMap exits.

The stock exclusive-unlock return is ignored. A private safety implementation
must track unsuccessful/unknown unlock, poison the owner and prevent subsequent
use rather than continue as if the lock transition succeeded.

A held stock reader normally holds shared DMS and prevents step 1's truncate.
This is useful for a **narrow positive contract**, not a proof that the node is
writable, the wal-index is valid, or the external reader will remain forever.
The private node must acquire and retain its own shared DMS before exposing
mappings. If exclusive DMS was obtained, that narrow stage may release it and
return a terminal IOERR without truncating. It may likewise reject creation,
missing/undersized regions, recovery/writes and deletion. These are explicit
unsupported branches of the experiment, not full WAL success or production
behavior changes. Exact unlock/release failures still need cleanup accounting.

**Do not use READONLY_CANTINIT as the terminal refusal code.**
W:119225 `_walIndexPageRealloc` marks `WAL_SHM_RDONLY=2` for all READONLY-family
results; plain READONLY=8 becomes OK. G:113005 `_walIndexReadHdr` specifically
handles CANTINIT by setting `bShmUnreliable` and `WAL_HEAPMEMORY_MODE`, then may
rebuild the index from WAL in heap memory. This is SQLite's real supported
fallback, but must not accidentally satisfy a test claiming private shared
mapping. With a mapped readonly index and invalid header, the same function
tries shared WRITE lock and returns READONLY_RECOVERY=264 on success (or lock
failure). Use a documented IOERR-family refusal for unimplemented branches;
verify no SQL success, no heap fallback and no bytes changed.

There is a distinct core-generated CANTINIT path: W:120259-120266 returns it
when `_walTryBeginRead` has no suitable existing readmark (`mxI==0`, unless rc
is BUSY, which becomes WAL_RETRY). This does not pass back through
`_walIndexReadHdr`'s map-result handler. The Windows amd64/arm64 implementation
of `_walBeginReadTransaction` is in `lib/sqlite_g_000000000003fea8.go:2517-2594`:
it retries only -1, then returns rc. C:38076 and `_pagerBeginReadTransaction` in
`lib/sqlite_g_0000000000063fff.go:25-46` propagate the failure (pager resets its
cache), without setting `bShmUnreliable` or heap mode there. These additional
shared generated files matter when following the full core call chain.

This distinction is not a blanket no-heap proof: W:107658-107663 can independently
initialize heap mode from `bNoShm`. Stage-D positive evidence must establish real
shared-index use; a no-suitable-readmark negative must fail SQL, not silently
pass by an alternate heap route. Do not modify core to manufacture that evidence.

Full WAL support must implement the first-opener truncate/rebuild route,
growth, writable maps, locks and retirement under qualified leases. Omitting
these routes is acceptable only as a labelled development stage.

## Mapping size, alignment and lifetime

W:126539 calculates required bytes `(iRegion+1)*szRegion`, queries current size,
and grows via truncate only when `isWrite != 0`. Otherwise an absent region
returns NULL with OK, or READONLY for a readonly node. This NULL response can
lead core to recovery; it is not a terminal refusal by itself.

Core W:119225 computes the actual region size from HASHTABLE_NPAGE=4096
(C:1321): `2*(4096*2)+4096*4 = 32768`. Generated exported macro placeholders
`WALINDEX_PGSZ=0` and `WALINDEX_HDR_SIZE=0` must **not** be used as real sizes.

Each new region gets a CreateFileMappingW object (unnamed, no security attrs)
with maximum size nByte, high DWORD zero. Writable protection PAGE_READWRITE=4,
view access SECTION_MAP_WRITE|SECTION_MAP_READ=6. Readonly protection
PAGE_READONLY=2, view access SECTION_MAP_READ=4.
For offset `region*szRegion`, shift is offset modulo allocation granularity;
MapViewOfFile maps offset-minus-shift with length szRegion-plus-shift.
Return pointer is saved **base + shift**. UnmapViewOfFile requires saved base,
not the adjusted returned pointer. On common 64-KiB geometry the second 32-KiB
region therefore maps from offset zero and returns base+32768.

Stock stores mapping object and base view in the node array, retaining all
successful regions until the last connection unmaps. Failure on a later region
closes its transient mapping object while preserving earlier entries. Both that
close and normal purge ignore native cleanup results in stock. Private cleanup
must record every view/object independently, consume once, and report/latch
failures. Never release a view at callback exit: SQLite retains and directly
accesses these pointers outside callback admission. Owner shutdown must drain
connections/SQLite pointer users before releasing views. Callback permits alone
are insufficient. Barrier needs real synchronization, not a no-op.

Bounds must reject negative region, nonpositive/mismatched size, multiplication
and allocation overflow, zero geometry and ranges exceeding the reviewed native
widths. Stock uses high size/offset DWORD zero and one loop multiplies region
and size as int32 before widening. Do not pretend arbitrary >4-GiB SHM support.

## Smallest owner-bound implementation and test closure

Implement one private node per qualified owner/main/SHM identity and generation,
not per filename. Bind actual TwinFile/TwinShm/TwinShmNode allocations through
side tables; keep node membership, masks, mutex/barrier and resource ledger
separate from stock. Acquire node DMS/mapping and independent connection-lock
leases lazily with operation admission that permits acquisition (no RWMutex
read-to-write upgrade). Validate every resource belongs to the same live owner.
Publish node/connection state only after all required acquisitions succeed;
otherwise roll back precisely the acquired resources.

The minimum useful held-stock SQL stage consists of: safe existing SHM opens,
DMS busy-to-shared transition, readonly region mapping, the full eight-slot
shared/exclusive/unlock protocol needed by readonly SQLite, private barrier,
and last-connection unmap/close. Do not conflate main/WAL readonly with SHM
readonly: W:107665-107668 sets `WAL_RDONLY=1` from WAL open outflags, while
W:119256-119261 sets `WAL_SHM_RDONLY=2` from a readonly map. The reader-slot EX
update branch at W:120233-120253 runs only when `WAL_SHM_RDONLY` is **clear**.
A main-readonly connection with writable SHM can use it; the actual readonly-map
path cannot. Full EX-slot support is a separate protocol qualification goal,
not a required event for held-readmark readonly SQL.
Refuse first-opener initialization/growth/recovery/deletion explicitly until
implemented. Do not replace mapping with a byte slice, snapshot, alternate DB,
or journal-mode change. Preserve real external lock offsets and shared bytes.

Required native evidence before declaring that narrow stage GREEN:

- Actual stock process commits and holds WAL; private SQL reads committed rows
  through real mapped SHM; stock reads before/after still work and all fixture
  bytes/mode remain as required. Confirm mappings used, not heap fallback.
- Cross-process conflicting/shared locks on bytes 120..128, separate private
  connections, separate owners and ordinary stock connections in same process.
- First-opener DMS refusal, absent/truncated SHM, bad index header, attempted
  growth and native errors all fail without accidental fallback or mutation.
- Two regions exercise shifted base pointers; one connection closing leaves
  another's mappings valid; last close releases each view/object/handle once.
- Partial CreateFileMapping/MapView failures, unlock/unmap/close failure,
  owner shutdown versus retained SQLite pointers, stale generation and wrong
  owner. No forged mask update or reuse of consumed handle numbers.
- Source-manifest/extraction checks include G; default stock method/global
  tables remain unchanged. Production capabilities remain disabled.

This source review does not resolve sidecar confidentiality/admission or final
no-share-delete retirement. They remain separate release blockers even after
SQL succeeds in the held-stock fixture.
