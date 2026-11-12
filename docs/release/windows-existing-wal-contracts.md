# Private existing-WAL method slice: native RED scaffold

This is not accepting SQL/WAL/SHM support. Existing main-only tests still use their
old lease gate. Only this new joint experiment uses the already-qualified permit
owner and a connection-bound adapter, with a new unimplemented RO resource method.
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
Read/size/close share lifecycle helpers. No accepting WAL record is published yet.
The new WAL FxOpen recognizes exact bound -wal and input 0x80006, then returns a
counted CANTOPEN. It does not call stock xOpen, RO acquisition or a new constructor.
Thus native RED can reach an actual private callback without generator setup
failure. Old -linked generation stays unchanged; -linked-wal is not run in RED.

Independent resource stub: permit.acquireReadOnlyWAL, purpose leaseWALReadOnly=4.
Future implementation must use a fresh relative FILE_OPEN with READ_DATA,
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
   pre-cancel refusal. Additional acquire failure/close uncertainty tests remain
   required when the new RO acquisition implementation is introduced.

Every fixture holds a real separate stock process with checkpointed schema and
new committed WAL-only row. Immutable main-only control is fixture-only. Existing
broker/permit construction must succeed independently. The first four fail only
at EXISTING_WAL_OPEN_EXPECTED_RED after actual recognized FxOpen; the fifth fails
at EXISTING_WAL_RESOURCE_EXPECTED_RED after a live permit reaches its new stub.
These initial failures do not claim the later positive/negative bodies executed.

No SHM mapping, DMS, WAL recovery, external sidecar retirement/creation safety or
concurrent tool confidentiality claim follows. The long no-share-delete broker
constraint remains. Genuine private bytes must come from private bound methods,
never stock forwarding or copied expected buffers.


Resource acceptance is intentionally incomplete at this RED checkpoint. Before
GREEN is accepted, add positive native handle+error consume-once, close-fault and
close-panic terminal retention, cancellation during native open, and operation
release/owner-closing races for the NEW readonly acquisition path. Existing permit
tests exercise the old acquisition method and cannot substitute for these cases.
Five top-level passing nodes alone must not be advertised as complete RO-resource
qualification until those missing behaviors are covered and executed.
