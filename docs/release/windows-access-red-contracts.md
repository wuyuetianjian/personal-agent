# Qualified xAccess: independent native RED scaffold

This adds an independent nondefault metadata VFS. It is NOT wired into the main
read VFS and cannot open content: FxOpen remains the base CANTOPEN callback.
Observation is not a reusable token or open authority. Production is unchanged.

Authority interface: Observe(exactName string, accessFlag int32) (bool, error).
ExperimentalAccessRegister creates a retained binding; ExperimentalAccessHits
counts actual native VFS xAccess dispatch. Error/panic yields IOERR_ACCESS and
zero output. The windowsprobe authority currently returns only the unique
errPersistentAccessUnimplemented sentinel. Positive behavior is deliberately RED.

Four top-level tests (no subtests):
- TestPersistentAccessExistingWALAndJournalTruth
- TestPersistentAccessMissingAndEmptySemantics
- TestPersistentAccessRefusesUnqualifiedObservation
- TestPersistentAccessObservationGrantsNoOpenAuthority

Every test first obtains independent stock-VFS/native metadata controls. The real
WAL test additionally creates and holds a stock SQLite connection and verifies a
row, and verifies nonempty WAL plus a synthetic nonempty metadata-only journal.
Synthetic empty/missing cases exercise both WAL and journal names. A journal
fixture is not submitted for SQL recovery.

Each test requires a successful private observation before any negative claim.
At current RED, the real private xAccess callback reaches the authority stub and
fails with ACCESS_OBSERVE_EXPECTED_RED. This is an observation-stage RED, NOT
proof that later unsafe-name, real symlink, injected open/close errors, replacement
or FxOpen-refusal bodies executed. No fixture failure receives that marker.

After its initial private observation succeeds, the fourth test requires actual
private FxOpen refusal, replaces the journal and independently verifies a new
identity, then requires another private observation and FxOpen refusal. These
replacement/open-refusal checks are not reached by the current observation stub. It does NOT test a future accepting xOpen's requalification;
that contract is deferred until a genuine WAL-opening callback exists.

The future implementation must close temporary metadata handles exactly once,
propagate uncertainty, accept only exact bound names/flags, and preserve pinned
stock empty-file EXISTS semantics. Observation grants no journal tool-read safety
and does not close the external-create confidentiality gap. Native symlink setup
failure is setup failure, never skipped/accepted feature RED.
