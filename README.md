# personal-agent

## English

AI agent for local user workflows.

This repository is a Go implementation of a local-first parallel personal agent. The current implementation includes the P0 foundation, P1 model/privacy foundation, P2 RAG/memory foundation, P3 orchestration/Sub-Agent foundation, P4 browser runtime foundation, P5 permissions/verification/cost foundation, P6 API/E2E/hardening foundation, P7 Runtime MVP closure, P8 External Coding Agent library foundation, P9 Capability/Skill/Workflow core closure, P10 proactive trigger/event core closure, P11 Runtime Integration closure, the P12 Operator Experience core closure, the P13 Reliability/Security/Observability core slice, and the P14 v1.0 GA release packaging slice: `pachat` CLI packaging, configuration loading, SQLite migrations, core agent/browser contracts, Runtime-backed chat, long-task state tracking with cancellable API background execution, model registry/policy contracts, OpenAI-compatible provider boundaries, a fail-closed Privacy Gateway for public remote model calls, local BM25 retrieval, optional vector/RRF/reranker hybrid retrieval with BM25 fallback, context compression, working/episodic/semantic memory stores, cancellable DAG orchestration, dependency-aware parallel scheduling, Sub-Agent retry handling, early stop, ordered node lifecycle evidence events, a governed chromedp browser runtime boundary, global permission policy evaluation, persistent confirmation inbox records, confirmation approve/deny APIs, claim/evidence verification, conflict detection, registry-backed cost accounting, local REST task APIs, full mock-based local E2E coverage, secret-leak regression fixtures, a unified workflow-backed Runtime used by CLI/API task execution, bounded model-backed Leader planning interfaces, governed External Coding Agent boundaries for Codex/Claude-style CLI backends, deterministic capability and skill registries, capability runtime metrics, skill eval activation gates, draft skill candidate generation, persistent workflow checkpoints with node dependencies, read-only MCP metadata adaptation plus governed MCP execution placeholders, persistent proactive triggers, untrusted event envelopes, event ingestion APIs, proactive daemon tick/recovery helpers, local notification inboxes, bounded goal planning, filesystem watcher scans, strict versioned config validation, local doctor diagnostics, knowledge/project/operator commands, packaged local HTTP serving, dashboard/SSE/model-discovery endpoints, backup/restore dry-run, retention cleanup, SQLite maintenance commands, structured log redaction, local runtime metrics, JSON health/readiness responses, release version metadata, cross-platform release builds, checksums, install/upgrade scripts, service templates, sample production configs, release runbooks, and context-cancellation conformance across model HTTP, browser, MCP, tool, and coding process boundaries.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, Evidence Bus records, model selection, provider enforcement, hybrid local retrieval/memory indexing, orchestration, semantic browser location, accessibility reads, screenshot capture, coordinate fallback, confirmation-gated browser execution, global permission decisions, verification gates, budget enforcement, local HTTP handler wiring, workflow-backed Runtime execution, coding agent adapter selection, safe process execution, environment sanitization, Git worktree isolation, CLI adapter request mapping, coding evidence collection, repository privacy checks, trigger scheduling helpers, trigger state persistence, event deduplication, local operator tooling, persistent approvals, notifications, dashboard event stubs, watcher scans, and bounded goal planning. It still does not include cookie reading, password reading, production-grade OS/kernel/browser isolation, external tracing export, external vulnerability scanning, or third-party notification delivery.

### Contents

- `cmd/pachat`: CLI entrypoint.
- `configs/config.example.yaml`: Local-first example configuration.
- `internal/config`: YAML loader and validator.
- `internal/storage`: SQLite storage and migrations.
- `internal/memory`: Working, episodic, and semantic memory stores.
- `internal/rag`: Document/chunk storage, chunking, BM25 retrieval, RRF fusion, Qdrant wrapper, reranker boundary, and context compression.
- `internal/agent`: Leader/Sub-Agent contracts, structured results, claims, evidence IDs, usage, and error categories.
- `internal/orchestrator`: DAG validation, ready-node calculation, parallel scheduling, retry handling, cancellation propagation, early stop, and lifecycle evidence events.
- `internal/browser`: Go contracts, chromedp runtime adapter, session/profile policy checks, semantic locator, accessibility/screenshot helpers, confirmation gate, coordinate fallback, redaction, evidence builders, and tests.
- `internal/permission`: Global permission policies, evaluator, confirmation workflow, and audit records.
- `internal/verification`: Claim extraction, claim/evidence coverage, conflict detection, and confidence policy.
- `internal/cost`: Registry-backed pricing lookup, usage estimation, and budget limit enforcement.
- `internal/model`: Model trust levels, capabilities, registry, policy selection, provider interfaces, and OpenAI-compatible HTTP boundary.
- `internal/privacy`: HMAC-SHA256 pseudonymization, secret redaction, credential dump blocking, and audit metadata.
- `internal/api`: Local REST handler for task create/list/status/cancel/events, trigger/event ingestion, dashboard/SSE/model discovery, notification inbox, and confirmation inspect/approve/deny.
- `internal/runtime`: Local-first Personal Agent Runtime that routes task execution through persisted workflows, bounded planning, memory, hybrid RAG, evidence, verification, synthesis, guarded external capability dispatch, and public escalation fail-closed boundaries.
- `internal/codingagent`: External Coding Agent contracts, registry, safe process executor, worktree isolation, Codex/Claude CLI adapters, environment sanitizer, and evidence validation.
- `internal/capability`: Capability descriptions, health states, deterministic policy resolver, runtime metrics, and Runtime capability registration.
- `internal/skill`: Versioned Skill manifests, dependency/permission validation, registry, skill-first matching, eval-gated activation, and draft candidate generation.
- `internal/workflow`: Persistent workflow/node state, persisted dependencies, checkpoints, recovery, and pause/resume/cancel transitions.
- `internal/project`: Project context and allowlist policy persisted through SQLite.
- `internal/mcp`: Read-only MCP server/tool metadata and capability adaptation boundary.
- `internal/trigger`: Proactive trigger model, validation, persistence, schedule calculation, daemon tick/recovery, condition evaluation, watcher scan, deduplication, debounce, cooldown, jitter, backoff helpers, history, and dead-letter storage.
- `internal/event`: Untrusted event envelope, in-memory bus, SQLite event store, and event deduplication.
- `internal/notification`: SQLite-backed local notification inbox.
- `internal/goal`: Bounded local goal planner.
- `internal/observability`: Structured log redaction, in-process metric snapshots, and health/readiness reports.
- `internal/e2e`: Mock-based local integration tests that combine API, SQLite, RAG, memory, model mocks, browser denial, verification, cost, and secret-leak regression coverage.

### P0 Usage

The completed P0 tool is `pachat`. Use it to verify local configuration, SQLite migrations, task persistence, interactive memory, and long-task state tracking.

Build:

```sh
make build
```

Run a no-op task:

```sh
bin/pachat run --config configs/config.example.yaml --task "smoke test"
```

Run with your own task text:

```sh
bin/pachat run --config configs/config.example.yaml --task "summarize my local notes"
bin/pachat run --config configs/config.example.yaml --project <project_id> --task "summarize project notes"
```

Expected output shape:

```text
task_id=task_<generated_id>
status=completed
confidence=0.90
remote_tokens=0

answer:
Local-first answer for: smoke test
```

What happens:

- The CLI loads and validates `configs/config.example.yaml`.
- SQLite opens at `./data/personal-agent.db`.
- Embedded migrations under `internal/storage/migrations` are applied.
- One row is inserted into the `tasks` table as `running`.
- The Runtime queries local memory and local RAG, stores evidence, verifies the answer, and marks the task `completed`.

Interactive local memory with slash-command completion:

```sh
bin/pachat chat --config configs/config.example.yaml
```

Inside chat, type a message to store it in local episodic memory. Type `/` and press `Tab` to complete built-in commands. Use `/help` to list commands, `/memory` to view recent memory, and `/exit` or `/quit` to leave.

Long-task state:

```sh
bin/pachat run --config configs/config.example.yaml --task "long local task" --long
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task usage --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```

Operator commands:

```sh
bin/pachat init --config configs/config.yaml --data-dir ./data
bin/pachat config validate --config configs/config.example.yaml
bin/pachat doctor --config configs/config.example.yaml
bin/pachat knowledge add --config configs/config.example.yaml ./notes.txt
bin/pachat knowledge list --config configs/config.example.yaml --json
bin/pachat project create --config configs/config.example.yaml --name "Local Project"
bin/pachat workflow events --config configs/config.example.yaml --id <workflow_id>
bin/pachat trigger create --config configs/config.example.yaml --type manual --id local-trigger
bin/pachat trigger run-now --config configs/config.example.yaml --id local-trigger
bin/pachat approval list --config configs/config.example.yaml
bin/pachat notification list --config configs/config.example.yaml
bin/pachat model discover --config configs/config.example.yaml
bin/pachat watcher scan --config configs/config.example.yaml --path ./docs
bin/pachat serve --config configs/config.example.yaml
curl http://127.0.0.1:8787/healthz
curl http://127.0.0.1:8787/readyz
curl http://127.0.0.1:8787/metrics
curl http://127.0.0.1:8787/dashboard
curl http://127.0.0.1:8787/models/discover
bin/pachat backup create --config configs/config.example.yaml
bin/pachat storage integrity --config configs/config.example.yaml
```

Current CLI boundary: default task execution is workflow-backed and local-first. Browser, Coding, MCP, and public model paths are governed and fail closed unless explicit executors/providers are configured.

Cancellation boundary: API/CLI task cancellation propagates through Runtime workflows, the scheduler, Sub-Agent execution, configured model HTTP calls, vector HTTP calls, browser executors, MCP child processes, allowlisted local tool child processes, and Codex/Claude-style coding process runners.

Skill workflow boundary: active high-confidence read-only Skills compile directly into persistent workflows with `skill_id` and `skill_version`, then run through the existing scheduler without calling the Leader planner. Planner-backed and default Runtime workflows remain the fallback when no eligible Skill matches.

Background workflow worker: `pachat serve` starts a Runtime worker that resumes unfinished runnable workflows on startup and polls for more work. `paused` and `waiting_approval` workflows are left for operator or approval actions.

Side-effect idempotency: non-read-only workflow capabilities claim a persisted idempotency key before execution. Replays from duplicate events, retries, or crash recovery do not re-run already claimed browser writes, MCP/tool mutations, coding pushes, PR creation, deploy, or message-send style effects.

Project policy boundary: Runtime workflow execution applies project policy to Skill matching, Leader model selection, memory/RAG capabilities, public escalation, browser/MCP/tool dispatch, and Codex/Claude backend allowlists. CLI task runs can select a project with `--project`; API task creation accepts `project_id`.

Coding Cross Review: set `coding_agents.cross_review.enabled: true` and configure `large_diff_bytes` to require a second enabled coding backend for security-sensitive, critical-project, verification-failed, or large-diff coding tasks. Ordinary coding tasks still use one backend.

### P1 Model And Privacy Foundation

P1 adds library-level model and privacy controls:

- Model trust levels: `local_private`, `local_sandboxed`, `trusted_remote`, and `public_remote`.
- Model capabilities: `chat`, `tool_calling`, `json_schema`, `vision`, `embedding`, `rerank`, `long_context`, and `browser_reasoning`.
- Registry validation for duplicate model IDs, provider references, trust levels, and capabilities.
- Role-specific model policy selection so Leader and Sub-Agent settings remain independent.
- Mockable chat, embedding, and rerank provider interfaces.
- OpenAI-compatible chat and embedding HTTP boundary with injectable transport.
- Privacy Gateway with stable HMAC-SHA256 pseudonymization, redaction, blocking, and audit metadata.
- Public remote provider calls fail closed when the Privacy Gateway is missing or blocks the payload.

### P2 RAG And Memory Foundation

P2 adds library-level retrieval and memory controls:

- Deterministic document chunking with content hashes.
- SQLite-backed document and chunk persistence using the existing local schema.
- Local BM25 retrieval over stored chunks.
- Reciprocal Rank Fusion for combining ranked retrieval lists with deterministic tie-breaking.
- Qdrant HTTP wrapper with caller-supplied base URL, collection, API key, and HTTP client.
- Reranker interface boundary for later model-backed reranking.
- Context compression that preserves evidence IDs, source URIs, titles, metadata, scores, and privacy class.
- Working memory with expiration cleanup.
- Episodic memory retained for chronological local events.
- Semantic memory with optional indexing into RAG for durable facts and preferences.

### P3 Orchestration And Parallel Sub-Agents

P3 adds library-level orchestration controls:

- Task DAG validation for duplicate node IDs, missing dependencies, self-dependencies, and cycles.
- Dependency-aware ready-node calculation.
- Parallel scheduling through role-specific Sub-Agent contracts.
- Retry handling for retryable failures using each node's max-attempt policy.
- Caller-provided early stop policy.
- Context cancellation propagation to running Sub-Agents.
- Structured node results with claims, evidence IDs, usage, and typed error categories.
- Ordered Evidence Bus lifecycle events for ready, started, retrying, completed, failed, cancelled, and early stop records.

### P4 Browser Runtime Foundation

P4 adds a governed local browser runtime boundary:

- Runtime action adapter for navigation, reads, screenshots, accessibility reads, semantic targeting, input, and coordinate fallback.
- chromedp-backed runtime with caller-supplied allocator options and screenshot directory.
- Local profile store that resolves profile directories from configured environment variables without exposing profile paths in metadata.
- Confirmation gate for write and high-risk browser actions.
- Redacted browser evidence publishing through an in-memory browser Evidence Bus boundary.
- Local browser E2E fixture under `internal/browser/testdata/browser`; run it with `PACHAT_BROWSER_E2E=1 go test ./internal/browser`.

### P5 Permissions, Verification, And Cost Foundation

P5 adds library-level safety and accounting controls:

- Global permission action categories for file system, network, browser read/write, high-risk browser actions, memory writes, public model calls, credential access, and external side effects.
- Configurable permission policy defaults and per-action rules.
- Confirmation requests that include action, target, risk, evidence IDs, and exact proposed effect.
- Auditable permission decision records for later evidence persistence.
- Deterministic claim extraction with claim-to-evidence coverage checks.
- Conflict detection between claims and evidence.
- Confidence policy gates for final synthesis.
- Registry/config-backed pricing lookup and model usage cost estimation.
- Budget soft and hard limit enforcement.

### P6 API, E2E, And Hardening Foundation

P6 adds local API and integration validation:

- Standard-library REST handler for task create, list, status, cancel, and event listing.
- Confirmation inspect, approve, and deny endpoints backed by the confirmation store boundary.
- JSON responses with stable status codes and secret redaction on API-facing text.
- Full local E2E test using provider mocks, SQLite, RAG, episodic/semantic memory, browser high-risk denial, verification, cost accounting, and final synthesis.
- Secret-leak regression fixtures for API responses, Privacy Gateway output, and browser evidence.
- PostgreSQL integration test placeholder that skips cleanly unless a test DSN is configured.

### P7 Runtime Integration / Personal Agent MVP

P7 Runtime MVP is documented in `docs/projdocs/P0_P7_EXECUTION_PLAN.md`, `docs/p7_runtime_integration.md`, `docs/p7_runtime_mvp_implementation.md`, and `docs/projdocs/task/P7.md`. The first vertical slice connects storage, memory, local RAG, evidence, verification, synthesis, CLI execution, and API task creation into real local-first task execution.

P7 MVP acceptance covers the removal of the no-op task path, local-first evidence, zero public model usage when local evidence is enough, workflow-backed CLI/API task execution, Runtime-backed chat turns, verification reports, persisted final answers, API background cancellation, bounded Leader planning interfaces, guarded BrowserAgent execution boundaries, public model escalation fail-closed interfaces, provider usage propagation when real providers return usage, and secret-leak regression coverage.

### P8 External Coding Agents

P8 is documented in `docs/projdocs/P0_P8_EXECUTION_PLAN.md`, `docs/p8_external_coding_agents.md`, and `docs/projdocs/task/P8.md`. The first library slice adds a governed External Coding Agent boundary for Codex CLI and Claude Code CLI style backends.

P8 includes configurable backend enablement, independent execution location and inference trust, capability-based backend selection with fallback, safe subprocess execution with timeout/cancellation, minimal environment sanitization, Git worktree isolation, Codex/Claude adapter request mapping, repository privacy denial for confidential repositories using public remote inference, and evidence validation requiring actual diff/files/tests instead of self-reported completion.

Current P8 boundary: coding capability nodes are recognized by Runtime workflow dispatch and return governed blocked/unavailable results unless a backend is explicitly enabled. Example configuration defaults keep Codex/Claude disabled and direct writes disabled.

### P9 Core Release Slice

P9 is documented in `docs/p9_core_release_slice.md`, `docs/projdocs/P0_P9_EXECUTION_PLAN.md`, and `docs/projdocs/task/P9.md`. This slice adds deterministic capability discovery and policy resolution, versioned Skill validation and matching, Project-scoped policy data, persistent Workflow checkpoints and recovery, and a read-only MCP metadata adapter.

Operator commands:

```sh
bin/pachat capability list --config configs/config.example.yaml
bin/pachat capability health --config configs/config.example.yaml
bin/pachat workflow list --config configs/config.example.yaml
bin/pachat workflow show --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow pause --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow resume --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow cancel --config configs/config.example.yaml --id <workflow_id>
```

The current P9 boundary persists workflow state, node dependencies, and checkpoints; exposes safe lifecycle transitions; provides capability metrics-aware routing; adds eval-gated Skill activation and draft candidate generation; and exposes governed MCP execution placeholders. External MCP transport remains fail-closed unless an executor is explicitly provided.

### P10 Proactive Trigger/Event Core Slice

P10 is documented in `docs/p10_core_release_slice.md`, `docs/projdocs/P0_P10_EXECUTION_PLAN.md`, and `docs/projdocs/task/P10.md`. This first proactive slice adds persistent trigger definitions and state, one-shot/interval/simple-cron schedule calculation, deterministic execution keys, debounce, cooldown, jitter, exponential backoff helpers, untrusted event envelopes, event deduplication, and an in-memory event bus with SQLite persistence.

Current P10 boundary: the package provides trigger CLI/API commands, push event ingestion, trigger history, daemon tick/recovery helpers, condition evaluation, dead-letter persistence, local notification inboxes, bounded goal planning, and filesystem watcher scans. Continuous always-on daemon operation and third-party notification delivery remain deployment choices.

### P11 Runtime Integration Closure Slice

P11 is documented in `docs/p11_runtime_integration_closure.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P11.md`. The first closure slice adds a Runtime-owned workflow engine that loads persisted workflow runs, recovers non-terminal nodes, checks project capability policy, checks registered capability availability, runs local read-only nodes through the existing scheduler, persists per-node checkpoints, and marks workflows completed, failed, or cancelled.

Current P11 boundary: `Runtime.Run()` now creates and executes persisted workflows through the scheduler. Persisted workflows can execute `memory.search`, `rag.search`, `verification.verify`, and `synthesis.local`; Browser/Coding/MCP nodes are routed to guarded SubAgents and fail closed when executors are unavailable.

### P12 Operator Experience Core Slice

P12 is documented in `docs/p12_operator_experience.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P12.md`. This core slice adds versioned strict config validation, `pachat init`, `pachat config validate`, `pachat doctor`, local knowledge indexing/list/status/reindex/remove commands, project create/list/show/update/archive/use commands, task/workflow JSON output, workflow checkpoint event inspection, a packaged local `pachat serve` command with health/readiness endpoints, backup create/restore dry-run, event retention cleanup, and SQLite integrity/vacuum/checkpoint maintenance commands.

Current P12 boundary: the operator CLI and REST API include persistent approval inbox storage, local notification inboxes, dashboard and SSE-ready endpoints, model discovery from configuration, filesystem watcher scans, and packaged local serving. The dashboard endpoint is intentionally minimal rather than a full visual web application.

### P13 Reliability / Security / Observability Release Gate

P13 is documented in `docs/p13_reliability_security_observability.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P13.md`. This core slice adds centralized redaction for structured logs and API-facing text, in-process local runtime metrics, JSON `/healthz` and `/readyz` reports for `pachat serve`, a JSON `/metrics` endpoint, request/error/activity counters, and secret-leak regression coverage for observability output.

The remaining P13 release-gate foundations add local tracing spans, a global resource governor, disk pressure checks in readiness, graceful shutdown coordination primitives, API auth/CORS/body-limit/rate-limit/security-header middleware, command/path/network policy helpers, prompt-injection and secret-leak fixtures, coding-agent sandbox checks, browser security regression helpers, a durable `security_audit_log` migration/store, golden eval cases, and measurable quality-gate threshold evaluation.

Current P13 boundary: P13 now provides local policy boundaries and regression gates. It does not claim production-grade OS-level sandboxing, distributed tracing export, external vulnerability services, or kernel/browser isolation.

### P14 v1.0 GA Release Packaging

P14 is documented in `docs/p14_ga_release_requirements.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P14.md`. This release slice adds a version model (`pachat version`), reproducible release metadata, a required build matrix for `darwin/arm64`, `linux/amd64`, and `linux/arm64`, SHA256 checksum generation, bootstrap install and upgrade scripts, systemd and launchd service examples, safe production config examples, migration compatibility coverage, and release checklist automation through `pachat release check` and `make release-check`.

The default `configs/config.example.yaml` uses the locally available Ollama model `qwen3.8:27b-mlx`. Change `models.registry[].model` in the mountable config file when using a different local model.

Interactive `pachat chat` now sends turns to the configured local OpenAI-compatible provider and uses `agent.leader.model_id` for model selection. The same configured Leader provider is also wired into `BoundedModelPlanner` when `agent.planner.enabled` is true and the selected model supports `chat` and `json_schema`. Planner output is bounded by `agent.planner.max_nodes`, validated against the Runtime capability registry, and rejected before workflow creation when it references unknown capabilities or invalid DAG dependencies. Provider connectivity or response errors are returned explicitly.

Planner configuration:

```yaml
agent:
  planner:
    enabled: true
    max_nodes: 8
```

Workflow reasoning and synthesis now reuse the configured private local model when `Runtime.ChatProvider` is available. `reasoning.local` emits bounded claims, evidence IDs, confidence, and provider usage without storing chain-of-thought. `synthesis.local` writes the final answer from verified evidence, and `Runtime.Run()` uses the completed synthesis checkpoint as the persisted task answer.

Hybrid RAG production wiring is controlled by the mountable `rag` config. `rag.vector` composes an embedding model with Qdrant vector search; `rag.reranker` composes a configured rerank model when available. If vector search or reranking is disabled or unavailable, retrieval keeps the local BM25 fallback.

Runtime capability executors are now registered through a local executor registry. When browser support is enabled, `browser.read`, `browser.navigate`, and `browser.write` route through the governed browser tool; read observations are persisted as Runtime evidence, while navigation and write actions still pass through session/domain policy and confirmation gates.

Enabled coding backends are registered as `coding.<backend>` executors, including Codex and Claude style adapters. Coding workflow nodes must provide structured JSON input with `repository_path`, `prompt`, write policy, privacy class, and optional test commands; execution delegates to the governed coding agent Runner and stores diff/test evidence.

Configured MCP servers are registered as `mcp.<server>.<tool>` executors and called through stdio JSON-RPC. MCP output is persisted as `UNTRUSTED OBSERVATION` evidence. Configured local tools are registered from `tools.allowlist`; only fixed allowlisted program/args execute, and workflow input is not interpolated into shell commands.

Public escalation is wired from enabled `public_remote` chat models in the model registry. If a public provider requires Privacy Gateway, `privacy.hmac_secret_env` must resolve or Runtime build fails closed. Secret-bearing payloads are blocked before provider calls.

Task usage can be queried with `pachat task usage --config <path> --id <task_id>`. Usage is aggregated from workflow checkpoints at node, workflow, and task levels. Model providers with reliable usage use provider token counts; external CLI/tool usage remains marked as unknown when token counts are unavailable.

Verification-driven early stop is enabled in the workflow engine. Once completed node claims meet the verification policy with persisted evidence and no conflicts, the scheduler cancels remaining pending work and usage stops growing beyond completed checkpoints.

P14 documentation lives under `docs/release/` and covers quickstart, user operations, administration, security/threat model, troubleshooting, upgrade, RC E2E scenarios, soak testing, performance baselines, and data-loss recovery drills. The current release package is archive/script based; package manager publishing and Windows binaries are post-v1 work.

### Validation

Run:

```sh
go test ./...
make build
make smoke
make release-build
make checksums
go run ./cmd/pachat release check --quick
```

### Safety Defaults

- Browser sessions, cookies, tokens, passwords, and profile data remain local.
- Public LLM browser planning can receive only Privacy Gateway redacted summaries.
- Browser actions pass through the Permission Layer before execution.
- High-risk actions such as login submission, payment, deletion, sending messages, uploads, OAuth grants, and legal acceptance require confirmation or elevated policy.
- Evidence Bus records must be redacted before storage.
- API responses do not echo raw task input and redact secret-like text in titles, final answers, events, and confirmation fields.

## 中文

面向本地用户工作流的 AI Agent。

本仓库是一个 Go 版本本地优先并行个人 Agent。当前实现包含 P0 基础层、P1 模型/隐私基础层、P2 RAG/记忆基础层、P3 编排/Sub-Agent 基础层、P4 浏览器运行时基础层、P5 权限/验证/成本基础层、P6 API/E2E/加固基础层、P7 Runtime MVP 闭环、P8 External Coding Agent 库层基础、P9 Capability/Skill/Workflow 核心闭环、P10 proactive trigger/event 核心闭环、P11 Runtime Integration 闭环、P12 Operator Experience 核心闭环、P13 Reliability/Security/Observability 核心 slice，以及 P14 v1.0 GA 发布打包 slice：`pachat` CLI 打包、配置加载、SQLite 迁移、核心 agent/browser 契约、Runtime-backed chat、可取消 API 后台长任务、模型注册/策略契约、OpenAI-compatible provider 边界、面向 public remote 模型调用的 fail-closed Privacy Gateway、本地 BM25 检索、可选 vector/RRF/reranker 的 hybrid RAG 降级路径、上下文压缩、working/episodic/semantic memory store、可取消 DAG 编排、依赖感知并行调度、Sub-Agent retry、early stop、有序节点生命周期 evidence event、受治理的 chromedp 浏览器运行时边界、全局权限策略评估、持久化 approval inbox、确认 approve/deny API、claim/evidence 验证、冲突检测、基于 registry 的成本核算、本地 REST 任务 API、基于 mock 的完整本地 E2E 覆盖、secret-leak 回归 fixture、CLI/API 任务执行使用的 workflow-backed Runtime、有界模型驱动 Leader planning 接口、面向 Codex/Claude 风格 CLI backend 的受治理 External Coding Agent 边界、确定性 capability/skill registry、capability runtime metrics、Skill eval activation gate、draft skill candidate generation、持久化 workflow checkpoint 与 node dependency、只读 MCP metadata adapter 与受治理 MCP 执行占位、持久化 proactive trigger、不可信 event envelope、event ingestion API、proactive daemon tick/recovery helper、本地 notification inbox、有界 goal planning、filesystem watcher scan、严格版本化配置校验、本地 doctor 诊断、knowledge/project/operator 命令、打包本地 HTTP server、dashboard/SSE/model-discovery endpoint、backup/restore dry-run、retention cleanup、SQLite maintenance 命令、structured log redaction、本地 runtime metrics、JSON health/readiness 响应、发布版本元数据、跨平台 release build、checksum、install/upgrade 脚本、service template、生产配置样例、release runbook，以及覆盖 model HTTP、browser、MCP、tool、coding 进程边界的 context cancellation 一致性。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏、Evidence Bus 记录、模型选择、provider enforcement、hybrid 本地检索/记忆索引、编排、语义浏览器定位、accessibility 读取、截图、坐标兜底、带确认 gate 的浏览器执行、全局权限决策、验证 gate、预算控制、本地 HTTP handler wiring、workflow-backed Runtime execution、coding agent adapter 选择、安全进程执行、环境脱敏、Git worktree 隔离、CLI adapter request mapping、coding evidence 收集、repository privacy 检查、trigger scheduling helper、trigger state persistence、event deduplication、本地运维工具、持久化 approval、notification、dashboard event stub、watcher scan 和有界 goal planning。仓库仍不包含 cookie 读取器、密码读取器、生产级 OS/kernel/browser 隔离、外部分布式 tracing export、外部漏洞扫描或第三方通知投递。

### 内容

- `cmd/pachat`：CLI 入口。
- `configs/config.example.yaml`：本地优先示例配置。
- `internal/config`：YAML 加载和校验。
- `internal/storage`：SQLite 存储与迁移。
- `internal/memory`：working、episodic 和 semantic memory 存储。
- `internal/rag`：document/chunk 存储、chunking、BM25 检索、RRF 融合、Qdrant wrapper、reranker 边界和上下文压缩。
- `internal/agent`：Leader/Sub-Agent 契约、结构化结果、claims、evidence IDs、usage 和错误分类。
- `internal/orchestrator`：DAG 校验、ready-node 计算、并行调度、retry、取消传播、early stop 和生命周期 evidence event。
- `internal/browser`：Go 契约、chromedp runtime adapter、session/profile 策略校验、semantic locator、accessibility/screenshot helper、confirmation gate、coordinate fallback、脱敏、evidence builder 和测试。
- `internal/permission`：全局权限策略、评估器、确认流程和审计记录。
- `internal/verification`：claim 抽取、claim/evidence 覆盖率、冲突检测和置信度策略。
- `internal/cost`：基于 registry 的价格查询、usage 估算和预算限制执行。
- `internal/model`：模型 trust level、capability、registry、policy selection、provider interface 和 OpenAI-compatible HTTP 边界。
- `internal/privacy`：HMAC-SHA256 pseudonymization、secret redaction、credential dump blocking 和 audit metadata。
- `internal/api`：本地 REST handler，支持 task create/list/status/cancel/events、trigger/event ingestion、dashboard/SSE/model discovery、notification inbox 和 confirmation inspect/approve/deny。
- `internal/runtime`：本地优先 Personal Agent Runtime，通过持久化 workflow 执行 task，连接有界 planning、memory、hybrid RAG、evidence、verification、synthesis、受治理外部 capability dispatch 和 public escalation fail-closed 边界。
- `internal/codingagent`：External Coding Agent 契约、registry、安全进程执行器、worktree 隔离、Codex/Claude CLI adapter、环境脱敏和 evidence 验证。
- `internal/capability`：Capability 描述、health 状态、确定性 policy resolver、runtime metrics 和 Runtime capability 注册。
- `internal/skill`：版本化 Skill manifest、依赖/权限校验、registry、skill-first matching、eval-gated activation 和 draft candidate generation。
- `internal/workflow`：持久化 workflow/node 状态、持久化 dependency、checkpoint、recovery 和 pause/resume/cancel 状态转换。
- `internal/project`：通过 SQLite 持久化的 Project context 与 allowlist policy。
- `internal/mcp`：只读 MCP server/tool metadata 和 capability adapter 边界。
- `internal/trigger`：proactive trigger 模型、校验、持久化、调度计算、daemon tick/recovery、condition evaluation、watcher scan、deduplication、debounce、cooldown、jitter、backoff helper、history 和 dead-letter storage。
- `internal/event`：不可信 event envelope、内存 event bus、SQLite event store 和 event deduplication。
- `internal/notification`：SQLite-backed 本地 notification inbox。
- `internal/goal`：有界本地 goal planner。
- `internal/observability`：structured log redaction、进程内 metrics snapshot 和 health/readiness report。
- `internal/e2e`：基于 mock 的本地集成测试，组合 API、SQLite、RAG、memory、model mock、浏览器 denial、verification、cost 和 secret-leak 回归覆盖。

### P0 使用方式

当前已完成的 P0 工具是 `pachat`，用于验证本地配置加载、SQLite 迁移、task 持久化、交互式记忆和长任务状态跟踪。

构建：

```sh
make build
```

运行 no-op 任务：

```sh
bin/pachat run --config configs/config.example.yaml --task "smoke test"
```

使用自己的任务文本：

```sh
bin/pachat run --config configs/config.example.yaml --task "帮我整理本地资料"
bin/pachat run --config configs/config.example.yaml --project <project_id> --task "整理项目资料"
```

预期输出形态：

```text
task_id=task_<generated_id>
status=completed
confidence=0.90
remote_tokens=0

answer:
Local-first answer for: 帮我整理本地资料
```

执行过程：

- CLI 加载并校验 `configs/config.example.yaml`。
- SQLite 打开位置为 `./data/personal-agent.db`。
- 执行 `internal/storage/migrations` 中的内置迁移。
- 以 `running` 状态向 `tasks` 表写入一条记录。
- Runtime 查询本地 memory 和本地 RAG、写入 evidence、验证答案，并将任务标记为 `completed`。

带 slash 命令补全的交互式本地记忆：

```sh
bin/pachat chat --config configs/config.example.yaml
```

进入 chat 后，输入普通消息会写入本地 episodic memory。输入 `/` 后按 `Tab` 可以补全内置命令。使用 `/help` 查看命令列表，使用 `/memory` 查看近期记忆，使用 `/exit` 或 `/quit` 退出。

长任务状态：

```sh
bin/pachat run --config configs/config.example.yaml --task "long local task" --long
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task usage --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```

运维命令：

```sh
bin/pachat init --config configs/config.yaml --data-dir ./data
bin/pachat config validate --config configs/config.example.yaml
bin/pachat doctor --config configs/config.example.yaml
bin/pachat knowledge add --config configs/config.example.yaml ./notes.txt
bin/pachat knowledge list --config configs/config.example.yaml --json
bin/pachat project create --config configs/config.example.yaml --name "Local Project"
bin/pachat workflow events --config configs/config.example.yaml --id <workflow_id>
bin/pachat trigger create --config configs/config.example.yaml --type manual --id local-trigger
bin/pachat trigger run-now --config configs/config.example.yaml --id local-trigger
bin/pachat approval list --config configs/config.example.yaml
bin/pachat notification list --config configs/config.example.yaml
bin/pachat model discover --config configs/config.example.yaml
bin/pachat watcher scan --config configs/config.example.yaml --path ./docs
bin/pachat serve --config configs/config.example.yaml
curl http://127.0.0.1:8787/healthz
curl http://127.0.0.1:8787/readyz
curl http://127.0.0.1:8787/metrics
curl http://127.0.0.1:8787/dashboard
curl http://127.0.0.1:8787/models/discover
bin/pachat backup create --config configs/config.example.yaml
bin/pachat storage integrity --config configs/config.example.yaml
```

当前 CLI 边界：默认 task execution 已通过 workflow 和 scheduler 执行，并保持本地优先。Browser、Coding、MCP 和 public model 路径必须显式配置 executor/provider，否则按治理策略 fail closed。

取消边界：API/CLI task cancel 会向 Runtime workflow、scheduler、Sub-Agent execution、配置的 model HTTP 调用、vector HTTP 调用、browser executor、MCP 子进程、allowlisted 本地 tool 子进程，以及 Codex/Claude 风格 coding process runner 传播。

Skill workflow 边界：active、高置信、read-only Skill 会直接编译为带 `skill_id` 和 `skill_version` 的持久化 workflow，并通过现有 scheduler 执行，不调用 Leader planner。没有合格 Skill 命中时继续回退到 planner-backed 或默认 Runtime workflow。

后台 workflow worker：`pachat serve` 会启动 Runtime worker，在服务启动时恢复未完成且可运行的 workflow，并持续轮询后续工作。`paused` 和 `waiting_approval` workflow 会保留给 operator 或 approval 操作处理。

Side-effect idempotency：非 read-only workflow capability 在执行前会持久化认领 idempotency key。duplicate event、retry 或 crash recovery 触发的 replay 不会重复执行已认领的 browser write、MCP/tool mutation、coding push、PR create、deploy 或 message send 类 side effect。

Project policy 边界：Runtime workflow execution 会把 project policy 统一应用到 Skill matching、Leader model selection、memory/RAG capability、public escalation、browser/MCP/tool dispatch，以及 Codex/Claude backend allowlist。CLI task run 可用 `--project` 选择 project；API task create 接受 `project_id`。

Coding Cross Review：设置 `coding_agents.cross_review.enabled: true` 并配置 `large_diff_bytes` 后，security-sensitive、critical-project、verification-failed 或 large-diff coding task 会要求第二个已启用 coding backend 做审查。普通 coding task 仍只使用一个 backend。

### P1 模型与隐私基础

P1 新增库层模型和隐私控制：

- 模型 trust level：`local_private`、`local_sandboxed`、`trusted_remote`、`public_remote`。
- 模型 capability：`chat`、`tool_calling`、`json_schema`、`vision`、`embedding`、`rerank`、`long_context`、`browser_reasoning`。
- Registry 校验 duplicate model ID、provider reference、trust level 和 capability。
- 按 role 独立选择模型，确保 Leader 与 Sub-Agent 配置互不泄漏。
- 可 mock 的 chat、embedding、rerank provider interface。
- OpenAI-compatible chat 和 embedding HTTP 边界，支持注入 transport 进行无网络测试。
- Privacy Gateway 支持稳定 HMAC-SHA256 pseudonymization、redaction、blocking 和 audit metadata。
- public remote provider 在 Privacy Gateway 缺失或 payload 被阻断时 fail closed。

### P2 RAG 与记忆基础

P2 新增库层检索和记忆控制：

- 使用 content hash 的确定性 document chunking。
- 基于现有本地 schema 的 SQLite document/chunk 持久化。
- 针对已存储 chunk 的本地 BM25 检索。
- 使用 Reciprocal Rank Fusion 合并多路检索结果，并提供确定性 tie-breaking。
- Qdrant HTTP wrapper，base URL、collection、API key 和 HTTP client 均由调用方提供。
- Reranker interface，为后续模型 rerank 接线预留边界。
- 上下文压缩会保留 evidence ID、source URI、title、metadata、score 和 privacy class。
- 支持过期清理的 working memory。
- 保留用于按时间记录本地事件的 episodic memory。
- Semantic memory 支持把长期事实和偏好可选索引到 RAG。

### P3 编排与并行 Sub-Agent 基础

P3 新增库层编排控制：

- Task DAG 校验 duplicate node ID、missing dependency、self-dependency 和 cycle。
- 依赖感知 ready-node 计算。
- 通过按 role 注册的 Sub-Agent contract 做并行调度。
- 基于每个 node 的 max-attempt policy 处理 retryable failure。
- 支持调用方提供 early stop policy。
- 将 context cancellation 传播给正在运行的 Sub-Agent。
- 结构化 node result，包含 claims、evidence IDs、usage 和 typed error category。
- 有序 Evidence Bus 生命周期事件，覆盖 ready、started、retrying、completed、failed、cancelled 和 early stop。

### P4 浏览器运行时基础

P4 新增受治理的本地浏览器 runtime 边界：

- Runtime action adapter 支持 navigation、读取、截图、accessibility 读取、semantic targeting、输入和坐标兜底。
- 基于 chromedp 的 runtime，allocator options 和截图目录都由调用方配置。
- 本地 profile store 从配置的环境变量解析 profile 目录，不在 metadata 中暴露 profile path。
- write 和 high-risk 浏览器动作必须通过 confirmation gate。
- 通过内存 browser Evidence Bus 边界发布脱敏浏览器 evidence。
- 本地浏览器 E2E fixture 位于 `internal/browser/testdata/browser`；使用 `PACHAT_BROWSER_E2E=1 go test ./internal/browser` 运行。

### P5 权限、验证与成本基础

P5 新增库层安全和核算控制：

- 覆盖文件系统、网络、浏览器读写、高风险浏览器动作、memory write、public model call、credential access 和 external side effect 的全局权限动作分类。
- 可配置的权限默认策略和按 action 覆盖规则。
- 确认请求包含 action、target、risk、evidence IDs 和精确 proposed effect。
- 可审计的权限决策记录，供后续持久化为 evidence。
- 确定性 claim 抽取和 claim/evidence 覆盖率检查。
- 检测 claim 与 evidence 之间的冲突。
- 用于 final synthesis 的置信度策略 gate。
- 基于 registry/config 的价格查询和模型 usage 成本估算。
- 预算 soft limit 和 hard limit 执行。

### P6 API、E2E 与加固基础

P6 新增本地 API 和集成验证：

- 基于标准库的 REST handler，支持 task create、list、status、cancel 和 event listing。
- 基于 confirmation store 边界的 confirmation inspect、approve 和 deny endpoint。
- JSON 响应提供稳定状态码，并对 API 输出文本做 secret redaction。
- 完整本地 E2E 测试，覆盖 provider mock、SQLite、RAG、episodic/semantic memory、浏览器高风险拒绝、verification、cost accounting 和 final synthesis。
- 针对 API 响应、Privacy Gateway 输出和 browser evidence 的 secret-leak 回归 fixture。
- PostgreSQL integration test placeholder，在未配置 test DSN 时会干净跳过。

### P7 Runtime Integration / Personal Agent MVP

P7 Runtime MVP 已记录在 `docs/projdocs/P0_P7_EXECUTION_PLAN.md`、`docs/p7_runtime_integration.md`、`docs/p7_runtime_mvp_implementation.md` 和 `docs/projdocs/task/P7.md` 中。第一条 vertical slice 已将 storage、memory、本地 RAG、evidence、verification、synthesis、CLI execution 和 API task creation 连接成真正的本地优先任务执行路径。

P7 MVP 验收覆盖移除 no-op task path、local-first evidence、本地证据足够时 public model usage 为零、workflow-backed CLI/API task execution、Runtime-backed chat turn、verification report、持久化 final answer、API background cancellation、有界 Leader planning interface、受治理 BrowserAgent execution boundary、public model escalation fail-closed interface、真实 provider usage 传播和 secret-leak 回归覆盖。

### P8 External Coding Agents

P8 已记录在 `docs/projdocs/P0_P8_EXECUTION_PLAN.md`、`docs/p8_external_coding_agents.md` 和 `docs/projdocs/task/P8.md` 中。第一条库层 slice 增加了面向 Codex CLI 与 Claude Code CLI 风格 backend 的受治理 External Coding Agent 边界。

P8 包含可配置 backend enablement、独立的 execution location 与 inference trust、基于 capability 的 backend selection 与 fallback、安全 subprocess 执行及 timeout/cancellation、最小环境脱敏、Git worktree 隔离、Codex/Claude adapter request mapping、针对 confidential repository + public remote inference 的隐私拒绝，以及要求真实 diff/files/tests 而非 self-reported completion 的 evidence 验证。

当前 P8 边界：Runtime workflow dispatch 能识别 coding capability node；未显式启用 backend 时会返回受治理的 blocked/unavailable result。示例配置默认禁用 Codex/Claude，且禁用 direct writes。

### P9 核心 Release Slice

P9 已记录在 `docs/p9_core_release_slice.md`、`docs/projdocs/P0_P9_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P9.md` 中。本核心 slice 增加确定性的 Capability Registry/Resolver、版本化 Skill manifest 校验与匹配、Project-scoped policy 数据、带 checkpoint 和 crash recovery 查询的持久化 Workflow state，以及只读 MCP metadata adapter。

运维命令：

```sh
bin/pachat capability list --config configs/config.example.yaml
bin/pachat capability health --config configs/config.example.yaml
bin/pachat workflow list --config configs/config.example.yaml
bin/pachat workflow show --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow pause --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow resume --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow cancel --config configs/config.example.yaml --id <workflow_id>
```

当前 P9 边界持久化 Workflow 状态、node dependency 和 checkpoint，并提供安全 lifecycle transition、capability metrics-aware routing、eval-gated Skill activation、draft skill candidate generation 和受治理 MCP 执行占位。外部 MCP transport 未显式配置 executor 时 fail closed。

### P10 Proactive Trigger/Event 核心 Slice

P10 已记录在 `docs/p10_core_release_slice.md`、`docs/projdocs/P0_P10_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P10.md` 中。第一条 proactive slice 增加持久化 trigger definition/state、one-shot/interval/simple-cron 调度计算、确定性 execution key、debounce、cooldown、jitter、指数 backoff helper、不可信 event envelope、event deduplication，以及带 SQLite 持久化的内存 event bus。

当前 P10 边界：已提供 trigger CLI/API、push event ingestion、trigger history、daemon tick/recovery helper、condition evaluation、dead-letter persistence、本地 notification inbox、有界 goal planning 和 filesystem watcher scan。持续常驻 daemon 与第三方通知投递属于部署选择。

### P11 Runtime Integration Closure Slice

P11 已记录在 `docs/p11_runtime_integration_closure.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P11.md` 中。第一条 closure slice 增加 Runtime 持有的 workflow engine：它会加载持久化 workflow run，恢复非终态 node，检查 project capability policy，检查已注册 capability 可用性，通过现有 scheduler 执行本地只读 node，写入 per-node checkpoint，并将 workflow 标记为 completed、failed 或 cancelled。

当前 P11 边界：`Runtime.Run()` 会创建持久化 workflow 并通过 scheduler 执行。持久化 workflow 可以执行 `memory.search`、`rag.search`、`verification.verify` 和 `synthesis.local`；Browser/Coding/MCP node 会路由到 guarded SubAgent，并在 executor 不可用时 fail closed。

### P12 Operator Experience 核心 Slice

P12 已记录在 `docs/p12_operator_experience.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P12.md` 中。本核心 slice 增加版本化严格配置校验、`pachat init`、`pachat config validate`、`pachat doctor`、本地 knowledge index/list/status/reindex/remove 命令、project create/list/show/update/archive/use 命令、task/workflow JSON 输出、workflow checkpoint event 查看、带 health/readiness endpoint 的本地 `pachat serve` 命令、backup create/restore dry-run、event retention cleanup，以及 SQLite integrity/vacuum/checkpoint maintenance 命令。

当前 P12 边界：operator CLI 与 REST API 已包含持久化 approval inbox、本地 notification inbox、dashboard 和 SSE-ready endpoint、基于配置的 model discovery、filesystem watcher scan 和打包本地 serving。dashboard endpoint 是最小可运行状态面，不是完整视觉 Web 应用。

### P13 Reliability / Security / Observability Release Gate

P13 已记录在 `docs/p13_reliability_security_observability.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P13.md` 中。本核心 slice 增加 structured log 与 API-facing text 的集中脱敏、进程内本地 runtime metrics、`pachat serve` 的 JSON `/healthz` 和 `/readyz`、JSON `/metrics` endpoint、请求/错误/活动请求计数，以及 observability 输出的 secret-leak 回归覆盖。

P13 剩余 release-gate 基础现已补齐：本地 tracing span、全局 resource governor、readiness 中的 disk pressure check、graceful shutdown 协调原语、API auth/CORS/body limit/rate limit/security header middleware、command/path/network policy helper、prompt-injection 与 secret-leak fixture、coding-agent sandbox 检查、browser security regression helper、持久化 `security_audit_log` migration/store、golden eval case，以及可度量 quality-gate threshold evaluation。

当前 P13 边界：P13 现在提供本地 policy boundary 和 regression gate，但不声称已经具备生产级 OS sandbox、分布式 tracing export、外部漏洞扫描服务，或 kernel/browser 隔离能力。

### P14 v1.0 GA 发布打包

P14 已记录在 `docs/p14_ga_release_requirements.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P14.md` 中。本发布 slice 增加版本模型（`pachat version`）、可复现发布元数据、`darwin/arm64`、`linux/amd64` 与 `linux/arm64` 的必需构建矩阵、SHA256 checksum 生成、bootstrap install 与 upgrade 脚本、systemd 与 launchd 服务示例、安全 production config 示例、迁移兼容测试，以及通过 `pachat release check` 和 `make release-check` 执行的发布检查自动化。

默认配置 `configs/config.example.yaml` 已使用本地 Ollama 模型 `qwen3.8:27b-mlx`；如本机模型名称不同，请在可挂载配置文件中修改 `models.registry[].model`。

交互式 `pachat chat` 现在会调用配置的本地 OpenAI-compatible provider，并使用 `agent.leader.model_id` 选择模型。当 `agent.planner.enabled` 为 true 且所选模型支持 `chat` 与 `json_schema` 时，同一个 Leader provider 也会接入 `BoundedModelPlanner`。Planner 输出受 `agent.planner.max_nodes` 约束，并会先通过 Runtime capability registry 与 DAG 依赖校验；引用未知 capability 或无效依赖时，会在创建 workflow 前失败。provider 连接或响应错误会明确返回。

Planner 配置：

```yaml
agent:
  planner:
    enabled: true
    max_nodes: 8
```

Workflow 中的 reasoning 与 synthesis 现在会在 `Runtime.ChatProvider` 可用时复用配置的私有本地模型。`reasoning.local` 输出有界 claims、evidence IDs、confidence 和 provider usage，不保存 chain-of-thought。`synthesis.local` 基于已验证 evidence 写出最终答案，`Runtime.Run()` 会使用完成的 synthesis checkpoint 作为持久化 task answer。

Hybrid RAG 生产接线由可挂载的 `rag` 配置控制。`rag.vector` 会组合 embedding 模型与 Qdrant 向量检索；`rag.reranker` 会在可用时组合配置的 rerank 模型。vector search 或 reranking 禁用/不可用时，检索会继续使用本地 BM25 fallback。

Runtime capability executor 现在通过本地 executor registry 注册。启用 browser 后，`browser.read`、`browser.navigate` 和 `browser.write` 会进入受治理的 browser tool；read observation 会作为 Runtime evidence 持久化，navigation 与 write action 仍经过 session/domain policy 和 confirmation gate。

启用的 coding backend 会注册为 `coding.<backend>` executor，包括 Codex 与 Claude 风格 adapter。Coding workflow node 必须提供结构化 JSON 输入，包含 `repository_path`、`prompt`、写入策略、privacy class 和可选 test commands；执行会委托给受治理的 coding agent Runner，并持久化 diff/test evidence。

配置的 MCP server 会注册为 `mcp.<server>.<tool>` executor，并通过 stdio JSON-RPC 调用。MCP 输出会作为 `UNTRUSTED OBSERVATION` evidence 持久化。配置的本地工具来自 `tools.allowlist`；执行时只使用固定 allowlisted program/args，不会把 workflow input 拼接进 shell 命令。

Public escalation 会从 model registry 中启用的 `public_remote` chat model 接线。如果 public provider 要求 Privacy Gateway，`privacy.hmac_secret_env` 必须能解析，否则 Runtime build 会 fail closed。包含 secret 的 payload 会在调用 provider 前被阻断。

可以使用 `pachat task usage --config <path> --id <task_id>` 查询 task usage。Usage 会从 workflow checkpoint 聚合到 node、workflow 和 task 层级。具备可靠 usage 的模型 provider 会使用 provider token count；外部 CLI/tool 在没有 token count 时会标记为 unknown。

Workflow engine 已启用 verification-driven early stop。当已完成节点的 claims 通过持久化 evidence 验证、满足 verification policy 且没有冲突时，scheduler 会取消剩余 pending work，usage 不会继续增长到被取消的节点之后。

P14 文档位于 `docs/release/`，覆盖 quickstart、用户操作、管理员操作、安全/threat model、troubleshooting、upgrade、RC E2E scenario、soak test、performance baseline 和 data-loss recovery drill。当前发布包采用 archive/script 形式；package manager 发布和 Windows 二进制属于 post-v1 工作。

### 验证

运行：

```sh
go test ./...
make build
make smoke
make release-build
make checksums
go run ./cmd/pachat release check --quick
```

### 默认安全策略

- 浏览器 session、cookie、token、密码和 profile 数据仅保留在本地。
- 公网 LLM 参与浏览器规划时，只能接收 Privacy Gateway 脱敏后的摘要。
- 所有浏览器动作执行前都必须经过 Permission Layer。
- 登录提交、支付、删除、发送消息、上传、OAuth 授权、接受法律条款等高风险动作需要用户确认或更高权限策略。
- Evidence Bus 写入前必须完成敏感信息脱敏。
- API 响应不会回显原始 task input，并会对 title、final answer、event 和 confirmation 字段中的 secret-like 文本做脱敏。
