# Stage B: retained readonly SHM resources

Design and native RED scaffold. Production stays disabled. Stage B uses direct
private VFS methods with synthetic files, not successful SQL. Its execution
tickets simulate core pointer retention; stage D must wire actual SQL execution,
Rows, Stmt, connection teardown and cancellation before claiming SQL protection.
No previous admission/permit overlay is changed.

## State and ownership

A new host owns its permit owner and an owner-private node registry. Attachments
are bound to host, connection, live main-file generation and SHM identity.
Each attachment retains a connection-bound operation permit until core no longer
uses returned pointers and detach completes. Node mapping/DMS grants are explicit
host-owned resources, never a borrowed other-connection private resource.
Registry entries progress acquiring -> DMS-held -> mapped -> draining -> closed;
uncertain cleanup enters terminal retained state. Never share stock nodes.

Mapping records save mapping-object handle, actual view base, aligned file offset,
view length, returned base+shift, readonly rights and consume-once cleanup ledger.
Region size is 32768; use actual allocation granularity, checked arithmetic,
existing file size, extend=0. A second region proves alignment. Success returns
SQLITE_READONLY with nonzero pp; failure clears pp and returns IOERR_SHMMAP.
Never use READONLY_CANTINIT or heap fallback.

## Acquisition and publication

Reserve a registry slot under a short mutex, then release it. Under the existing
broker mutex, requalify lineage, exact name, registered SHM identity and native
metadata -> independently opened data handle. Rights must be exactly 0x100081.
Own handle+error immediately. Do not compare mutable SHM content as identity.
Create no files, truncate nothing, never DuplicateHandle as an independent lock
owner. A published pointer requires the entire registered resource transaction.

First acquire EXCLUSIVE immediate DMS at 128/1. Success means initialization
would be needed: unlock exactly and refuse without truncation. Only actual
ERROR_LOCK_VIOLATION allows shared DMS acquisition, retained through node teardown.
Other failures are errors, not BUSY evidence. Shared DMS is not header validity.
Create PAGE_READONLY mapping and FILE_MAP_READ view. Check closing/cancellation
before publication. Existing frozen-size mapping does not implement growth.

## Draining and lock order

Host closing rejects new execution, attachment and mapping. Existing execution
may finish; existing attachments may unlock/detach. Keep pointers until their
connection/core lifetime ends, not merely callback count zero. A timeout retains
resources and reports blocked shutdown, never force-unmaps.

Registry mutex never spans broker/native/old-owner calls. Node operations are
serialized separately. Wait via condition variables with state mutex released.
No callback reentry into host while holding its operation mutex is supported.
Old permit admit allows existing permits during ordinary closing, but borrow
rejects terminal or resource.closing. Thus never close a resource before unlocking
its ranges, and terminal cleanup uses its own grant ledger rather than ordinary
borrow. Release attachments before the final underlying owner.close.

Last attachment cleanup: unmap each actual base, close mapping object, release
DMS, close data handle. Consume each attempted action once and preserve errors.
Unmap uncertainty retains backing mapping/DMS resources; do not clear ledger and
reopen on ambiguous failure. Repeated shutdown returns saved result, not another
close on a potentially reused handle. Existing broker exclusion survives node
retirement. Unknown aliases and external sidecar replacement remain blockers.

## RED reachability and contracts

Six exact top-level tests, three repetitions each. Every fixture independently
qualifies real handles, registered identity, exact access mask, real readonly map
bytes and independent DMS conflict before calling actual private xShmMap. Each
starts with a positive map requirement; the RED sentinel must come from that
callback, never constructor/setup. Subsequent negative and lifecycle assertions
are intentionally not yet reached by the refusing implementation.

1. TestPrivateSHMReadonlyTwoRegionMapping
2. TestPrivateSHMConnectionPointerLifetime
3. TestPrivateSHMOwnerCloseDrainsPointers
4. TestPrivateSHMFirstOpenerRefusesWithoutMutation
5. TestPrivateSHMAcquisitionRollback
6. TestPrivateSHMForeignAndRetiredAttachment

Fault matrix expansion before GREEN qualification must include handle+error,
map-object+error, cancellation before publication, unmap/close uncertainty and
close/publication races. A simple injected post-map rollback is not this whole
matrix. All core lifetime tests here are simulations, not SQL success evidence.

DMS constrains cooperating SQLite participants only; share-write permits raw
truncation. Held protocol-holder fixtures do not qualify arbitrary external
truncate/rebuild, and no ACL change or blanket stock-writer prohibition is added.
On unmap uncertainty retain the node connection pin/long permit as well as
backing resources, preventing the old owner.close tail from closing the broker.
Cleanup authority is dedicated and may run after terminal state; never rely on
ordinary p.borrow admission after poison. Retained terminal is observable and
requires no destructive retry.

GREEN lock discipline: no node-operation mutex is held together with broker,
Root or old permit owner mutex. Node uses an in-flight reservation counter across
those domains and reacquires its mutex to validate generation/publish. Shutdown
marks closing then waits with the mutex released. Native node map/cleanup is
serialized by reservations, not cross-domain nested locks. Retained terminal
explicitly forbids underlying owner.close/broker.close and pin release.

The RED close dispatcher calls a no-resource detach stub only. Before accepting
mapping, implement per-file close/map serialization, saved close result,
generation/role validation, and panic containment; ordinary WithRole cannot be
the terminal cleanup path because its lease Borrow can reject. Do not hold f.mu
while recursively calling the old MainReadClose which takes that same mutex.
Authority returning pointer+error retains responsibility in its cleanup ledger;
the bridge clears pp and never treats this as a successful mapping.

The pointer-drain contract starts file detach asynchronously, observes its entry,
proves it cannot finish while the simulated core ticket still retains pp, then
releases the ticket before waiting for detach and host close. Tickets must release
idempotently. It is not sufficient to wait only for host close.

Before GREEN, uncertain retained fault cases need an isolated normal-exiting
child harness: verify retained pins/ledger and blocked teardown there, then exit
normally for OS resource cleanup. Parent must validate protocol and successful
exit. Timeout/kill is failure, not evidence. Do not put such a deliberately
retained owner through the regular fixture's unconditional owner cleanup, nor
fake successful cleanup with a hook. Current post-map rollback case exercises
only a determinate cleanup path, not this uncertainty matrix.

## GREEN candidate implementation (native validation pending)

The host now binds attachment authority at actual private main xOpen via
AttachSHM, before its successful return. A key includes the bridge's actual
file generation and is fixed to one connection. Test tickets now take this key
and connection: detach marks that attachment draining under the admission mutex
before waiting; fresh tickets for it are refused even without host shutdown.
Host-wide ticket counts conservatively delay other detaches. Tickets remain
simulated core use, not SQL/Rows/Stmt integration.

Native work uses the busy reservation with no host mutex held. Cached pointer
grants and new-view publication both check closing and commit under that same
mutex. Shutdown has one finalization executor, waits for every attachment and
ticket, then closes the underlying permit owner outside the host mutex and
caches its result. Terminal retained state never reaches that finalizer.

Live SHM method/close dispatch is serialized by the actual main record mutex.
Close records a saved result and consumes the main lease only after successful
node detach, rather than recursively entering the old close callback. Detach
panic is contained and leaves the owner pin retained. Failed main attach clears
outFlags, invokes idempotent detach (absent state owns nothing), and consumes the
main lease only when safe; panic or uncertain cleanup poisons the binding.

Retained records include unpublished views, attempted base/mapping/data/metadata
handles, and exclusive/shared DMS unlock state. An attempted identifier is audit
state only, never permission to retry a potentially reused number. Normal-exit
child cases cover unmap failure, mapping/data close uncertainty, close-hook panic,
unlock failure, detach panic, unpublished-view cleanup failure, metadata cleanup
failure and first-opener exclusive-unlock uncertainty. Children check already
recorded test errors before emitting a witness and exiting zero. Parent requires
that witness and a successful process exit; timeout/kill cannot pass.

Determinate faults exercise real open/mapping/view results returned with errors,
once-only rollback, and successful reacquisition. A native before-publication
barrier proves **host-close publication rollback**, not generic context
cancellation: mapRegion currently has no ctx argument. Stage D must separately
prove real SQL cancellation. Foreign-connection tests use the actual live key
with its original-connection positive control. Retirement is separately tested;
the same-address/new-generation test is a host-token simulation, not evidence
that SQLite's allocator reused an actual file allocation at that address.

Current host.close is a synchronous drain primitive with no context argument.
Bounded waits in tests are harness controls only; a production close(ctx) or
SQL cancellation API is not implemented. Waiting never authorizes forced unmap.
The retained child suite additionally verifies attach-panic cleanup: it records
poison, but when detach succeeds there must be no leaked attachment pin, and
owner.close returns the saved poison instead of hanging. Ordinary attach errors
both before and after attachment registration clear outFlags/methods after safe
cleanup and leave the original authority usable.

The trusted adapter's Revoke must propagate terminal state to both SHM host and
permit owner. Detach hook panic is contained by the bridge; the direct host API
alone does not promise panic containment for that trusted hook. The retained
children verify host.close returns the expected terminal error without changing
underlying owner closing/closed state, then independently prove that an explicit
raw owner.close remains blocked by the retained pin.
