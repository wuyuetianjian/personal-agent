# P14 v1.0 GA Release Requirements

P14 turns the local-first Personal Agent into a distributable v1.0 package for macOS Apple Silicon workstations and Linux servers. This document extends `docs/p14_pre_completion_requirements.md`; it does not add new Runtime architecture.

## Scope

- Define release metadata for application, config schema, database schema, Skill manifest schema, and API version.
- Build release archives for `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Record version, commit, build date, Go version, and SHA256 checksums.
- Provide bootstrap install and upgrade scripts that create config/data directories, validate config, back up data, run migrations, and support health checks.
- Provide systemd and macOS LaunchAgent examples with local data/log paths and conservative service limits.
- Provide safe production config examples without inline secrets.
- Set the default local Ollama model in `configs/config.example.yaml` to the locally available `qwen3.8:27b-mlx` model.
- Wire configured OpenAI-compatible local providers into the Runtime chat path so the interactive CLI uses the selected local model.
- Wire the configured Leader OpenAI-compatible provider into `BoundedModelPlanner` when `agent.planner.enabled` is true and the selected model supports `chat` and `json_schema`.
- Provide quickstart, user, administrator, security/threat model, troubleshooting, upgrade, RC E2E, soak, performance, and recovery drill documentation.
- Provide a release checklist command that verifies tests, vet, build, build matrix, checksums, and required release documents.
- Add migration compatibility coverage for upgrading from a supported P13/P14-pre schema state to the latest embedded migrations.

## Non-Goals

- Package manager publishing.
- Windows release support.
- External CI provider configuration.
- Production-grade kernel or browser isolation beyond documented service hardening examples.

## Functional GA Runtime Brain Slice

`BLOCK-01` closes the first Functional GA blocker by composing the configured local Leader model into the bounded workflow planner:

```text
Config
  -> Model Registry
  -> agent.leader.model_id
  -> OpenAI-compatible ChatProvider
  -> BoundedModelPlanner
  -> Runtime.Planner
```

The planner is enabled by `agent.planner.enabled` and bounded by `agent.planner.max_nodes`. It remains fail-closed: disabled or unavailable providers leave `Runtime.Planner` unset, unknown capabilities are rejected, disabled capabilities are denied, and invalid DAGs such as cycles fail validation before workflow creation.

`HIGH-01` and `HIGH-02` add model-backed local reasoning and synthesis on top of the same configured private provider. Reasoning receives the task, compressed evidence references, and constraints, and returns claims, a decision summary, confidence, evidence IDs, and provider usage without storing raw chain-of-thought. Synthesis receives verified evidence context and returns the final answer while excluding unsupported claims and surfacing conflicts when evidence is insufficient. If no configured private provider is available, both nodes keep the existing deterministic local fallback.

`HIGH-03` wires hybrid RAG through production config. `rag.vector` uses a configured embedding model with Qdrant search, then fuses vector and BM25 results with RRF before compression. `rag.reranker` uses a configured rerank-capable model when available. Vector and reranker failures do not fail local retrieval; the pipeline keeps BM25 fallback.

`BLOCK-02A` introduces the Runtime capability executor registry and registers governed browser executors for `browser.navigate`, `browser.read`, and `browser.write` when browser support is enabled. Browser read execution stores real browser observation evidence through the Runtime evidence store. Navigate and write actions are routed through the browser session/domain/confirmation policy boundary before any side effect.

`BLOCK-02B` and `BLOCK-02C` register governed coding executors for enabled `coding.<backend>` capabilities such as `coding.codex` and `coding.claude`. The executor accepts structured workflow input, delegates execution to the existing coding agent Runner, preserves worktree/diff/test evidence, and stores the result as Runtime evidence. Disabled backends, privacy-denied repositories, direct-write denial, or insufficient evidence fail closed.

`BLOCK-02D` and `BLOCK-02E` add governed MCP and local tool executors. MCP servers are configured under `mcp.servers`, exposed as `mcp.<server>.<tool>` capabilities, called through a stdio JSON-RPC transport, and stored as `UNTRUSTED OBSERVATION` evidence. Local tools are configured under `tools.allowlist`; workflow input is never interpolated into the command, and only the configured program/args are executed.

`BLOCK-03` composes public escalation from the model registry, public provider metadata, and Privacy Gateway. Enabled `public_remote` chat models become `Runtime.Escalator` only when the required Privacy Gateway can be constructed. Local evidence remains preferred; public escalation is invoked only when local workflow evidence is empty and policy permits it. Secret-bearing payloads are blocked before provider calls.

`BLOCK-04` adds a queryable usage report over workflow checkpoints. Runtime usage reports aggregate node usage into workflow and task totals, mark unknown usage when token/cost counters are unavailable, and use accumulated checkpoint usage during budget checks before launching later nodes. The CLI exposes this through `pachat task usage`.

`BLOCK-05` connects verification policy to scheduler early stop. When completed node claims are supported by persisted evidence, meet the configured verification policy, and have no conflicts, the scheduler cancels remaining pending work. Usage reports then stop at the completed checkpoint usage instead of growing through cancelled speculative nodes.

`BLOCK-06` verifies unified cancellation conformance across the Runtime backend boundary. Cancellation now has tests from model HTTP calls through vector HTTP, browser, MCP, and allowlisted tool executors, including MCP/tool child process cancellation. The existing coding agent process runner keeps context-backed subprocess cancellation for Codex/Claude-style backends.

`HIGH-04` connects mature read-only Skills to Runtime workflow execution. Active Skills with high match confidence and read-only capability graphs compile directly into persistent workflow nodes with `skill_id` and `skill_version`, then run through the existing scheduler. Leader planning is skipped for those matches; planner/default workflow paths remain the fallback when no eligible Skill matches.

`HIGH-05` adds a persistent background workflow worker for service mode. On startup and each poll, the worker finds unfinished runnable workflows (`pending`, `running`, and `retrying`), dispatches them with bounded concurrency through the existing `WorkflowEngine`, checkpoints through the normal scheduler path, and leaves `paused` or `waiting_approval` workflows untouched until operator action resolves them.

`HIGH-06` adds a persistent side-effect idempotency ledger. Non-read-only capabilities such as browser writes, MCP/tool mutations, coding pushes, PR creation, deploys, and message sends must claim their workflow node idempotency key before execution. Duplicate events, retries, and crash recovery see the existing claim and do not invoke the side-effect executor again.

`HIGH-07` applies Project Policy across Runtime workflow execution. Project allowlists now cover Skill matching, Leader model selection, memory/RAG capability execution, public escalation, browser/MCP/tool dispatch, and Codex/Claude backend selection. CLI runs can pass `--project`, and API task creation accepts `project_id` so persisted workflows carry the project boundary.

`HIGH-08` adds optional Coding Cross Review. When `coding_agents.cross_review.enabled` is true, Runtime coding execution asks a different enabled backend for `code_review` only when policy triggers are present: security-sensitive work, critical project work, verification failure, or diff size above `large_diff_bytes`. Simple coding tasks still run a single backend by default.

`HIGH-09` adds opt-in real Codex/Claude coding E2E coverage. The default test suite skips external CLI execution, but `PACHAT_CODING_AGENT_E2E=1` with `PACHAT_CODEX_CLI_PATH` or `PACHAT_CLAUDE_CODE_CLI_PATH` runs the governed coding Runner against a temporary git repository and validates diff/test evidence.

`HIGH-10` persists condition watcher state for proactive triggers. Condition-watch ticks now record previous state, current state, last transition time, last check time, last notification time, and cooldown in SQLite so daemon restarts preserve watcher context.

`HIGH-11` closes notification policy behavior. Notifications carry severity and delivery state, deduplicate by `dedup_key`, and can be written through quiet-hours policy so delivery is suppressed until the configured window ends.

`HIGH-12` persists bounded Goal runtime state. Goals and milestones now store status, dependencies, workflow linkage, completion criteria, budget policy, and re-evaluation timestamps, with pause/resume transitions and planner-driven re-evaluation remaining bounded by the existing iteration limit.

`HIGH-13` wires proactive triggers into the long-running service path. `pachat serve` starts the proactive daemon when `proactive.enabled` is true, recovers trigger and watcher state on startup, processes due schedules and condition-watch transitions on each poll, dispatches triggered work as pending Runtime workflows, and stops through the server context during shutdown.

`OP-01` adds persistent Skill operator commands. `pachat skill` can validate manifests, import versions into SQLite, list/show/version Skills, enable or disable active versions, and run an enabled Skill through the existing Runtime workflow path.

`OP-02` adds MCP operator commands. `pachat mcp` can list configured servers, report command availability health, enumerate configured MCP tool capabilities, and run a configured tool through the Runtime MCP workflow path for local smoke testing.

## Acceptance

- `go test ./...` passes.
- `make build` passes.
- `make smoke` passes.
- `make release-build` creates all required platform archives under `dist/`.
- `make checksums` creates `dist/SHA256SUMS`.
- `pachat version` prints all version model fields.
- `pachat release check --quick` passes in a local development checkout.
- README contains English and Chinese P14 release notes.
- `Runtime.Build()` creates both `Runtime.ChatProvider` and `Runtime.Planner` for the default `qwen3.8:27b-mlx` local planner configuration.
- Planner tests reject unknown capabilities, disabled capabilities, cycles, and node counts over `agent.planner.max_nodes`.
- Runtime workflow tests verify model-backed `reasoning.local` and `synthesis.local` execution with provider usage and final-answer propagation from the synthesis checkpoint.
- Hybrid RAG tests verify configured embedding/Qdrant wiring and BM25 fallback when Qdrant is unavailable.
- Workflow tests verify `browser.read` executes through a registered Runtime executor and persists browser evidence instead of returning the missing-executor error.
- Workflow tests verify `coding.codex` executes through a registered Runtime executor and persists diff/test evidence.
- Workflow tests verify `mcp.<server>.<tool>` and `tool.<id>` execution through registered Runtime executors with untrusted MCP output and fixed allowlisted command arguments.
- Builder tests verify public escalator wiring, missing-gateway fail-closed behavior, and secret blocking before public provider calls.
- Runtime and CLI tests verify node/workflow/task usage aggregation, unknown usage reporting, and hard-budget enforcement from accumulated checkpoint usage.
- Runtime tests verify verification-driven early stop cancels pending work and keeps usage limited to completed nodes.
- Model, RAG, and Runtime tests verify context cancellation is honored by model HTTP calls, vector HTTP calls, browser executors, MCP executors, MCP child processes, and allowlisted tool child processes.
- Runtime tests verify a high-confidence read-only Skill match compiles into a persistent workflow and does not call the Leader planner.
- Runtime and workflow tests verify the background workflow worker discovers unfinished runnable workflows and resumes them without a manual `workflow resume` command.
- Runtime and storage tests verify side-effect idempotency keys are persisted and block duplicate executor calls after retries or recovery.
- Runtime and project tests verify project policy denies disallowed capabilities, disallowed Leader models, disallowed Skill matches, disallowed coding backends, and confidential public escalation.
- Runtime tests verify Coding Cross Review runs only when configured policy triggers require it and uses a different enabled backend.
- Opt-in coding E2E tests are available for real Codex and Claude CLI binaries and skip cleanly unless explicitly enabled.
- Trigger tests verify condition-watch state persists check, transition, notification, and cooldown fields.
- Notification tests verify deduplication, severity, delivery state, and quiet-hours suppression.
- Goal tests verify persisted goals, milestone dependencies, workflow linkage, pause/resume, and bounded re-evaluation.
- Serve, trigger, and Runtime tests verify the proactive daemon starts from `pachat serve` configuration, recovers/ticks on startup, creates workflow-backed trigger work, and shuts down through context cancellation.
- CLI and storage tests verify `pachat skill list/show/import/validate/enable/disable/run/versions` against persisted Skill records and Runtime-backed Skill execution.
- CLI tests verify `pachat mcp list/health/tools/test` for configured MCP stdio servers and Runtime-backed tool execution.
