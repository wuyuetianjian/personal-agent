# P12 Operator Experience Core Slice

## Scope

This P12 slice adds local operator commands so a new user can initialize, validate, inspect, serve, and maintain a local `pachat` installation without editing Go code or manually creating the database.

The slice covers:

- `pachat init` for generating a mountable local config and creating the data directory.
- `config_version` support with strict unknown-field detection and operator validation output.
- `pachat config validate` for syntax, schema, model/provider, storage, path, permission, and coding-agent checks.
- `pachat doctor` for local health diagnostics with `OK`, `WARN`, and `FAIL` statuses.
- Knowledge, project, approval, task, and workflow operator commands with concise and JSON output where supported.
- `pachat serve --config ...` for a packaged local HTTP server with health and readiness endpoints.
- Backup, restore dry-run, data retention cleanup, and SQLite maintenance commands.

## Behavior

Operator commands must use the same configuration loader, storage migrations, Runtime builder, and local stores as the existing CLI/API paths. Commands must fail closed when backing functionality is not yet persistent instead of pretending an external service or pending approval exists.

`pachat init` writes a valid config file containing environment-variable references for secrets. It must not print generated secret values or credentials.

`pachat config validate` reports warnings for legacy config files that omit `config_version`, and fails on unsupported schema versions or unknown fields.

`pachat doctor` checks database/migrations, filesystem writability, privacy secret availability, configured model references, browser screenshot directory, configured coding-agent binaries, and basic disk availability. External model/MCP reachability is reported conservatively without automatic downloads or credential exposure.

Knowledge commands index local files into the existing SQLite RAG document/chunk tables. Project commands use the existing project table. Approval commands expose the current persistent boundary and report that no persistent approval inbox exists when no stored requests are available.

Backup commands create a zip archive containing the SQLite database, sanitized config, and metadata. Restore protects existing data by default and supports dry-run validation before extraction.

## Data And Configuration

The canonical schema version for this slice is:

```yaml
config_version: 1
```

No raw secrets are added to config files, backup metadata, logs, JSON output, or doctor output. Secret values remain referenced through environment variables.

The slice reuses:

- `tasks`
- `documents`
- `document_chunks`
- `projects`
- `workflow_runs`
- `workflow_nodes`
- `workflow_checkpoints`
- `events`
- `permission_decisions`

## Validation

The slice is validated with focused CLI/config/storage tests plus:

```sh
go test ./...
make build
make smoke
```

## Non-goals

This slice does not complete the full P12 web/product surface. The following remain follow-up P12 work:

- Full local Web Dashboard.
- SSE-backed web live events.
- Persistent approval request inbox integrated with Runtime confirmations.
- Real Ollama/MCP/Codex/Claude process health execution beyond local configuration and binary checks.
- Automatic model downloads.
- Filesystem watcher daemon for incremental knowledge indexing.
