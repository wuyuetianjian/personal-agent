# Windows temporary read-handle cleanup

Scope: `Root.read` and `acquireIOGuard` only. These disconnected foundations stay
outside production. No identity, exact-name, sharing, root-authority or capability
policy changes. NewRoot and other existing cleanup paths are not qualified here.

A per-call private `handleOps` seam selects native open and close operations.
Public paths always construct real native operations; no global hook or exported
injection exists. RED only routes the old behavior through that seam: cleanup
errors remain ignored and owned handles returned together with open errors remain
unclaimed until the fix. It does not replace any default path with a refusal stub.

GREEN will register every valid returned handle before inspecting the open error.
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
