# P11 Runtime Integration Closure Slice

## Scope

This P11 slice closes the first runtime gap after P10: persisted workflow runs can now be dispatched by the Runtime instead of remaining storage-only records.

The slice covers:

- A Runtime-owned workflow engine that loads persisted workflow runs and recoverable nodes.
- Project policy checks before node dispatch.
- Capability registry checks before node dispatch.
- Runtime-level idempotency by skipping completed checkpointed nodes during recovery.
- Context cancellation propagation through the existing orchestrator scheduler.
- Budget preflight checks for node execution.
- Per-node checkpoints with evidence IDs and usage JSON.
- Workflow completion, failure, and cancellation status updates.

## Behavior

The workflow engine executes only nodes that are not terminal in `workflow_nodes`. Completed, failed, skipped, and cancelled nodes are not replayed during recovery.

For each recoverable node:

1. Load the workflow run.
2. Load the project context when `project_id` is set.
3. Confirm the node capability exists, is enabled, and is allowed by project policy.
4. Check the configured per-run budget from workflow input fields.
5. Dispatch through the existing `orchestrator.Scheduler`.
6. Persist a checkpoint after each completed local node.
7. Mark the workflow completed when all recoverable nodes complete.

The initial executable node types are local read-only runtime capabilities:

- `memory.search`
- `rag.search`
- `verification.verify`

Unsupported capabilities fail closed instead of executing a partial side effect.

## Data And Configuration

No new configuration file values are introduced in this slice.

The engine reuses:

- `workflow_runs`
- `workflow_nodes`
- `workflow_checkpoints`
- `projects`
- `evidence`

Budget preflight values are read from workflow input JSON when present:

```json
{
  "input": "user task",
  "max_input_tokens": 4000,
  "max_output_tokens": 2000,
  "max_cost_usd": 0.25
}
```

Missing budget fields mean no explicit limit for that dimension.

## Validation

The slice is validated with:

- Unit tests for workflow execution and checkpoint persistence.
- Unit tests proving restart recovery skips completed nodes.
- Unit tests for project policy denial.
- Existing repository tests with `go test ./...`.
- Build and smoke validation through `make build` and `make smoke`.

## Non-goals

This slice does not complete all P11 release gates. The following remain follow-up P11 work:

- Model-backed Leader planning.
- Full Scheduler replacement for the existing direct `Runtime.Run` fast path.
- BrowserAgent execution from user-facing commands.
- CodingAgent runtime dispatch from user-facing commands.
- Real MCP transport execution.
- Public model escalation.
- Live provider-reported usage accounting.
- Runtime-backed multi-turn chat.
