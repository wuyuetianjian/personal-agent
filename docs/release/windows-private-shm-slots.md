# Stage C: private SHM slot locks and barrier

Experimental RED contracts only. Stage B's private readonly mapping and retained
pointer lifetime are prerequisites, not SQL qualification. Production and tool
read authority stay disabled. No changes to the qualified B overlay or permit.

## Fixed protocol and scope

Use actual local-file Windows slot bytes 120..127, independent from DMS128/1.
Each main-generation/connection attachment owns a fresh qualified RO lock handle
(0x100081). The mapping node and DMS grant remain shared by its host; slot locks
never borrow that node handle or another connection's handle. Revalidate registered
SHM identity, canonical name, lineage and access mask before publication. Do not
use stock global nodes, DuplicateHandle or global SQLite syscall replacement.

Accept SHARED LOCK=6 / UNLOCK=5, EXCLUSIVE LOCK=10 / UNLOCK=9. Both SHARED LOCK and SHARED UNLOCK require a single slot; EXCLUSIVE operations
may span legal multi-byte ranges. Pinned
core uses recovery [1,3) or [2,3) and checkpoint/restart [4,8), each with its original
unlock range. Do not reject all count>1. Fully unheld unlock is OK with no native
call. Repeated shared lock is OK without an additional native layer. Repeated
exclusive lock reports BUSY (stock invokes the kernel and normally gets conflict;
private bookkeeping may identify the same conflict without claiming success).

Track exact (offset,count,mode) native acquisitions and state, not only SQLite
masks. Never merge two original locks to unlock a combined range; never unlock
and reacquire a larger/smaller range to simulate successful partial unlock.
Unknown partial/merged overlap fails without changing the actual holdings.
Only ERROR_LOCK_VIOLATION becomes BUSY. Unexpected native errors and uncertain
unlock/close become terminal; preserve attempted-action ledger, necessary backing
and owner pins without retrying consumed identifiers. Child process tests must
exit normally after proving retained terminal state, never treat kill as success.

## Lifetimes and ordering

A slot method is admitted only for its live attachment/generation. Per-file
serialization and host reservations must prevent map/lock/detach races. Do not
hold short registry mutexes across native operations. Closing rejects new work,
waits for simulated core tickets and in-flight methods, releases each exact slot
range, closes that attachment's independent handle, then detaches B mapping.
Only the last safely detached attachment retires mapping/DMS. Another connection's
locks and pointers survive the first connection's close. Cleanup uncertainty
must not drop the B owner pin or invoke its generic close tail.

The adapter's barrier uses real sequentially consistent synchronization, not an
empty callback or a counter alone. Since xShmBarrier is void, latch errors and
refuse later operations. The new bridge validates live main ownership first.
A visibility fixture uses a cooperating process writing synthetic data under a
native slot lock, then the readonly private view reads it after its actual barrier.
IPC also provides ordering, so this is interoperability evidence, not a formal
proof of the barrier's memory model. Source review and native execution are
separate evidence requirements.

SHM_RO bit2 causes the pinned core to skip the reader-mark EX update branch.
Main/WAL readonly bit1 alone does not imply SHM readonly. Stage C qualifies EX
protocol directly; it does not assert held-index readonly SQL must take EX.
Stage D must verify actual pager sequences and no heap-index fallback separately.

## Six native contracts

Each test first verifies B mapping and native independent-RO-handle conflict,
then requires a real private xShmLock success. RED is only a callback-counted
unimplemented failure, never fixture setup. Downstream assertions initially
remain unreachable and are not counted as validated behavior.

- TestPrivateSHMSlotSharedExclusiveProtocol
- TestPrivateSHMSlotIndependentConnections
- TestPrivateSHMSlotCrossProcessConflict
- TestPrivateSHMSlotExactRangeLedger
- TestPrivateSHMBarrierVisibility
- TestPrivateSHMSlotDetachAndFaultRetention

Three repetitions, no added top-level or subtest nodes, strict native event gate.
This stage does not create/grow/recover SHM, change formats/journal mode, support
external sidecar replacement, guarantee confidentiality of unknown aliases, or
provide general SQL cancellation or production SQL/Rows/Stmt lifecycle wrapping.

### Exact detach ordering across the unchanged B layer

The C detach coordinator must first mark the matching B attachment draining under
B.mu (validate key/connection), then release that mutex. C marks its own grant
closing and waits for C in-flight native operations with C.mu released. It then
waits for B tickets and B native reservation to drain, setting B.busy only for
C's own exact-unlock/close transaction; no B mutex spans native calls. Once safe
C cleanup finishes, clear B.busy/broadcast before invoking ordinary B.detach.
Never recursively call B.detach while holding B.mu or owning B.busy. The still
live B attachment prevents another connection from retiring the shared node.

Slot admission validates B draining state then reserves the C grant under C.mu;
C closing rejects the gap race, while an already reserved operation is counted
and drained. New B map/ticket admission already rejects a draining attachment.
Unknown slot cleanup leaves the B attachment/pin unretired and propagates terminal
state; never continue to B.detach on that failure. The lifetime contract must
prove the native reader slot remains held while the core ticket is alive, not
merely that close eventually waits somewhere after releasing the lock.

SQLite's VFS contract requires unlock to match its acquisition SHARED/EXCLUSIVE
mode as well as the exact original range. The Win32 API's lack of a mode argument
and generated stock code clearing both masks do not relax the public contract.
Reject cross-mode requests without native mutation; prove the original grant
still conflicts, then release it normally using its original mode. Completely
unheld unlock remains a no-op only after validating all parameters.

Pinned source identity:
`bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc`.
The original os_win.c asserts `n==1 || (flags & SQLITE_SHM_EXCLUSIVE)` before both
LOCK/UNLOCK branches; generated Go omitting assertions is not broader permission.

- https://sqlite.org/c3ref/c_shm_exclusive.html
- https://sqlite.org/src/raw/src/os_win.c?ci=bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc
- https://sqlite.org/src/raw/src/sqlite.h.in?ci=bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc

Native contention returns busy=true,nil to the bridge, producing SQLite BUSY
without poisoning or converting through Go-error Borrow handling to generic IOERR.

## Verified direct-method native qualification

Commit f54babe / run 43256116998: six contracts × three passed, including
eight normal-exit retained children per round and the success-callback/release-
failure control. This is direct-method qualification, not SQL integration.

C operations reserve the existing B.busy state after validating the actual
attachment under B.mu. Thus the B reservation counts every C in-flight operation;
no second C lock is held over B/broker/native work. A lock admitted before draining
may finish, but detach waits for its reservation. Detach marks B draining before
the test hook, waits for tickets and busy, performs C cleanup under its own B.busy
reservation, then clears busy before the adapter invokes B.detach. Cleanup failure
returns without invoking B.detach and propagates terminal state to both hosts.

Every attachment lazily opens an independent qualified readonly handle via the
existing metadata-to-data transaction (without DMS acquisition or mappings).
The exact-range ledger records attempted uncertain operations and never retries
those identifiers. Legal no-op unlock and repeated shared acquire do not invoke
native APIs. Duplicate/overlapping exclusive acquisition reports BUSY. Unexpected
native errors/panics retain the grant and mapping lifetime; they are not BUSY.

Barrier works on a mapped attachment before its first slot lease exists. It uses
an actual sequentially consistent atomic RMW, separate from counters. The bridge
captures a failure/panic cause inside Borrow, then calls Revoke once after Borrow
and file mutex unwind. The adapter propagates terminal state to both hosts.

The added normal-exit child matrix covers unlock/close error and panic, unexpected
lock error/panic, and barrier error/panic. It verifies saved close results, no
repeated native attempt, retained mapping bytes/DMS, explicit host terminal result,
and blocked underlying owner close. For close uncertainty the exact slots already
unlocked must remain free; other uncertainty cases must preserve the held slot.
Barrier children verify Revoke observes no outstanding Borrow refs/B reservation
and the original fault is still recoverable with errors.Is. These tests remain
synthetic direct-method contracts, not real SQL or production qualification.
