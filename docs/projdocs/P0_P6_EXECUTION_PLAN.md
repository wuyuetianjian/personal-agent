# P0-P6 Execution Plan

This file converts the project documents into an implementation sequence for this repository. It is a local planning artifact under ignored `docs/`, so code commits remain focused on tracked source files.

## How To Use This Tool

The current runnable tool is the P0 CLI entrypoint. The packaged binary name is `pachat`:

```sh
go run ./cmd/pachat run --config configs/config.example.yaml --task "smoke test"
```

What it does today:

- Loads and validates `configs/config.example.yaml`.
- Opens the configured SQLite database.
- Applies embedded migrations from `internal/storage/migrations`.
- Creates one task row in the `tasks` table.
- Marks that task as completed with a no-op answer.
- Prints the task ID, status, and answer.
- Supports an interactive chat shell that stores local episodic memory.
- Supports long-task status commands for list/show/cancel.
- Includes a P4 library-level governed browser runtime boundary backed by chromedp.

Expected output shape:

```text
task_id=task_<generated_id> status=completed answer="No-op task completed."
```

The default config stores local runtime data under `./data`, including `./data/personal-agent.db`. The `data/` directory is ignored by git.

Use a custom task:

```sh
go run ./cmd/pachat run --config configs/config.example.yaml --task "summarize my local notes"
```

Use a custom config:

```sh
go run ./cmd/pachat run --config /path/to/config.yaml --task "your task"
```

Use interactive chat with local memory:

```sh
go run ./cmd/pachat chat --config configs/config.example.yaml
```

Inside chat:

- Type normal text to persist a user message and a local no-op assistant response.
- Use `/memory` to show recent episodic memory.
- Use `/exit` or `/quit` to leave.

Build the package:

```sh
make build
bin/pachat run --config configs/config.example.yaml --task "smoke test"
bin/pachat chat --config configs/config.example.yaml
```

Long-task state commands:

```sh
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```

Current limitation:

- P0 remains the CLI runner with local task state and episodic memory. It does not yet call models, run RAG, automate the browser from the CLI, verify claims, or execute Sub-Agents.
- P4 browser runtime code is available as a library boundary, but it is not wired into CLI task execution yet.
- Those capabilities are planned for P1-P6 below.

Development workflow:

1. Pick the next phase from this file.
2. Implement only that phase's deliverables.
3. Run the validation commands listed for the phase.
4. Commit tracked source/config/README changes.
5. Keep `docs/` and `data/` local because both are ignored by git.

## P0: Foundation

Goal: make the repository runnable and testable as a local-first agent shell.

Deliverables:

- `configs/config.example.yaml` copied from the project spec and kept free of secrets.
- `internal/config` with YAML loading, environment indirection, duration parsing, and validation.
- `internal/storage` with SQLite opening, a mockable PostgreSQL connector boundary, embedded migrations, and a migration runner.
- Initial migrations for tasks, task nodes, evidence, claims, memory, documents, model usage, permission decisions, and browser session metadata.
- `internal/cli` and `cmd/pachat` with `pachat run --config ... --task ...`.
- `pachat chat --config ...` interactive shell with local episodic memory.
- `pachat task list/show/cancel --config ...` long-task state commands.
- No-op task execution that persists a completed task row.
- Tests for config validation, migration smoke, CLI startup smoke, chat smoke, memory persistence, and task status commands.
- README updates in English and Chinese.

Validation:

- `go test ./...`
- `go run ./cmd/pachat run --config configs/config.example.yaml --task "smoke test"`
- `go test ./...`
- `make build`

Commit boundary:

- One commit: `pachat` package, interactive P0 memory, and long-task state commands.

## P1: Model And Privacy Foundation

Goal: make model selection explicit and make public model calls fail closed without privacy filtering.

Deliverables:

- `internal/model` trust levels, capabilities, provider metadata, model registry, model policy, and mockable provider interfaces.
- OpenAI-compatible provider boundary with mockable transport.
- `internal/privacy` HMAC-SHA256 pseudonymization, redaction, credential dump blocking, and audit metadata.
- Public provider enforcement that requires Privacy Gateway.

Validation:

- HMAC stability tests.
- Redaction and blocking fixtures.
- Model capability/trust selection tests.
- Public provider bypass prevention tests.

Commit boundary:

- Split into registry/policy, privacy gateway, and provider enforcement commits if large.

## P2: RAG And Memory

Goal: provide local-first retrieval and three memory stores with traceable evidence.

Deliverables:

- `internal/rag` document/chunk model, chunker, local BM25, Qdrant client wrapper, RRF fusion, reranker interface, and context compressor.
- `internal/memory` working, episodic, and semantic memory stores.
- Semantic memory indexing hook into RAG.
- Evidence-preserving compression and retrieval result packaging.

Validation:

- BM25 ranking test.
- RRF deterministic ordering test.
- Qdrant integration test skipped cleanly when unavailable.
- Memory lifecycle tests.
- Context compression evidence preservation test.

Commit boundary:

- Separate commits for document/chunk storage, BM25/RRF, Qdrant boundary, memory stores, and compressor.

## P3: Orchestration And Parallel Sub-Agents

Goal: execute bounded work through a cancellable task DAG.

Deliverables:

- `internal/orchestrator` DAG nodes, validation, dependency-aware scheduler, retry policy, early stop, and cancellation propagation.
- `internal/agent` LeaderAgent/SubAgent contracts, roles, structured results, claims, evidence IDs, usage, and error categories.
- Evidence Bus integration for node lifecycle events.

Validation:

- DAG cycle rejection.
- Ready-node calculation.
- Parallel scheduling overlap test.
- Cancellation propagation test.
- Early stop test.
- Evidence ordering and persistence tests.

Commit boundary:

- Separate commits for DAG, SubAgent contracts, scheduler, and early stop/evidence integration.

## P4: Browser Runtime

Goal: connect existing browser contracts to a real local runtime while preserving privacy and permission boundaries.

Status: completed locally on 2026-09-06. See `docs/projdocs/task/P4.md`.

Deliverables:

- `internal/browser/runtime_adapter.go`
- `internal/browser/chromedp_adapter.go`
- `internal/browser/profile_store.go`
- `internal/browser/locator.go`
- `internal/browser/a11y.go`
- `internal/browser/screenshot.go`
- `internal/browser/coordinate_fallback.go`
- `internal/browser/confirmation.go`
- Local test pages under `testdata/browser`.
- Browser evidence publishing to global Evidence Bus.

Validation:

- Runtime adapter mock tests.
- Local browser E2E tests.
- Session reuse policy tests.
- Coordinate fallback tests.
- Domain allowlist denial tests.

Commit boundary:

- Separate commits for runtime adapter, semantic locator, screenshot/A11y reads, input actions, and confirmation/evidence integration.

## P5: Permissions, Verification, And Cost

Goal: enforce side-effect policy, verify claims, and account for model/runtime usage.

Deliverables:

- `internal/permission` global policy, decisions, confirmation workflow, evaluator, and audit records.
- `internal/verification` claim extraction, claim/evidence coverage, conflict detection, and confidence policy.
- `internal/cost` budgets, usage records, pricing lookup from config/registry, soft and hard limit enforcement.

Validation:

- Permission decision tests.
- Confirmation flow integration test.
- Claim/evidence coverage tests.
- Conflict fixture tests.
- Budget soft/hard limit tests.

Commit boundary:

- Separate commits for permission layer, verification, and cost accounting.

## P6: API, E2E, And Hardening

Goal: expose usable APIs and prove the integrated local-first flow works.

Deliverables:

- `internal/api` REST task create/status/cancel/events and confirmation approve/deny endpoints.
- Full local E2E using provider mocks, SQLite, local test pages, RAG, memory, browser, verification, and final synthesis.
- Optional PostgreSQL mode integration tests.
- Secret-leak regression fixtures and operational hardening.

Validation:

- REST API tests.
- CLI E2E test.
- Full agent E2E with mocks.
- Browser high-risk denial E2E.
- Logs/evidence secret-leak regression tests.
- PostgreSQL integration tests skipped cleanly when unavailable.

Commit boundary:

- Separate commits for API surface, confirmation endpoints, full E2E, and hardening.
