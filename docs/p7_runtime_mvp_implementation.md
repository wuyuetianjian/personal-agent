# P7 Runtime MVP Implementation

## Scope

This implementation completes the first P7 vertical slice by replacing no-op task execution with a unified local-first runtime path. The runtime connects existing P0-P6 storage, RAG, memory, orchestration evidence events, verification, and task persistence components.

## Runtime Behavior

- `pachat run` creates a task row, executes `runtime.Run`, and persists the final answer, confidence, or failure state.
- `POST /tasks` can execute the same runtime path synchronously for normal tasks while preserving the existing running-state behavior for `long` task creation.
- Runtime routing is local-first:
  - Memory and local RAG are queried before any model escalation.
  - The initial MVP does not call public models.
  - Local evidence is converted into verification evidence and cited in the final answer.
- Runtime emits task and node events through the existing orchestrator evidence bus.
- Verification gates the answer using the existing `internal/verification` policy and report structure.

## Data And Configuration

- No new environment-specific values are hardcoded.
- No migration is required because the existing `evidence` table and task columns already support P7 MVP persistence.
- The runtime builder uses the configured SQLite database, configured leader model ID, and existing local defaults.

## Validation

The P7 MVP should be validated with:

```sh
go test ./...
make build
make smoke
```

Expected CLI output no longer contains `No-op task completed.`. It includes task status, confidence, remote token usage, and a synthesized local-first answer.

## Remaining P7 Increments

- Full model-backed Leader planning with structured DAG JSON.
- Browser Sub-Agent wiring into CLI/API runtime execution.
- Background task runner with cancel-function tracking for API long tasks.
- Public model escalation through forced Privacy Gateway context building.
- Live model usage accounting for production provider calls.
