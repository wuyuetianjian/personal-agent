# Qualified xAccess: independent experimental observer

This adds an independent nondefault metadata VFS. It is NOT wired into the main
read VFS and cannot open content: FxOpen remains the base CANTOPEN callback.
Observation is not a reusable token or open authority. Production is unchanged.

Authority interface: Observe(exactName string, accessFlag int32) (bool, error).
ExperimentalAccessRegister creates a retained binding; ExperimentalAccessHits
counts actual native VFS xAccess dispatch. Error/panic yields IOERR_ACCESS and
zero output. The native RED at fc0add8 (run 43227676669, job 131215003606) reached
12 actual xAccess dispatches and the designated unimplemented sentinel. The
observer implementation now awaits independent review and native GREEN evidence.

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
At the recorded fc0add8 RED, the real private xAccess callback reached the
authority stub and failed with ACCESS_OBSERVE_EXPECTED_RED. That established
an observation-stage RED, NOT
proof that later unsafe-name, real symlink, injected open/close errors, replacement
or FxOpen-refusal bodies executed. No fixture failure receives that marker.

After its initial private observation succeeds, the fourth test requires actual
private FxOpen refusal, replaces the journal and independently verifies a new
identity, then requires another private observation and FxOpen refusal. These
replacement/open-refusal checks were not reached by the fc0add8 observation stub. It does NOT test a future accepting xOpen's requalification;
that contract is deferred until a genuine WAL-opening callback exists.

The future implementation must close temporary metadata handles exactly once,
propagate uncertainty, accept only exact bound names/flags, and preserve pinned
stock empty-file EXISTS semantics. Observation grants no journal tool-read safety
and does not close the external-create confidentiality gap. Native symlink setup
failure is setup failure, never skipped/accepted feature RED.


## Implemented metadata-only contract (awaiting native GREEN)

Construction captures the qualified Root/ancestor identities and compares the
canonical directory locator against the actual root handle's normalized path.
It owns no extra long-lived filesystem handles. Each call takes observer.mu then
root.mu; Root does not acquire observer.mu. It never calls a Root method while
holding root.mu. Root.Close cannot retire a borrowed lineage mid-observation.

Only exact main/-wal/-shm/-journal names and EXISTS/READ/READWRITE flags qualify.
Metadata opens share read/write/delete and never acquire content IO rights.
Only a bare STATUS_OBJECT_NAME_NOT_FOUND from the leaf open, without a handle,
after successful lineage checks is absence. PATH_NOT_FOUND, joined errors,
ancestor failures or returned-handle errors remain errors. Regular single-link
local NTFS metadata and exact spelling are checked before returning a result.
READWRITE follows stock read-only attributes, not ACL writability promises.

Every returned valid metadata handle is consumed once, even with an error. An
ordinary acquisition error whose cleanup succeeds does not poison the observer.
Cleanup uncertainty and lineage failure permanently poison this observer only;
Root itself has no terminal-error latch and is NOT claimed globally revoked.
No identity is registered as public or granted to tool reads. Metadata observations
are transient, not an atomic namespace snapshot or a substitute for later opens.

The existing four tests now additionally cover readonly attributes/restoration,
invalid flags, real hardlinks/directories/reparse objects, nonexact names,
handle+error close-once, path failure distinct from leaf absence, real closed root,
lineage-failure terminal and cleanup-failure terminal. The lineage fault hook runs
only AFTER native inspection of every retained directory and cannot bypass it.
Every filesystem cleanup/attribute restoration checks errors. Test hooks are
trusted experiment-only fault injection, not public authority interfaces.
