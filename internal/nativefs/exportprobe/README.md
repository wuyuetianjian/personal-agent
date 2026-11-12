# Export capability experiment

Only builds with `nativeprobe_export`. No production entry point, filesystem I/O,
SQLite linkage, actual user content, native-tool enablement, or approval UI.
All constructors/methods/types are private. Test code is the trusted issuer;
this is not a boundary against malicious code in the same Go package or unsafe.

`qualify` means a candidate snapshot was admitted to a bounded registry. It does
not establish provenance or grant permission. `authorize` is a separate trusted
fixture decision, bound to an opaque authority instance and one immutable byte
snapshot. Equal textual purposes, copied handle structs, external JSON claims,
and another registry's references do not carry that authority. SHA-256 records
snapshot identity only, never a safety verdict. The actual authorization predicate is exact pointer membership in this registry
and the exact opaque authority instance. Epoch, nonce and generation are internal
issuance records, not a validated serialized bearer-token protocol. There is no
restoration/deserialization or process-restart authorization.

Fixed bounds: 16 authorities, 32 candidates, 64 lifetime grants/tombstones,
64 KiB per snapshot, 1 MiB stored bytes, 8 concurrent reads (at most 512 KiB of
inflight result copies). Issuance slots are not recycled; close is terminal.
All committed copies are outside these quotas, including copies that have not
yet returned to their caller. A read slot is released at commit, not delivery.
Expiry is checked at admission and commit. Entropy failures/collisions refuse.

Revoke and read commit share a mutex. Revoke waits admitted work to commit/abort,
not for a receiver or network to acknowledge delivery. A previously committed
read may return after revoke completes. Waiting cancellation does not undo
revocation or force-free an active snapshot. Close revokes everything and waits;
a timed-out close can later finish draining, but cannot reopen the registry.
No user/network delivery retraction is promised.

TDD evidence: the initial refusal scaffold compiled, five behavior tests failed
at the unimplemented constructor, and the disconnected-production contract passed.
That is constructor-level RED, not evidence that later assertions executed then.
The final six contracts pass locally, including three race-enabled repetitions,
with cancellation/expiry at commit, revoke/close draining, copy isolation, quotas,
forged/cross-authority references, and already-committed late return. Independent
review and publication are separate. None of these tests qualifies a native OS
reader or solves global confidentiality for unknown historical derived files.

Entropy and clock functions are trusted injected dependencies called under the
registry mutex. The experiment does not promise bounded progress for arbitrary
blocking or panicking dependency callbacks. No such callbacks accept tool input.
