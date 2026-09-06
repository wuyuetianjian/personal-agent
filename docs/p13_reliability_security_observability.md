# P13 Reliability / Security / Observability Release Gate

## Scope

P13 makes the local `pachat` process safer to leave running continuously. The first P13 slice added observability foundations. This document now covers the remaining P13 release-gate scope from `docs/projdocs/P0_P14_EXECUTION_PLAN.md`.

The release gate covers:

- Structured log records with stable P13 fields and centralized redaction.
- In-process runtime metrics counters and snapshots for local APIs.
- Local tracing spans for request, workflow, model, RAG, MCP, browser, coding-agent, and verification boundaries with sensitive attributes disabled by default.
- JSON health and readiness responses for `pachat serve`.
- A local `/metrics` endpoint exposing sanitized process/runtime counters.
- Global resource limits for workflows, model calls, browser sessions, coding agents, MCP calls, open files, and temporary disk.
- Disk pressure checks for database, screenshots, worktrees, logs, vector storage, and backup storage.
- Graceful shutdown coordination that stops new work, checkpoints workflows, flushes events, closes browser/coding resources, and closes storage.
- Crash, chaos, race, and load regression fixtures that can run locally without external services.
- Prompt-injection and secret-leak defense fixtures across untrusted observations and API-facing output.
- Shell command, filesystem path, and network egress policy helpers.
- API security middleware for auth, CORS, request body limits, rate limiting, sanitized errors, and security headers.
- External coding-agent sandbox policy checks.
- Browser security policy regression helpers for navigation, downloads, uploads, credential pages, popups, and OAuth.
- Durable security audit log records for permission, approval, escalation, public transfer, write, deploy, config, and trigger changes.
- Dependency/vulnerability quality command documentation and measurable golden-eval quality gates.

## Behavior

Structured logging must use explicit fields instead of free-form concatenation for task, workflow, node, agent, capability, provider, duration, status, and error category metadata. Log output must redact secret-like values before serialization.

Metrics are intentionally local and dependency-free in this slice. They expose counters and gauges through an in-memory registry and `/metrics` JSON response. Metric names and labels must be sanitized before export.

`/healthz` reports process liveness only. `/readyz` reports database, migration, Runtime initialization, resource governor, disk pressure, and dependency status separately. Dependency warnings do not automatically make the whole process unready unless a required local dependency fails.

No raw secrets, credentials, tokens, cookies, private keys, or credential-bearing URLs may be emitted through logs, metrics, health/readiness responses, or API-facing text.

API security is deny-by-default when a token is configured: unsafe methods must supply the configured bearer token, browser CORS origins must match configuration, request bodies are capped, clients are rate limited, errors remain code-only, and common browser security headers are emitted.

Command, filesystem, and network helpers must classify adversarial input before execution or access. Commands are represented as argv arrays and reject shell metacharacter input for non-shell commands. Paths must resolve under an allowed root. Network egress rejects unsupported schemes, userinfo credentials, private hosts by default, and redirects outside policy.

Untrusted content from RAG, browser pages, MCP, tools, coding agents, memory, events, and Skill imports must remain observation data. Prompt-injection fixtures should detect attempts to override policy, reveal secrets, ignore instructions, or exfiltrate data.

Durable audit records must be append-only at the API boundary, redacted before persistence, and queryable by actor, action, target, status, and time.

## Data And Configuration

This release gate adds one migration for durable security audit records.

The local HTTP server continues to read listen address and request timeout from the existing `server` configuration. Security and reliability settings have safe zero-value defaults. Operators may tighten them through service configuration without rebuilding:

- `security.api.auth_token_env`
- `security.api.allowed_origins`
- `security.api.max_body_bytes`
- `security.api.rate_limit_per_minute`
- `security.network.allowed_domains`
- `security.network.allow_private_networks`
- `reliability.resources.*`
- `reliability.disk.*`

## Validation

The release gate is validated with focused unit and integration tests plus:

```sh
go test ./...
make build
```

## Non-goals

P13 does not claim production-grade OS-level sandboxing, distributed tracing export, external dependency scanning services, or browser/kernel isolation. It provides local policy boundaries, persistent auditability, regression fixtures, and quality gates that later phases can wire into deeper integrations.
