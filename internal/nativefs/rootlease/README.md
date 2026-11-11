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
