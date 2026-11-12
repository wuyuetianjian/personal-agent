# Stock WAL flags diagnostic (native test-first RED)

This is a nondefault **stock protocol diagnostic**, not the private persistent
VFS, an authority layer or a WAL implementation. No production graph changes.
It uses one independent fixed-module overlay, no main/access/generated overlay.
Existing accepted modes and manifests remain unchanged.

Initial native RED: registration and real FxOpen dispatch work, but the callback
records a non-forwarded CANTOPEN result. DIAGNOSTIC_OPEN_EXPECTED_RED is permitted
only after actual dispatch and independent stock fixture controls. It establishes
missing forwarding, not observed WAL flags. The future GREEN change must forward
to the captured stock xOpen and record actual flags/output/rc; it must not convert
flags, synthesize outFlags or mutate the default VFS.

Two exact top-level contracts, three repetitions each:
- TestStockWALFlagsReadonlyMain
- TestStockWALFlagsReadWriteMain

Each test starts a separate process selected by the same test name plus a fixture
environment marker. The child creates a fresh WAL-mode DB, disables autocheckpoint
for that isolated fixture, creates a table, checkpoints that baseline, commits a new value, then keeps the stock DB open
until an explicit release handshake. This does not change a user's existing DB.
The parent requires nonempty WAL and SHM, confirms an immutable main-only query
has no committed new value, and independently reads the WAL-only value with a fresh stock
connection before and after the diagnostic query. Child stdout is privately drained
and cannot count as parent Go test events. Readiness/shutdown have local time bounds;
failed shutdown is reported, never accepted as a skipped test.

The diagnostic query uses a real SQLite URI selecting the nondefault diagnostic
VFS and mode=ro or mode=rw. It must return the WAL-only committed value, prove
actual callback invocation, and record successful forwarded main and WAL opens.
Pinned source _sqlite3WalOpen requests READWRITE|CREATE|WAL even when main was
opened readonly. Tests make that source-based prediction falsifiable: exact WAL
input flags, actual outFlags READWRITE and SQLITE_OK must be observed natively.
Main mode bits and outFlags must match ro/rw; full input values are logged too.
Do not treat compile success or source inspection as confirmed native observations.

A subsequent private read-only WAL-method slice must explicitly decide what to do
with these actual SQLite requests. It cannot silently discard CREATE or convert
RW to RO, and this diagnostic must never supply the actual bytes claimed as a
private implementation's reads. SHM, sidecar lifecycle and tool confidentiality
remain separate unresolved implementation/qualification work.
