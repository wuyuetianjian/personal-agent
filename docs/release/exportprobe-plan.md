# Disconnected export capability experiment: specification and plan

Scope: portable, explicitly tagged `nativeprobe_export`, no production imports,
filesystem access, SQLite integration or user data. This is a narrower experimental
authorization contract, not a solution to arbitrary unknown historical copies.
A fixture issuer is not a user approval UI. No existing workspace contract or
native readiness gate changes.

## Specification

A registry owns immutable bounded byte snapshots. Candidate qualification only
admits a snapshot; it never grants export. A trusted in-process issuer creates
opaque session-and-purpose authorities, and explicitly grants one candidate to
one authority. Callers cannot substitute session strings, names, hashes or public
booleans for authority. A digest binds exact bytes, never proves non-sensitivity.
Authority is decided by exact pointer membership in the private registry and
the exact opaque authority instance. Epoch, nonce and monotonic generation are
internal issuance records, not a validated serialized bearer-token protocol. Zero values and copied/forged handles
fail closed. No untrusted deserializer or production constructor is exposed.

Read admission reserves a bounded concurrent operation; copies snapshot bytes
outside the short registry mutex. Commit occurs under that mutex after checking
cancellation, expiry, authority, candidate and grant are still live. Only committed
bytes may be returned. Revoke linearizes under the same mutex and waits for
admitted operations of that grant to leave commit/abort. A read committed before
revoke may be received by its caller after revoke returns. Neither network delivery
nor already-committed bytes are retractable. A cancelled revoke wait leaves the
revocation in effect and preserves outstanding resources.

Bounds are fixed: at most 16 live authorities, 32 snapshots, 64 grants, 64 KiB per
snapshot, 1 MiB snapshot bytes, and 8 reads in flight. Snapshot and returned buffers
are copied; a read slot covers its copy and commit. Registry close revokes all,
waits admitted reads and discards snapshots. All committed result copies, including not-yet-returned copies, are outside
registry quotas: read slots end at commit rather than delivery. Expiry is checked at admission and
commit using a trusted clock; no arbitrary untrusted time input. Entropy and clock
dependencies execute under the registry mutex; bounded progress is not promised
for arbitrary blocking or panicking injected callbacks. Issuance counters
are monotonic and never reused within an epoch. Closed registries never reopen.

## Six fixed test contracts

1. TestExportCandidateNeverAuthorizes
2. TestExportExplicitSnapshotIsolation
3. TestExportOpaqueScopeAndRevocation
4. TestExportVersionExpiryAndBounds
5. TestExportCommitCancelAndRevoke
6. TestExportDisconnectedNativeGate

Tests use synthetic strings only. The final contract checks this package has no
filesystem/native/runtime coupling and production packages do not import it;
it does not claim a mocked native implementation is qualified. Portable and race
runs are separate evidence, never Windows kernel qualification.

## TDD implementation sequence

1. Add the six positive/negative contracts and refusal stubs. Run the tagged
   suite and retain the expected behavior failures, distinct from build failures.
2. Implement opaque registries, copying and quota checks; enforce explicit grant.
3. Implement short-lock admission/commit/revoke and bounded drain; channel hooks
   are trusted test-only synchronization, not external callbacks under the mutex.
4. Run six contracts, repeated race checks, go vet and dependency disconnection.
5. Independently review. Do not wire into tools or relax any native gate.
