# Stage D: dedicated readonly private SQL session

RED design and scaffold only. Production/tool authority stays disabled. C's
native acceptance precedes this run. A new registration composes owner metadata,
exact URI admission, existing-WAL RO authority, retained B mappings and C slots
before publication; no old overlay or stock default registration is modified.
Its RED xOpen explicitly refuses actual qualified main URI0x141 and increments a
specific counter. A SQL error without that counter is setup failure, not RED.

## Session ownership contract for GREEN

A dedicated DB pool and fixed Conn are internal only. Do not expose raw DB/Conn,
Rows or Stmt. Every SQL execution obtains a real lifetime permit before entering
SQLite. Query transfers it to a Rows wrapper until EOF/error/Close, including time
between Next and Scan. Rows Next/Scan/Close and diagnostics are serialized. A
prepared statement owns a separate lifetime permit; its query owns another Rows
permit, not a borrowed statement-lifetime substitute. Close rejects new work,
drains actual execution and Rows, finalizes owned statements, closes fixed Conn
then dedicated DB, verifies main/SHM detach, and only then closes the storage host.
A successful sqlite3_close_v2 is not proof of statement destruction. Context
expiry reports blocked drain without force-unmapping; ownership remains retained.
The current RED forwarding session bodies do not implement these guarantees and
cannot execute SQL successfully because their paired xOpen is refusing.

Diagnostics observe a paused first row on the actual private connection. The
trusted observer matches actual Pager/Wal file/generation, page0 pointer, and C
shared reader-slot ledger without exporting or retaining pointers. It waits for
B.busy to clear under B.mu, then compares state while preventing new B/C native
reservations. It must never reenter SQL/file locks or be called by a busy holder
waiting for that same file mutex. Snapshots are point-in-time evidence only:
normal mode + reliable readonly SHM + live mapped page + live shared read slot.
They cannot assert no historical heap fallback. SuitableReadmarks is not proof
that a corresponding slot can actually be locked.

## Fixed six contracts

- TestPrivateSQLHeldWALRead
- TestPrivateSQLNativeWALIndexWitness
- TestPrivateSQLRowsLifetime
- TestPrivateSQLStatementDrain
- TestPrivateSQLReadonlyFailures
- TestPrivateSQLStorageRequalification

Each first requires a private SQL result for the real stock child's committed
WAL-only row, independently absent from the checkpointed main. The stock child
holds a real read transaction and established readmark until explicit release.
Stock queries before/after remain independent controls. First qualified main
xOpen refusal is the intended RED; no lifecycle/diagnostic assertions beyond
that point are represented as already executed.

ReadonlyFailures initially covers actual write rejection; a naturally produced
state with no usable readonly readmark remains unqualified. Old marks and zero
may be usable. Do not equate no active reader or appended WAL with no suitable
mark, inject SHM edits and call them normal stock operation, or silently recover.

Requalification proves active-owner share guards refuse deletion, then after
explicit SQL/host/stock-holder shutdown a fresh owner can qualify a new fixture
identity. It does not permit active SHM replacement or solve unknown-alias
confidentiality. Old binding/generation must remain revoked. VFS registrations
are retained process-lifetime, not dynamically unregistered; this experiment
is not an unbounded production session manager.
