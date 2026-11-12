# Stage D: dedicated readonly private SQL session

Historical RED design, with the GREEN candidate below. Production/tool authority stays disabled. C's
native acceptance precedes this run. A new registration composes owner metadata,
exact URI admission, existing-WAL RO authority, retained B mappings and C slots
before publication; no old overlay or stock default registration is modified.
Its RED xOpen explicitly refuses actual qualified main URI0x141 and increments a
specific counter. A SQL error without that counter is setup failure, not RED.

## Session ownership contract for GREEN

A dedicated DB pool and fixed Conn are internal only. Do not expose raw DB/Conn,
Rows or Stmt. A session owns an independent real owner-operation permit before entering
SQLite, including initial connection bootstrap. Query also obtains a core ticket for its Rows wrapper until proven EOF/Close, including time
between Next and Scan. Rows Next/Scan/Close and diagnostics are serialized. A
prepared statement owns a separate lifetime permit; its query owns another Rows
permit, not a borrowed statement-lifetime substitute. Close rejects new work,
drains actual execution and explicitly closed Rows/Stmt, closes fixed Conn
then dedicated DB, verifies main/SHM detach, and only then closes the storage host.
A successful sqlite3_close_v2 is not proof of statement destruction. Context
expiry reports blocked drain without force-unmapping; ownership remains retained.
The context bounds the drain wait, not synchronous native finalization after the
single finalizer begins.
The historical RED forwarding bodies did not implement these guarantees; the
current candidate implementation and limitations are described below.

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
explicit SQL/host/stock-holder shutdown a fresh owner can qualify a recreated fixture
at the same path. It does not permit active SHM replacement or solve unknown-alias
confidentiality. Old binding/generation must remain revoked. VFS registrations
are retained process-lifetime, not dynamically unregistered; this experiment
is not an unbounded production session manager.

## GREEN candidate implementation (native result still pending)

The RED above is historical: commit 5c53c28, run 43257104150 produced exactly
18 qualified actual-main 0x141 refusals. The candidate now routes validated main
opens to the existing resource/slot bridge, and WAL opens to the existing-only RO
bridge. It never forwards private content reads to the default stock VFS.

Only an internally constructed canonical/mode=ro/this-registration URI may be
passed to the session constructor. This is not a user connection-string API.
This slice accepts only `SELECT value FROM fixture` for Query/Prepare and the
fixed INSERT refusal probe. Arbitrary SQL, ATTACH/DETACH, pragmas and transaction
control are not enabled: they could trigger storage teardown while owning a
core ticket and require a separate integration design.

stateMu controls admission, inflight count, owned Rows/Stmt count, and one final
closer. It is never held during native calls or waits. coreMu serializes every
native connection/statement/Rows operation and diagnostic; Rows state is taken
after coreMu. Close first enters draining. Existing Rows can continue or close;
existing Stmt cannot create new Rows. A Stmt with dependent Rows refuses Close
before any native teardown, allowing a later Close after its Rows retire.

Bootstrap has both session inflight protection and a real independent owner
operation permit before the main attachment exists. Terminal failure retains
that permit even after the session active-operation counter returns to zero.
After a successful Query/Prepare, its real key/connection-bound B ticket is
obtained before exposing the wrapper. EOF/Close only drops that ticket after
successful native teardown; unknown query/cleanup error or panic latches a
terminal state and retains ownership. Native teardown is never repeated after
an uncertain result. All owned tickets must retire before fixed Conn.Close then
DB.Close. Actual attachment retirement and empty ticket/busy state are verified
before calling storage host.close, not inferred from close_v2 alone.

Contexts are checked before admission. QueryContext, PrepareContext and
Stmt.QueryContext receive WithoutCancel contexts deliberately: database/sql's
asynchronous awaitDone cleanup otherwise bypasses coreMu. This experiment does
not support in-flight SQL cancellation. An original caller context cancelled
after admission does not release a returned Rows ticket. Close(ctx) limits only
waiting for explicit teardown; timeout retains the draining session and all
native resources. Uncertain session cleanup prevents fixture cleanup from
force-closing the underlying owner/root.

Tests additionally check real diagnostic/Next/Close serialization, EOF and
explicit Close retirement, cancellation-at-admission versus after-delivery,
Stmt dependent-Rows refusal, closing Stmt admission refusal, exact READONLY error
and independent stock total/bad-row counts, and active main/WAL/SHM delete share
refusals after the external holder has stopped. Fresh-owner recreation is a new
qualification at the same path, not proof that NTFS assigned distinct numerical
file IDs; file ID reuse is permitted. Active replacement and natural missing
readmark cases remain unqualified. No ordinary-user, unknown-alias, production,
or tool-read authority is enabled by these tests.

The fixed valid INSERT's normal exact base SQLITE_READONLY return is not a
cleanup failure. Fixed conn.go Exec defers stmt.Close; stmt.Close calls finalize,
and lib/sqlite.go finalize resets and deletes a valid statement even if its return
code repeats the prior execution error. This narrow path releases its execution
ticket only after checking B/C hosts and the owner are healthy/nonterminal and no
retained node exists; release errors still latch terminal state. The test verifies
stock total/bad-row counts, zero execution tickets, a subsequent successful private
query and normal session shutdown. Extended READONLY codes, panic, MISUSE and
IOERR are not implicitly covered by this exception. Other error retention is a
conservative experiment policy, not a claim that every SQLite error leaves native
statements alive.

Four normal-exit children inject pre-teardown Rows/Stmt error or panic, proving a
still-live object remains pinned, close attempts are not repeated, original error
causes survive panic conversion, session.close reports retained without touching
host shutdown, and an explicitly started underlying owner.close remains blocked.
These are controlled uncertainty injections, not proof of every possible native
SQLite finalize failure. Each child checks all test errors before exactly one
success marker and normal exit.

Before releasing the session's independent owner permit, retirement verification
also checks owner.terminal and that this connection has no registered main/WAL
resources. The new bridge witness snapshots file records for this exact binding,
then requires every remaining record to be closed successfully with its lease
consumed, and the binding to be nonterminal. It never dereferences freed native
sqlite3_file memory. Registry snapshots are taken before file mutexes, not held
across them. In this slice there is one dedicated connection; closing admission,
active==0, objects==0 and coreMu exclude further native session work between this
check and permit release. This is not an atomic global health-and-release grant:
independent connections or arbitrary external callers poisoning the owner in that
interval have not been qualified. The old permit implementation remains unchanged.
