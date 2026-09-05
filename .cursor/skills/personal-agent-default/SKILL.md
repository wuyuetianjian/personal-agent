---
name: personal-agent-default
description: Applies personal-agent project workflow rules. Use when implementing or reviewing any P0-P6 phase, changing pachat CLI behavior, memory, long-task state, storage, config, browser contracts, or project progress documentation.
---

# Personal Agent Default Workflow

## Phase Progress Records

After completing any implementation phase, update the matching local progress file:

- P0: `docs/projdocs/task/P0.md`
- P1: `docs/projdocs/task/P1.md`
- P2: `docs/projdocs/task/P2.md`
- P3: `docs/projdocs/task/P3.md`
- P4: `docs/projdocs/task/P4.md`
- P5: `docs/projdocs/task/P5.md`
- P6: `docs/projdocs/task/P6.md`

Each progress file must include:

- Status and completion date.
- Completed capabilities.
- How to use the completed functionality.
- Runtime data locations.
- Validation commands and results.
- Known boundaries and next-phase work.

`docs/` is intentionally ignored by git. Treat these files as local handoff records for future agents.

## Implementation Rules

- Follow `docs/projdocs/P0_P6_EXECUTION_PLAN.md` when choosing the next phase.
- Keep each phase focused and independently verifiable.
- Update tracked `README.md` when user-visible commands, setup, or behavior changes.
- Keep README content in both English and Chinese.
- Do not commit `docs/` or `data/`.
- Run `go test ./...` before finishing code changes.

## Current P0 Tool

The current package name is `pachat`.

Build:

```sh
make build
```

Run:

```sh
bin/pachat run --config configs/config.example.yaml --task "smoke test"
```

Interactive memory:

```sh
bin/pachat chat --config configs/config.example.yaml
```

Long-task state:

```sh
bin/pachat run --config configs/config.example.yaml --task "long local task" --long
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```
