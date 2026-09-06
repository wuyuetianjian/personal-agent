# P3 Orchestration And Parallel Sub-Agents

Archived stage record: `docs/projdocs/task/P3.md`.

## Scope

P3 adds the library-level orchestration foundation for bounded, cancellable, dependency-aware task execution. It does not change the CLI execution path, which remains a local no-op runner until later API and E2E phases wire the runtime together.

## Requirements

- Add DAG node validation that rejects duplicate node IDs, missing dependencies, self-dependencies, and cycles.
- Add dependency-aware ready-node calculation for pending nodes.
- Add a scheduler that executes ready nodes in parallel through role-specific Sub-Agent contracts.
- Add retry policy support for retryable failures, with max attempts carried by each node.
- Add early stop support so callers can stop scheduling once enough results have been collected.
- Propagate context cancellation to all running Sub-Agent executions.
- Extend agent contracts with task-scoped nodes, retry metadata, structured errors, claims, evidence IDs, usage, and node metadata.
- Add an Evidence Bus boundary for ordered node lifecycle events, including ready, started, retrying, completed, failed, cancelled, and early stop events.

## Design

The `internal/agent` package keeps ownership of Leader and Sub-Agent contracts. `TaskNode` now includes task ID, dependency IDs, timeout, max attempts, and metadata so a node can be scheduled independently without inheriting Leader model settings. `Result` continues to carry claims, evidence IDs, and usage, while error categories use the existing typed constants.

The new `internal/orchestrator` package owns DAG validation, ready-node calculation, and scheduling. A DAG is an in-memory set of `agent.TaskNode` values. Validation builds dependency and reverse-dependency maps, checks all references, and runs cycle detection before scheduling begins.

The scheduler maintains node state internally. It starts all ready pending nodes up to the configured parallelism limit, executes each node with the Sub-Agent registered for its role, retries retryable errors while attempts remain, and marks dependents ready only after every dependency completes. Cancellation comes from the caller context and is passed into every Sub-Agent call. Per-node timeouts are applied only when a node configures a positive timeout.

Evidence Bus integration is a narrow interface so later storage-backed evidence can reuse the same lifecycle events. P3 uses an in-memory bus implementation for deterministic tests and stores events in publish order.

## Validation

- DAG validation rejects cycles.
- Ready-node calculation returns only pending nodes whose dependencies completed.
- Parallel scheduling proves independent nodes overlap.
- Cancellation propagates to running Sub-Agents.
- Early stop prevents additional scheduling once the policy returns true.
- Evidence events preserve publish ordering and include lifecycle records for persisted node outcomes.
