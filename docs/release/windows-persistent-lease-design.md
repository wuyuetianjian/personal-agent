# Persistent connection leases: design and test-first boundary

Status: **implementation candidate, native qualification pending**. The private SQL spike at `0cf123d` reached real
private xOpen three times and refused with SQLITE_CANTOPEN. This next stage is
only independently opened native file leases, not accepting SQLite callbacks.
Both `nativeprobe_sqlite_lease` and `nativeprobe_sqlite_identity` are required;
production remains disconnected. No journal-mode, permissions or ACL changes.

## Authority and state

An owner independently constructs the existing broker for one bound directory
and basename and qualifies all main/WAL/SHM objects. This keeps its protection
identities alive until host close; ending a connection or the last resource lease
must never release those exclusions. Owners have active, terminal and closed
states. Terminal forbids new admission but retains ownership until close. Close
is idempotent with a retained error, not a retry of an uncertain native close.

Each owner creates private connection objects. Connections are live or closed,
are bound to exactly one owner, and own a registry of resource leases. A resource
moves acquiring -> qualified -> registered -> published -> closing -> consumed.
No resource pointer is published before its independent handle has been qualified
and registered. Failed registration closes the candidate exactly once; native
open returning both a handle and an error still transfers cleanup ownership.
Any uncertain close permanently latches the owner terminal error. A failing close
cannot remove the broker's retained identity exclusion or restore admission.

The minimum purposes are main, WAL, SHM mapping/DMS and SHM connection-lock. They
select fixed broker roles, not arbitrary filenames. Every acquisition performs a
fresh relative NT open and metadata-to-IO identity qualification. DuplicateHandle
is not used for a connection resource. Separate SHM lock purposes require separate
FILE_OBJECT instances: native cursor-sharing positive controls distinguish a
DuplicateHandle from independent opens, not merely different handle numbers.

## Registry critical section and callback lifetime

An atomic closing transition precedes waiting for the owner write lock. New
admission then refuses (or waits for the write lock and refuses afterward).
One owner RWMutex guards admission: borrow and workspace read hold its read lock;
acquire, resource/connection/owner close hold its write lock. A pending writer
therefore prevents new readers, and close waits for callbacks already admitted.
Within the owner lock, the existing broker mutex protects root/identity checks.
Lock order is owner gate -> broker -> caller workspace Root. No callback may
reenter owner mutation, retain a raw handle or call close while borrowed.
Callback panic must release admission via defer, without swallowing the panic.
Callbacks are trusted internal bridge code; this is not a public tool API.

`owner.newConnection(ctx)` issues a private connection reference.
`connection.acquire(ctx, purpose)` returns an opaque lease after registration.
`connection.borrow(ctx, lease, fn)` validates owner, connection, registry membership
and live state before passing its handle temporarily to trusted code.
`lease.close()` drains admitted borrows before consuming the handle.
`connection.close()` closes its resources, not the owning broker.
`owner.readWorkspace(...)` holds admission through the actual Root.read result
and its cleanup, and does not return bytes after errors.
`owner.close()` drains admitted operations, closes resources and then the broker.
No fresh operation can reuse a consumed resource or cross an owner/connection.

The test-only after-closing hook observes the owner transition before drain; it
is invoked once, never again on repeated close.
The test-only after-read hook runs only after a successful real Root.read and
before returning those bytes, with admission held. It demonstrates owner-close
drain, not complete storage mutation or SQLite transaction semantics.

## Native RED contracts

- Independent FILE_OBJECTs, with a real duplicate cursor-sharing positive control.
- Registration failure publishes no lease and reclaims its candidate.
- Borrow blocks resource close until callback exit; late borrow refuses.
- Native handle+error is reclaimed once and the original error is preserved.
- Close error consumes once, remains terminal, and forbids later operations.
- Cross-owner and cross-connection borrow fails without invoking its callback.
- Last connection/resource close still protects every stored identity while
  neighboring workspace data remains readable until host close.
- Real workspace read holds admission through delivery and drains before host
  close; after close all reads refuse, without a fallback to raw Root.Read.

## Remaining blockers and stock integration

These leases do not solve an external process creating or replacing SHM outside
the registry. A reduced-rights duplicate still references the same FILE_OBJECT
and may retain sharing constraints; it is not a solution for stock sidecar
removal/recreation. Current host-long no-share-delete identity guards remain a
limited experiment, not a qualified complete lifecycle policy.

The eventual module bridge may temporarily borrow a lease handle into the REAL
pinned stock TwinFile constructor. It must never pass the three broker handles
or invent a compatible-looking winFile. Private `_winOpenSharedMemory` must obtain
separate mapping/DMS and per-connection lock leases, with owner-isolated SHM nodes.
Mapping handles/views need their own registry and drain before accepting xShmMap.
Private close/unmap paths must consume leases once and propagate terminal errors
outside SQLite destructors. No accepting xOpen, SHM, fake lock or mmap is added
by this lease stage. External SHM creation, live sidecar retirement and namespace
confidentiality/integrity remain explicit blockers before production integration.

### Initial RED reachability

Before the owner-construction refusal stub, each fixture independently opens the
existing broker, qualifies all three roles, reads neighbor bytes, refuses a
protected identity and proves DuplicateHandle cursor sharing on real handles.
It then emits LEASE_FIXTURE_NATIVE_CONTROLS_OK. A failure at the following explicit
LEASE_OWNER_EXPECTED_RED marker demonstrates missing owner construction only;
it is not evidence that acquisition, independent FILE_OBJECTs, drain or cleanup
assertions ran. Implementation native runs must execute those positive bodies.
Close-drain tests synchronize a close-transition hook before the wait assertion,
and completion receives have deadlines; scheduler delay alone is not drain proof.

### Bridge reentrancy boundary

Do not wrap an entire stock xShmMap callback in the current borrow read lock:
xShmMap can lazily call _winOpenSharedMemory, whose lease acquire needs the owner
write lock. That is a lock-upgrade deadlock, not a supported reentrant operation.
This lease stage only qualifies non-reentrant borrowed fragments. Future stock
integration must separate an operation-admission/drain token from the short
registry mutex, or acquire all required resources in a protected phase before
borrowing handles for fragments that cannot acquire again. The current RWMutex
is not a drop-in wrapper around arbitrary SQLite callbacks.

### Candidate after owner-only RED

At `941cf47`, Actions `43214280097` reached the explicitly unimplemented owner
constructor after 36 successful leaf native prerequisite controls. All 14 required
nodes failed three rounds with no setup/build/skip/panic/timeout failure. This is
owner-only RED, not execution evidence for the downstream lifecycle assertions.

The candidate now creates an owner-held broker and opaque connection registries.
Every resource uses fresh metadata and data opens and compares current full
identity, regular type, exact name and lineage before publishing. The resource
close hook covers data candidates/live resources only; metadata always uses native
CloseHandle, with any error retained in owner terminal state. Failed opens claim
returned handles, registration rollback consumes candidates, and borrow unlocks
via defer even on a propagating callback panic. Owner and connection closing
transitions precede lock acquisition; terminal/closing owners reject new work.
The test now invokes owner.close directly twice after initial completed shutdown,
checking retained terminal error and no extra resource close, independently of the
fixture single-flight helper. Unknown cleanup results never trigger numeric handle
retry. Native lifecycle GREEN remains pending for the implementation's exact SHA.
