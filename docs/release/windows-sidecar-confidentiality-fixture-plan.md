# Sidecar provenance: executable counterexample plan

Design and isolated diagnostic evidence. No production changes, relaxed broker
or new capability is introduced here. Starting evidence: main methods and metadata xAccess
have independent native GREEN; private WAL SQL and full external lifecycle do not.

## Recorded native diagnostic (not security GREEN)

At `eaff144`, run `43231058382` / job `131226158431`, the two diagnostic
contracts each passed three times. Current broker pending/closed/protected
refusals and benign neighbor reads held. Guarded native rename failed with the
specific sharing violation and succeeded after guard release. The separate
relaxed known-main-ID model returned a synthetic payload from a genuinely
renamed, single-link, fresh external WAL; registering that same retired identity
then blocked the bytes. This confirms the candidate-policy gap, NOT a flaw in
the current fail-closed broker or production readiness.

The implemented B fixture is the minimal known-main-ID model. The optional second
stock generation/current-name-scan variant below is not yet implemented or proven.

## What this experiment must distinguish

The current `persistentBroker.readWorkspace` requires all three guards, checks
their identities, and calls `Root.read` with those exclusions. Closing the broker
revokes admission; missing guards return `errPersistentPending`. Consequently,
the counterexample below is NOT an exploit of that current broker. It falsifies
the proposed replacement policy “release deletion-blocking guards, retain known
IDs, discover current sidecars, and resume ordinary reads.”

`probe_windows.go` checks the exclusion set both before IO acquisition and before
the first byte. It validates single links, exact spelling, stable identity and
metadata. None of those checks establishes a previously unknown object's storage
history. A retired, single-link WAL under an ordinary name can satisfy all of them.
`persistentOpen(..., false)` permits delete sharing; an independently opened
metadata handle is distinct from DuplicateHandle of a restrictive FILE_OBJECT.

Keep these tests separate from SQL compatibility qualification. Run only in a
synthetic temporary mixed directory, never a user's database. All markers are
generated test strings. Keep WAL below the real 256 KiB read limit so a size
refusal cannot masquerade as confidentiality. Preserve native errors verbatim.

## Fixture A: demonstrate the existing compatibility cost

1. Stock child opens a small real WAL database, disables automatic checkpoints,
   commits a unique string, proves SELECT returns it, and reports readiness.
   Parent proves main/WAL/SHM exist, then registers all current broker guards.
2. Parent proves ordinary neighbor reads succeed and registered database/sidecar
   reads return `errProtected` before data acquisition.
3. Ask the child to close normally. Check Close errors, exit code, sidecar names
   and identities. Do NOT assume stock propagates a failed DeleteFile as a Close
   error: remaining sidecars or denied retirement are observable separately.
4. Through a separate native handle, attempt an identity-verified rename/delete
   of a synthetic guarded sidecar; require an actual sharing-conflict result.
   Do not treat access-denied/setup failure as the expected sharing control.
5. Release broker guards, verify read admission now refuses, and repeat the native
   retirement control successfully. Run an unguarded fresh stock close/reopen
   control with the same SQL. A stock close that legitimately leaves sidecars is
   not alone evidence of incompatibility; the explicit retirement control matters.

Use deadlines and a child command protocol, not sleeps. Always reap the child.
Do not retrofit the production broker with a “release but keep reading” switch.

## Fixture B: unknown external WAL becomes an ordinary neighbor

Implement a separate experiment-only test harness using the *real* `Root.read`
primitive and live pinned historical IDs. It represents only the candidate
known-identity policy; it must not claim to be `persistentBroker.readWorkspace`.

1. Start from an unguarded stock-created database that has closed successfully.
   Ensure the next child will create fresh sidecars, rather than assuming it.
   Keep a real metadata pin on main and on any historical object being excluded.
2. Start an independent stock child. Commit a unique payload in WAL without
   checkpointing. Child must SELECT the payload and signal ready. Parent verifies
   WAL header, nonempty frames, identity, size below Limit, and payload present
   in WAL but absent from main. Observation for fixture assertions must NOT add
   the fresh ID to the simulated policy registry.
3. Child exits through an explicit crash command without database Close, so OS
   cleanup releases handles without SQLite deleting/checkpointing the WAL.
   Validate the expected exit code and wait for process completion. Fresh WAL
   must remain; missing WAL is setup failure, not a security pass.
4. Rename that WAL to `ordinary-notes.bin` within the same directory. Prove its
   identity is unchanged, link count is exactly one, it is non-reparse/local NTFS,
   and its ID is not in the simulated registry. No copied WAL stands in for this
   rename: copies would test a different provenance question.
5. Optionally let a second stock child reopen the original database, create a
   new WAL generation and hold it. Inventory current main/WAL/SHM identities and
   pin those too. Prove the retired neighbor still has an excluded-set miss.
   This variant falsifies “scan current canonical sidecars before every read.”
6. Invoke real `root.read(ctx, ["ordinary-notes.bin"], pinnedExclusions)`.
   Successful return containing the unique payload is the diagnostic finding.
   A generic IO refusal is INCONCLUSIVE, not a security success. Independently
   read a benign ordinary neighbor with the same primitive and require its bytes.
7. Stronger control: add a live metadata pin for the retired WAL identity to the
   exclusion set. Repeat the read and require `errProtected` and zero bytes.
   This establishes that registration, rather than parser/fixture failure, is
   the missing condition. No logs should print database contents.

This diagnostic test can PASS when it reproduces a gap. Give it an unmistakable
`SIDECAR_PROVENANCE_GAP_CONFIRMED` marker and separate workflow/artifact. It must
never count toward production security GREEN. A later security contract reverses
the assertion for the proposed new mechanism: deterministic protected refusal
before bytes, while ordinary neighbor reads still succeed.

The crash/rename schedule removes probabilistic races. No claim of a mathematical
impossibility follows. It is a concrete counterexample to specific policies. SHM
can be renamed similarly but does not necessarily contain row payload; WAL is
the smallest useful confidentiality witness. Rollback/super-journal families need
their own later fixtures, not an inference from a filename suffix.

## Candidate mechanisms and precise scope

**Current bounded user-mode authority:** retain guards and require all external
sidecar creators to cooperate with admission. This can protect registered objects
and execute held-reader experiments. Unmodified independently launched stock
processes do not participate. A launcher protocol is not enforcement against
another same-user process; calling it full compatibility would change the threat
model. Keep this boundary experimental, not a solution to Fixture B.

**Current-name scan, directory notifications, historical IDs, content magic:**
Fixture B tests their missing provenance directly. Notifications can be delayed
or overflow; current scans lose retired names. Bare persisted file IDs risk reuse.
Rejecting every SQLite-looking neighbor changes neighbor semantics and misses
arbitrary payload-bearing journals; it is not an identity security boundary.
USN journal reconciliation is a research candidate, not an accepted substitute:
it must prove record availability before tool admission, rename/hardlink history,
journal loss/wrap handling, baseline completeness and correctness with open
writers. “Eventually caught up” is insufficient. Do not build this assumption
into the private VFS.

**Synchronous filesystem mediation:** a signed minifilter or equivalent trusted
OS mediator is a possible mechanism that does not require changing database
format, ACLs, accounts, paths or benign neighbor access. It would have to label
sidecar identity before external create/rename completion, preserve provenance
across retirement and hardlinks, and interlock tool admissions with transitions.
It must handle pre-existing writable handles and mapped writes, not only
CreateFile; persistent history and service downtime remain requirements.
This is not proven feasible here or a drop-in patch. An ordinary user-mode watcher
cannot be described as equivalent. Same-user administrative attackers able to
unload the mediator are outside any such enforcement unless separately constrained.

## Next actionable decision and cost

First implement Fixtures A/B in an independent experimental file/runner, with
stock-child controls and exact event validation. No user decision is needed for
synthetic tests within the existing scope. Keep private WAL/SHM work progressing
separately; solving SQL interoperability does not solve provenance.

If Fixture B confirms the gap, the current constraints have no established
transparent user-mode solution in this codebase. Before production, choices
requiring user direction include a signed system mediator (administrator install,
driver signing/deployment/support, possible restart, new security-sensitive
software approval) or an explicitly narrower external-process trust contract
(all database tools launched through a coordinator, no protection against other
same-user creators). Neither is silently authorized by this design. Moving old
data, changing accounts/ACLs, blocking mixed-directory neighbors, or changing WAL
mode remain excluded options, not automatic fallbacks.

Any new mediator also needs an explicit protection-start baseline. Installing it
cannot infer that an arbitrary already-retired neighbor previously held storage
content. Enumerating current roles is a starting inventory, not retroactive
provenance. Record this scope rather than claiming old mixed directories were
fully classified without evidence.
