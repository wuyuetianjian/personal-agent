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

The next experiment therefore has a separate `sqlite-nativeprobe-private`
driver and private connection wrapper. Only this wrapper may expose journal
capture. The constructor must reject caller URIs and construct its own fixed
private-cache/win32 URI from a strictly validated absolute DOS filename; it is
not a replacement for production storage.OpenSQLite or its hooks/extensions.
Factory, validator and child-only shared-default control start as explicit
refusal stubs. Their native RED is distinct from later journal-behavior RED.

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
