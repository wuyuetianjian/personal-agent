# Operation permits and short registry locks

Status: experimental implementation awaiting native GREEN validation. The native
owner-only RED was confirmed at 955d97b (run 43218296007): 8 nodes × 3
failed at the designated constructor sentinel after prerequisite controls.
Independent of the
qualified transitional lease implementation, which remains unchanged. Requires
`nativeprobe_sqlite_identity,nativeprobe_sqlite_lease,nativeprobe_sqlite_permit`.
No SQLite callback accepts IO, no production capability is enabled.

## Contract

A new owner owns its own existing-object broker and retains main/WAL/SHM identity
protection throughout host lifetime. It does not borrow or export the previous
lease owner's handles. Connection-private resources are acquired with independent
relative NT opens and retain their purpose (main, WAL, SHM mapping or SHM lock).
Owner-wide shared mappings/grants are not implicit and are not implemented here.

A short registry mutex plus condition variable guards owner, connection, permit
and resource membership/state/counters. It is never held during native acquire,
trusted borrow callbacks or waits for their completion. A permit holds admission
and lifetimes, not a mutex. Nested acquire/borrow of a second resource while a
first handle is borrowed must work. A whole-callback RWMutex wrapper would fail
this test through lock upgrade; it is not an acceptable implementation.

API: owner.newConnection(ctx); connection.begin(ctx); permit.acquire(ctx,purpose);
permit.borrow(ctx,resource,fn); permit.release(); resource.close(); connection.close();
owner.close(). Permits and resources bind to both owner AND connection. No shared
grant, cross-connection borrowing, raw token restoration or native handle export.
Borrowed native handles are valid only during trusted callbacks and cannot escape.
These internal objects are pointer-only; do not copy or construct zero-value
tokens/resources. A future opaque-ID bridge must resolve only published bindings
before calling lifecycle methods; these structs are not an untrusted token API.
A future stock bridge may temporarily set real winFile.Fh under its per-file lock
and reset it to invalid in defer; this stage does not add that bridge.

## Closing and in-flight ownership

Owner/connection closing rejects new connections/operations immediately. Already
admitted live permits may still acquire/borrow to finish their callback; close
waits for them to release before final resource/broker teardown. Terminal error
instead refuses future IO while allowing cleanup/release to drain.

Each permit method first validates membership and increments its method-inflight
counter under the short mutex, then drops it before native work. Resource borrow
also increments that resource's reference count. All counts are decremented in
defer, including propagating callback panics. Acquisition owns any returned handle
even on error; it registers a qualified resource before publication. Publication
never leaves an unaccounted IO interval after release.

release first marks the token ending (rejects new methods), waits for its opening
and borrowed methods to finish, then removes it and decrements owner/connection
operation counts. Resource close marks only that resource closing and waits only
for its own borrows, not the enclosing permit. This allows a callback to release
one resource while its operation continues. Connection close drains its permits
and resources, not other connections; owner close drains all of them.

Native handles are consumed once before close; uncertainty latches terminal
state and never retries a numeric handle. Temporary metadata close errors are
also retained. Registry locks are not held while invoking cleanup callbacks.
Self-release/close from inside one's own in-flight borrow is not supported;
callbacks must not wait on their own lifetime. Nested acquisition of OTHER
resources is explicitly supported and is the purpose of this stage.

## Eight native contracts

1. A permit admitted before owner closing can finish lazy acquire/borrow; new
   admission is closed, and owner shutdown waits for permit release.
2. While borrowing a real first file, acquire, borrow and close a second file
   without releasing the operation or waiting on an owner RWMutex.
3. Resource close begins, rejects new borrows and drains an active borrow only.
4. Native open returns a live handle while paused; concurrent permit release and
   owner close cannot finish until acquisition qualification/publication finishes.
5. Closing rejects new connections/permits; ending/retired permits reject work.
6. Owner and same-owner connection mismatches refuse before callback invocation;
   released tokens cannot regain authority via later connections/resources.
7. Native handle+error is consumed once, with primary and cleanup errors retained.
8. Borrow callback panic propagates but releases its method/resource counts, so
   subsequent resource/permit/owner close completes without lock or ref leak.

Fixtures first run existing broker/lease native prerequisite controls. The initial
PERMIT_OWNER_EXPECTED_RED marker is emitted only for the new unimplemented-owner
sentinel: it demonstrates missing construction, not that downstream concurrency
bodies executed. Implementation runs must execute every positive body. All waits
have local deadlines and cleanup shares one owner shutdown attempt; timed-out
shutdown is retained, never forced into freeing active resources.

## Unresolved boundaries

Owner/connection/resource counting does not solve external SQLite creation of
SHM, deletion/recreation semantics, DMS recovery, private stock SHM node/mapping
ownership or actual SQL interoperability. Host-long broker no-share-delete guards
are retained as a transitional experimental constraint, not a complete compatible
VFS. No existing DB/WAL is migrated, truncated, deleted or downgraded.

The implementation retains the broker mutex across qualification of an opening
only; registry/refcount locks are independent and are not held during it. No
borrow callback holds that broker mutex. Metadata cleanup uses real CloseHandle,
separately from the data-resource close injection hook. Cleanup failures latch
terminal state before the admitted method releases its lifetime count.
