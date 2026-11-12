# Windows temporary read-handle cleanup

Scope: `Root.read` and `acquireIOGuard` only. These disconnected foundations stay
outside production. No identity, exact-name, sharing, root-authority or capability
policy changes. NewRoot and other existing cleanup paths are not qualified here.

A per-call private `handleOps` seam selects native open and close operations.
Public paths always construct real native operations; no global hook or exported
injection exists. The RED checkpoint routed the old behavior through that seam without fixing
ignored cleanup errors or unclaimed handles returned with errors. It did not
replace any default path with a refusal stub.

The implementation candidate registers every valid returned handle before
inspecting the open error.
The guard consumes its candidate on unsuccessful qualification. Read owns every
successfully acquired temporary metadata/data guard until deferred reverse-order
cleanup. Each close is consumed once, never retried, and its error is joined with
the primary error. Any cleanup failure discards all read bytes. Caller-owned Root
remains owned by its caller: this patch does not revoke it or retry an uncertain
close; a later operation can independently acquire fresh temporary handles.

Native tests use real Windows handles and actual CloseHandle, then inject an
error after successful close. This tests error propagation and one-shot ownership,
not real operating-system failure semantics. RED handle+error tests retain a test
ledger to close only handles for which cleanup was never attempted, avoiding a
fixture leak without retrying uncertain closes. Exact disk-handle restoration,
reverse order, original-error preservation, no partial bytes and retained root
ownership are checked. Native tests must run/pass three times without skips before
claiming qualification. Cross-compilation alone is not native evidence.

## Evidence and remaining limits

At `196c88a`, Actions run `43210192859` (job `131155515781`) executed all five
contracts three times. They failed at the actual behavioral defects: successful
reads still returned 22 bytes with a nil error despite injected cleanup failure;
primary failures lost cleanup causes; error-returned handles had zero close calls
and remained in the test-owned ledger. No build failures or skips counted as RED.

The candidate now joins temporary cleanup errors, clears bytes and consumes every
valid error-returned handle. Native GREEN evidence is still pending. Windows
amd64/arm64 compilation and ordinary Windows vet are build/static evidence only.
This does not qualify other foundation cleanup paths, persistent VFS semantics,
real OS close uncertainty, or production capability enablement. An actual uncertain
close may leave a kernel object alive; no success is reported and its numeric value
is not retried. The caller-owned Root is not automatically revoked by this narrow
change. Callers needing a host-wide terminal policy must apply it separately.
