# P10 Core Release Slice

## Scope

This increment implements the first P10 proactive foundation on top of P9 persistent workflows:

- trigger core types for one-shot, interval, cron, event, condition watch, manual, and goal triggers;
- trigger validation for schedule, event, and condition requirements;
- SQLite-backed trigger and trigger state persistence;
- deterministic next-fire calculation for one-shot, interval, and simple cron schedules;
- trigger deduplication keys, debounce, cooldown, jitter, and exponential backoff helpers;
- an untrusted event envelope, in-memory event bus, SQLite event store, and event deduplication;
- focused tests for validation, persistence, schedule behavior, noise controls, event bus/store, and migrations;
- README and task archive updates.

## Design

`internal/trigger` owns proactive trigger definitions and state. The model is declarative: a trigger may reference a workflow template or skill, but it does not bypass project policy, capability resolution, permissions, privacy, budgets, evidence, or verification. Disabled triggers are never eligible to fire.

The scheduler helpers are deterministic and restart-safe. One-shot schedules fire exactly once and are disabled after success. Interval schedules use fixed delay from the previous successful completion or last fire time to avoid overlapping long work. Cron support is intentionally small for this slice: minute-level five-field expressions with numeric values or `*`, evaluated in the schedule timezone.

Noise controls are pure helpers so they can be reused by CLI, API, watcher, or future daemon code. Deduplication uses trigger ID, scheduled window, and event hash. Debounce and cooldown use persisted trigger state rather than process-local memory. Backoff uses consecutive failure count with capped exponential delay.

`internal/event` treats event payloads as untrusted observations. The bus validates and stores events but does not execute actions. The store persists payload bytes, privacy class, trust level, dedup key, correlation ID, causation ID, and event depth so later P10 workflow chaining can enforce recursion limits.

## Persistence

Migration `0003_p10_core.sql` adds:

- `triggers`
- `trigger_state`
- `events`

JSON fields are stored as serialized text to stay portable across the existing SQLite boundary.

## Validation

The slice is complete when the following pass:

```sh
go test ./...
make build
make smoke
```

## Boundaries

This slice does not add a background proactive daemon, workflow creation, filesystem watching, notification delivery, goal planning, trigger CLI/API commands, or high-risk action execution. Those remain follow-up P10 waves and should reuse the trigger/event stores added here.
