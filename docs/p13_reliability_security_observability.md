# P13 Reliability / Security / Observability Core Slice

## Scope

This P13 slice makes the local `pachat serve` process safer to operate continuously by adding a small observability foundation and focused secret-leak regression coverage.

The slice covers:

- Structured log records with stable P13 fields and centralized redaction.
- In-process runtime metrics counters and snapshots for local APIs.
- JSON health and readiness responses for `pachat serve`.
- A local `/metrics` endpoint exposing sanitized process/runtime counters.
- Secret-leak tests across structured logs, metrics labels, health/readiness responses, and API-facing sanitization.

## Behavior

Structured logging must use explicit fields instead of free-form concatenation for task, workflow, node, agent, capability, provider, duration, status, and error category metadata. Log output must redact secret-like values before serialization.

Metrics are intentionally local and dependency-free in this slice. They expose counters and gauges through an in-memory registry and `/metrics` JSON response. Metric names and labels must be sanitized before export.

`/healthz` reports process liveness only. `/readyz` reports database, migration, Runtime initialization, and dependency status separately. Dependency warnings do not automatically make the whole process unready unless a required local dependency fails.

No raw secrets, credentials, tokens, cookies, private keys, or credential-bearing URLs may be emitted through logs, metrics, health/readiness responses, or API-facing text.

## Data And Configuration

This slice adds no new persistent schema and no new required configuration.

The local HTTP server continues to read listen address and request timeout from the existing `server` configuration. Observability output uses safe defaults and stays local to the existing `pachat serve` process.

## Validation

The slice is validated with focused observability and CLI tests plus:

```sh
go test ./...
make build
```

## Non-goals

This core slice does not complete all P13 release gates. The following remain follow-up work:

- OpenTelemetry tracing for request, planning, workflow, model, RAG, MCP, browser, coding-agent, and verification spans.
- Global resource governor for concurrent workflows, model calls, browser sessions, coding agents, MCP calls, file handles, and temporary disk.
- Disk pressure monitoring and automatic cleanup/governance.
- Full graceful shutdown checkpointing across browser sessions, coding-agent processes, event flushes, and DB close ordering.
- Crash/chaos tests that kill the process during RAG, browser, coding, MCP, approval wait, workflow checkpoint, and notification delivery.
- Full race hardening under `go test -race ./...`.
- Load tests for parallel tasks, large DAGs, watchers, knowledge collections, and API readers.
- Prompt-injection defense fixtures for RAG documents, web pages, MCP results, tool output, coding-agent output, memory, events, and Skill imports.
- Shell/command injection, path traversal, SSRF/network egress, API security, external-agent sandbox, browser security, durable audit-log, dependency/vulnerability CI, golden eval suite, and measurable quality gates.
