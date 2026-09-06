# P6 API, E2E, And Hardening Requirements

## Scope

P6 exposes the local-first task state through a small REST API and verifies that the P1-P5 library boundaries can participate in one integrated local flow. It does not introduce production model execution, public network calls, or CLI-driven browser automation.

## API Behavior

- Provide an `internal/api` package with an `http.Handler`.
- Support task create, list, status, cancel, and event listing endpoints.
- Support confirmation inspect, approve, and deny endpoints.
- Return JSON responses and stable HTTP status codes.
- Store task state in the existing SQLite-backed `storage.DB`.
- Keep confirmation state in the existing in-memory confirmation store boundary so later persistent wiring can replace it.

## Integrated Local Flow

- Use provider mocks for model calls.
- Use SQLite for task, memory, and RAG storage.
- Use local RAG indexing/retrieval and semantic/episodic memory writes.
- Verify final claims against explicit local evidence.
- Exercise cost estimation and budget enforcement without live metering.
- Exercise browser high-risk denial through the governed browser permission boundary.

## Hardening

- Add regression coverage that prevents secrets from leaking through API responses, model payload filtering, browser evidence, and logs/evidence-like text.
- Keep PostgreSQL integration optional and skipped when no test DSN is configured.
- Keep all URLs, DSNs, secrets, and runtime paths configurable through existing config/test inputs.

## Validation

- `go test ./...`
- `make build`
- `make smoke`

