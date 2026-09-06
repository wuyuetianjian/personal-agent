# P9 Core Release Slice

## Scope

This increment implements the first usable P9 slice on top of P0-P8 (completed locally on 2026-09-06):

- a unified Capability model, registry, health state, and deterministic policy resolver;
- versioned Skill manifests with validation and skill-first matching;
- persistent Workflow and Workflow Node state with checkpoint, recovery, pause, resume, and cancel transitions;
- Project context and policy checks for privacy, skills, capabilities, and coding agents;
- read-only MCP server/tool metadata and capability adaptation without external network dependencies;
- SQLite migrations and focused unit tests for the new boundaries;
- operator CLI commands for capability and workflow inspection;
- README and task archive updates.

## Design

`internal/capability` owns discovery and deterministic selection. Capability candidates are filtered by enablement, health, project allowlists, privacy class, trust, tags, and side-effect level before being scored. Existing memory, RAG, browser, verification, and configured Codex/Claude backends are represented as capabilities; the resolver does not execute them.

`internal/skill` treats a Skill as a versioned, validated workflow declaration rather than a prompt. Skill validation rejects unknown capabilities, invalid dependencies, inactive statuses, and permission/trust escalation. Matching is deterministic and records enough metadata for a later persistent execution layer.

`internal/workflow` persists lifecycle state independently from the existing in-process DAG scheduler. Node completion is only valid after a checkpoint is written. Recovery returns only non-terminal work and never automatically replays a completed side-effect node. Pause, resume, and cancel are explicit state transitions.

`internal/project` supplies project-scoped policy. It is consulted by capability resolution and skill matching, while the existing permission/privacy packages remain the enforcement owners for actual side effects and model calls.

`internal/mcp` is metadata-only in this slice: a registry can expose read-only server/tool definitions and adapt them to capabilities. MCP results remain untrusted observations; no command execution or credential loading is added.

## Persistence

Migration `0002_p9_core.sql` adds `projects`, `skills`, `skill_versions`, `workflow_runs`, `workflow_nodes`, and `workflow_checkpoints`. JSON fields are stored as serialized values so the schema remains portable across the existing SQLite boundary.

## Validation

The slice is complete when the following pass:

```sh
go test ./...
make build
make smoke
```

The task archive records the exact implemented boundary and known follow-up work.
