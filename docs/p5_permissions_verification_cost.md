# P5 Permissions, Verification, And Cost

## Scope

P5 adds library-level foundations for global side-effect permissions, claim verification, and usage accounting. It does not wire these packages into CLI task execution or REST APIs; that integration remains P6 scope.

## Requirements

- Add `internal/permission` for global policy evaluation across file system, network, browser read/write, high-risk browser actions, memory writes, public model calls, credential access, and external side effects.
- Permission policies must deny high-risk actions by default and support confirmation requests containing action, target, risk, evidence IDs, and the exact proposed effect.
- Permission decisions must be represented as audit records that can later be persisted as evidence.
- Add `internal/verification` for claim extraction, claim-to-evidence coverage, conflict detection, and confidence policy evaluation.
- Verification must fail claims without required evidence coverage and flag conflicts between claims and evidence.
- Add `internal/cost` for budget definitions, usage records, model pricing lookup, and soft/hard budget limit enforcement.
- Pricing must come from model registry/config metadata supplied by callers, not hardcoded provider constants.

## Validation

- Permission decision tests cover default denial, allow policy, confirmation requirements, and confirmation approval.
- Verification tests cover claim extraction, evidence coverage, conflict fixtures, and confidence policy decisions.
- Cost tests cover model price lookup, usage estimation, soft limit warnings, and hard limit blocking.

## Boundaries

- P5 packages are intentionally dependency-light library boundaries.
- Storage-backed audit persistence, REST confirmation endpoints, CLI-connected verification, and end-to-end budget enforcement are deferred to P6.
