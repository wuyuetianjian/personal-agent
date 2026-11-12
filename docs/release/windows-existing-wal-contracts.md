# Private existing-WAL method slice: experimental GREEN candidate

This is not accepting SQL/WAL/SHM support. Existing main-only tests still use their
old lease gate. Only this new joint experiment uses the already-qualified permit
owner and a connection-bound adapter, with a new independently implemented RO resource method. Native GREEN is pending.
Production remains disabled, format and journal_mode unchanged for existing DBs.

Stock protocol evidence is separate: c12ccca / run 43231504076 /
job 131227711089 recorded 6 passes and 12 forwarded stock opens. Main readonly
input 0x141 -> out 1; main rw 0x142 -> out 2; WAL both 0x80006 -> out 2.
Private target explicitly accepts an existing qualified WAL requested with exact
READWRITE|CREATE|WAL but grants READONLY through SQLite's outFlags protocol. It
never promises RW or creates an absent WAL. The input CREATE bit is not authority
to create here. This read-only capability is not a journal-mode/data migration.

Current raw main-method tests use the older exact READONLY|MAIN_DB (0x101) entry;
the SQL URI main request 0x141 is NOT yet accepted. URI handling and genuine SQL
must be separately implemented and tested, not silently stripped for compatibility.

## Interface / split

ExperimentalExistingWALRegisterRO binds OpenMain, OpenWALReadOnly and Revoke.
Each returns an opaque resource lease; Borrow begins a fresh connection-bound
permit, borrows only the exact expected purpose/connection resource, and releases
the permit in defer. Open acquires under a short-lived permit; releasing that
permit does not close the returned resource. Close consumes the resource. The
adapter rejects an old RW WAL resource substituted as RO, even though the general
permit borrow machinery itself is purpose-agnostic.

The main bridge now records role main/WAL and centralizes record publication;
main lock/unlock/reserved callbacks require role main under the per-file mutex.
Read/size/close share lifecycle helpers. The new WAL FxOpen recognizes exact bound -wal and input 0x80006, acquires a
qualified RO resource, calls the separately pinned existing-WAL initializer, and
publishes role WAL last. It never calls stock xOpen. Its factory prepares the
complete callback/binding before a single VFS registration. Old -linked output
stays unchanged; the candidate adds a separate -linked-wal overlay.

Independent resource implementation: permit.acquireReadOnlyWAL, purpose leaseWALReadOnly=4.
It uses a fresh relative FILE_OPEN with READ_DATA,
READ_ATTRIBUTES, SYNCHRONIZE, not WRITE_DATA/APPEND_DATA/DELETE/WRITE_DAC/WRITE_OWNER
or write attributes/EA. Existing broker RW handles stay unchanged; do not claim
whole-owner OS authority is now read-only. Metadata observation grants no such
content authority and is not reused for this acquisition.

## Five fixed native contracts, three repetitions, no subtests

1. TestExistingWALPrivateHeaderAndSize: independent stock/native WAL header
   control; actual private FxRead/size; SQLite SHORT_READ zero fill.
2. TestExistingWALRoleAndFlagsRefused: positive private WAL open before negative
   role/flags/name tests, main lock refusal, RW/foreign-connection substitution.
3. TestExistingWALIndependentLifetimes: close main while WAL remains readable and
   vice versa; broker identities still exclude workspace reads after file close.
4. TestExistingWALMissingNeverCreated: positive WAL first, then a separate missing
   WAL authority actually attempts native metadata FILE_OPEN and fails; actual
   private FxOpen refuses and no file appears. This is bridge missing-error
   propagation, not proof that a complete missing-role owner exists.
5. TestExistingWALReadOnlyResourceQualification: actual granted-access mask lacks
   all write/delete rights, purpose bound, independent FILE_OBJECT cursor, and
   pre-cancel refusal. The same top-level test now also executes handle+error/cleanup failure,
   published close error/panic, cancellation during open, and release/owner
   closing races, with explicit transition signals and bounded waits.

Every fixture holds a real separate stock process with checkpointed schema and
new committed WAL-only row. Immutable main-only control is fixture-only. Existing
broker/permit construction must succeed independently. Native RED at 6548fb3 / run 43234055242 / job 131236592921 had 12 actual
FxOpen failures plus 3 resource-stub failures with qualified fixtures and no setup
errors. Those historical failures did not execute downstream bodies. Current
GREEN tests no longer tag an implemented FxOpen failure as expected stub RED.

No SHM mapping, DMS, WAL recovery, external sidecar retirement/creation safety or
concurrent tool confidentiality claim follows. The long no-share-delete broker
constraint remains. Genuine private bytes must come from private bound methods,
never stock forwarding or copied expected buffers.


## RO factory provenance and close uncertainty

Only newPersistentWALReadOnlyOwner creates eligible owners; an unexported registry
records provenance and acquireReadOnlyWAL rejects legacy owners. The factory wraps
its trusted data-close injection hook before publishing the owner, converting a
close panic into a terminal error. Thus inherited resource/owner close still uses
the unchanged permit lifecycle, consumes once and receives an ordinary error.
Adapters cannot opt out of this wrapper through a public API; direct mutation of
internal hook fields is unsupported. Normal native CloseHandle is unchanged.
The registry retains experiment-lifetime owner references and is not a production
resource-management design. This guarantee covers data-close hooks, not arbitrary
panics in beforeResourceClose/afterClosing/openData or other trusted callbacks.

RO acquisition owns both metadata and candidate handles, verifies actual granted
access is exactly READ_DATA|READ_ATTRIBUTES|SYNCHRONIZE, and independently verifies
metadata/IO identities, regular single-link NTFS snapshots, exact names and retained
lineage. An injected RW candidate fails qualification. Metadata cleanup uses the
native close path separately. Any cleanup uncertainty latches owner terminal
before the acquisition releases its in-flight permit count.

Required fault tests are now present but require actual Windows execution before
claiming qualification. They cover primary+cleanup error composition and one-time
candidate ownership, published close error and panic, legacy-owner refusal,
pre-cancel and cancel-during-open, and real native-open pause while permit release
and owner close both wait. They do not establish arbitrary hook-panic recovery or
full VFS/SHM/external-sidecar compatibility.
