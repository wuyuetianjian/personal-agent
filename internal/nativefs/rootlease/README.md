# Disconnected root lifetime experiment

This package has no exported constructor or operation and no production callers.
Its backend interface is implemented only in test files. It does not prove DB
exclusion, session authorization, admission transactions, or native readiness.
The Windows fixture adapter is test-only and does not bypass reduced-token tests.

A registry reserves capacity before acquisition and retains it until acquisition
or abort cleanup finishes. Pending references cannot read. Test-only activation
copies the exact binding; reads borrow internally without exposing a backend or
callback that could escape. Shutdown refuses new work, waits ongoing opens and
reads, and reports cleanup errors. Nothing reopens a saved path on validation.

Each host uses a fresh 256-bit random epoch and each attempt a random lease key.
Canonical references are opaque lifecycle identifiers, not a complete permission
scheme. Production authorization/DB protection must be designed and integrated
before any entry point is exported. Active capacity is at most 128, including
opening and cleaning entries. A bounded tombstone set prevents reference reuse:
at most 4,096 references are issued per registry lifetime; four random collision
attempts fail closed. Entropy failure refuses the operation.

Portable tests first ran against refusal stubs and failed. They then passed with
race detection, including blocked acquisition/abort/shutdown and copied binding
checks. Windows lifecycle tests exercise real root ownership under the current
runner token, not an independent standard-user account. Native results must be
checked for the exact commit; cross-compilation is not runtime qualification.

## Explicit SQLite main-object experiment

An additional `nativeprobe_sqlite_identity` test build exercises a real SQLite
main-file owner through this private registry's test-only backend. A dedicated
runner creates a temporary copy of the exact checksummed SQLite module plus a
reviewable experiment-only driver patch; it never edits the module cache or
production go.mod. The initial patch/guard refused; run 43002158984 at f513613 established five
positive tests failing three times on Windows because implementation was absent.
The implementation must pass that same manifest on the target kernel.
Compilation is not runtime evidence.

Each read borrows the actual SQL connection and obtains an owned metadata-only
handle duplicate inside Raw; no saved path is reopened to manufacture identity.
The callback must last through protected reading and duplicate cleanup. This
covers neither WAL/SHM/journals/attachments nor private directory ancestry or
other connections. It cannot activate admission or any production capability.
A formal driver API and full protected-storage design remain prerequisites.

### Current WAL requires controlled private-cache construction

A journal `sqlite3_file*` returned by SQLite is borrowed after Btree locks have
been released. Raw, the driver's lock and a connection mutex do not prevent a
second shared-cache connection from changing the shared Pager. A one-connection
pool or an assumed default does not fix this lifetime problem.

The experiment therefore has a separate `sqlite-nativeprobe-private`
driver and private connection wrapper. Only this wrapper may expose journal
capture. The constructor must reject caller URIs and construct its own fixed
private-cache/win32 URI from a strictly validated absolute DOS filename; it is
not a replacement for production storage.OpenSQLite or its hooks/extensions.
Factory, validator and child-harness shared-default control initially used
explicit refusal stubs. Five positive factory contracts failed three times at
fd7eacc in run 43008362582. Factory validation and journal behavior are separate
qualification stages; constructor-blocked WAL tests do not prove journal RED.
At 712e0be in run 43009315063, all six factory contracts passed three times,
including the three child completion markers. Only the three positive WAL
contracts then failed three times at the explicit journal refusal stub.

The implementation now holds the driver lock and SQLite database mutex across
main/journal capture and metadata-only duplication. It uses the public
sqlite3_file/io_methods prefix and the pinned generated function-slot ABI, not
private winFile layout. All acquired duplicates are closed on failure; borrowed
SQLite handles are never closed. At cf16173 in run 43011024828, the dedicated
Windows job passed all 20 required contracts three times with both package
completions and no fail/skip events. This qualifies the tested current-main/WAL
experiment, not complete storage protection or general native readiness.

Tests include an isolated child with global shared-cache default enabled. With
read_uncommitted enabled, controlled private connections must see committed data,
while both ordinary default-cache and explicit shared-cache controls must see
the pending value. This avoids the driver's unlock-notify retry and also proves
the global-default control really took effect, rather than trusting a label. The child helper is excluded from
the parent manifest. Current-WAL tests retain both actual main/journal duplicates
through reading and refuse incomplete observations; missing journal is not
proof that sidecar data is absent. Rollback, SHM, stale/future sidecars,
attachments and temp files remain unqualified. No production capability is
activated. Disk-handle lifetime checks use a positive-controlled exact PSS set,
not a tolerance on Go runtime thread/event handle counts.

### Remaining storage boundary

The stock Windows VFS reopens saved paths for later sidecars. A pinned directory
identity does not confine those opens: directory write-attributes/reparse changes
can redirect future storage even when rename/delete sharing is denied. Current
main/WAL exclusions do not cover SHM, historical or future objects. A separate
same-user process or disk workspace copy does not remove that namespace race.

SHM source review also found separate mapping and per-connection locking-handle
opens with different lock windows. No external private-layout getter or global
syscall interception is introduced. Production remains disabled pending complete
controlled storage I/O and native qualification, not merely a larger list of
currently observed identities.

## Stock-driver anonymous TEMP VFS experiment

A separate explicit-tag experiment registers a non-default VFS through SQLite's
public ABI. It owns its file prefix and never converts a stock winFile layout.
Its main database is memory-only; named paths, main/WAL/SHM files and dynamic
extension loading have no filesystem fallback. Initially every xOpen refuses.
A valid behavioral RED must show the real SQL TEMP workload reaching an anonymous
exact-flags TEMP_DB refusal, not an earlier connection or pragma failure.

The positive contract requires deterministic row payloads, host-side forward and
reverse verification, actual kernel read/write bytes and complete cleanup. This
is a future temporary-file consumer, not a complete persistent-storage VFS or a
production file capability. Registration lifetime includes physical connections
with zero open files; actual driver close errors retain ownership rather than
trusting database/sql pool close results alone.

This foreign ABI uses pinned-version function slots and foreign allocations.
Explicit tagged go vet reports unsafe-pointer diagnostics at those conversions;
compilation is not a clean tagged-vet result. These accesses require independent
ABI/lifetime review and native evidence. No production vet gate is disabled.

At 2c37706 (run 43017837341), the stock TEMP job reached exactly that refusal
three times: anonymous flags 0x21e, on insert 2, with no scratch objects opened.
Both path/flag and physical-close ownership contracts passed three times. The
separate legacy identity job accidentally selected this new TEMP contract via
its broad test-name prefix, so that job failed despite its original 20 tests
passing. Both runners now select their complete fixed manifests by exact names;
no failed events or required tests are filtered out.

The next private owned-scratch adapter has four explicit contract groups for
offset/EOF I/O, independent lineage ownership, bounded failures and parent
mutation. Its constructor and methods initially refuse. These are adapter
contract REDs until implemented, not evidence that the described I/O or failure
paths have already run. The TEMP SQL consumer remains a separate required RED.

At 762ce08 (run 43020075092), all four adapter groups failed three times at the
explicit unimplemented constructor, while the existing 20-item identity job
passed with zero TEMP events. The private adapter implementation now supplies
owned creation and bounded handle I/O, but still requires native verification
against those unchanged contracts. It is not yet connected to the TEMP VFS;
that SQL spill contract continues to refuse. Sequential contract results do not
qualify concurrent interleavings or complete namespace confinement.

At 636b63e (run 43021783102), the four unchanged sequential adapter contracts
passed three times each. The next test-only checks exercise controlled Create
revocation, write/close overlap and competing growth while one write is held.
Their bounded scheduler windows are regression evidence, not a formal proof of
all possible races; the implementation is unchanged for these checks.

A separate statement-lifetime contract addresses idle Conn-bound prepared
statements, which database/sql does not always put in its physical connection's
openStmt set. The current controlled workload explicitly closes all statements.
The new contract first uses a safe fake native driver through real database/sql;
missing drain fails before any real SQLite late-close case is entered. Only
once that protocol passes may the real prepared-statement lifecycle run. This
is a protocol RED, not proof that native statement drain has already succeeded.

The `0fe7034` native run also passed the bounded scratch concurrency contract in
three rounds. Its statement-lifetime safety protocol failed in all three rounds:
an idle Conn-bound statement was not finalized before physical connection close.
The real SQLite stage was deliberately not entered. The experimental connector
now tracks each prepared statement and drains it before closing the physical
connection. Late closes reuse the recorded result without touching released TLS;
fixed-driver finalize errors remain observable without pretending a destroyed
statement is still live. Optional statement context interfaces are preserved only
when the underlying driver implements them. This change still requires native
qualification; portable protocol/race tests and cross-compilation are not native
SQLite lifetime evidence. Anonymous TEMP `xOpen` remains refusing at this stage.

At `48dd77b`, the statement-lifetime top-level test and every selected safety,
error, context-interface, concurrent-close, real idle-statement and real prior-SQL
error subtest passed three native Windows rounds. The stock spill contract still
failed because `xOpen` remained refusing. The next ABI contract stage adds an
explicit root-bearing experimental constructor with an independently owned root
and a version-1 foreign methods table. It still refuses every open and does not
publish the table to a file. New positive ABI tests are expected to fail at that
refusal; this is not evidence that read/write callbacks executed. Production
capabilities remain disabled.

At `9d03992`, all four new ABI contracts failed in three native rounds at the
explicit root-bearing `xOpen` refusal, while the existing eight non-spill TEMP
contracts passed. The experimental bridge now connects only anonymous exact-flag
TEMP_DB opens to owned scratch handles. File tokens bind to their foreign POD
address; close revokes new admission and drains all previously admitted callbacks
before consuming the handle. The real SQL test retains its workload and checksums,
and additionally requires fresh live handle metadata, actual read-byte growth
during scans, an empty fixture directory and exact handle-inventory restoration.
This implementation is pending native qualification and does not qualify main,
WAL, SHM, arbitrary namespace races or production features. The final local tagged
Windows vet check still fails with 37 `unsafe.Pointer` diagnostics at translated
foreign-ABI boundaries; untagged production vet passing does not erase that result.
`PeakKernelSize` is the maximum actual size sampled after successful writes, not
an exhaustive peak across truncate-only growth. VFS close errors are reported by
`WithConnection` separately from driver close errors because SQLite's destructor
can discard the VFS return code; explicit owner close retains those errors too.
