# personal-agent

## English

AI agent for local user workflows.

This repository is a Go implementation of a local-first parallel personal agent. The current implementation includes the P0 foundation, P1 model/privacy foundation, P2 RAG/memory foundation, P3 orchestration/Sub-Agent foundation, P4 browser runtime foundation, P5 permissions/verification/cost foundation, P6 API/E2E/hardening foundation, P7 Runtime MVP first vertical slice, P8 External Coding Agent library foundation, P9 Capability/Skill/Workflow core slice, P10 proactive trigger/event core slice, the first P11 Runtime Integration Closure slice, the P12 Operator Experience core slice, and the P13 Reliability/Security/Observability core slice: `pachat` CLI packaging, configuration loading, SQLite migrations, core agent/browser contracts, interactive local memory, long-task state tracking, model registry/policy contracts, OpenAI-compatible provider boundaries, a fail-closed Privacy Gateway for public remote model calls, local BM25 retrieval, RRF result fusion, Qdrant boundary code, context compression, working/episodic/semantic memory stores, cancellable DAG orchestration, dependency-aware parallel scheduling, Sub-Agent retry handling, early stop, ordered node lifecycle evidence events, a governed chromedp browser runtime boundary, global permission policy evaluation, confirmation audit records, claim/evidence verification, conflict detection, registry-backed cost accounting, local REST task APIs, confirmation approve/deny APIs, full mock-based local E2E coverage, secret-leak regression fixtures, a unified local-first Runtime used by CLI/API task execution, governed External Coding Agent boundaries for Codex/Claude-style CLI backends, deterministic capability and skill registries, persistent workflow checkpoints, read-only MCP metadata adaptation, persistent proactive triggers, untrusted event envelopes, Runtime-owned workflow dispatch for local read-only capabilities, strict versioned config validation, local doctor diagnostics, knowledge/project operator commands, packaged local HTTP serving, backup/restore dry-run, retention cleanup, SQLite maintenance commands, structured log redaction, local runtime metrics, and JSON health/readiness responses.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, Evidence Bus records, model selection, provider enforcement, local retrieval/memory indexing, orchestration, semantic browser location, accessibility reads, screenshot capture, coordinate fallback, confirmation-gated browser execution, global permission decisions, verification gates, budget enforcement, local HTTP handler wiring, Runtime-connected local RAG/memory/verification synthesis, coding agent adapter selection, safe process execution, environment sanitization, Git worktree isolation, CLI adapter request mapping, coding evidence collection, repository privacy checks, trigger scheduling helpers, trigger state persistence, event deduplication, an in-memory event bus, and local operator tooling. It does not yet include cookie reading, password reading, production model caller wiring, CLI-connected browser execution, model-backed Leader DAG planning, API background task cancellation, live model-call metering, public model escalation, automatic Runtime dispatch to real Codex/Claude CLI processes from user-facing commands, a proactive background daemon, trigger CLI/API commands, notification delivery, long-term goal planning, a full web dashboard, SSE-backed dashboard events, persistent approval inbox storage, or filesystem watchers.

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
- `internal/api`: Local REST handler for task create/list/status/cancel/events and confirmation inspect/approve/deny.
- `internal/runtime`: Local-first Personal Agent Runtime that connects task execution to memory, RAG, evidence, verification, synthesis, and persisted workflow dispatch for local read-only capabilities.
- `internal/codingagent`: External Coding Agent contracts, registry, safe process executor, worktree isolation, Codex/Claude CLI adapters, environment sanitizer, and evidence validation.
- `internal/capability`: Capability descriptions, health states, deterministic policy resolver, and Runtime capability registration.
- `internal/skill`: Versioned Skill manifests, dependency/permission validation, registry, and skill-first matching.
- `internal/workflow`: Persistent workflow/node state, checkpoints, recovery, and pause/resume/cancel transitions.
- `internal/project`: Project context and allowlist policy persisted through SQLite.
- `internal/mcp`: Read-only MCP server/tool metadata and capability adaptation boundary.
- `internal/trigger`: Proactive trigger model, validation, persistence, schedule calculation, deduplication, debounce, cooldown, jitter, and backoff helpers.
- `internal/event`: Untrusted event envelope, in-memory bus, SQLite event store, and event deduplication.
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
bin/pachat serve --config configs/config.example.yaml
curl http://127.0.0.1:8787/healthz
curl http://127.0.0.1:8787/readyz
curl http://127.0.0.1:8787/metrics
bin/pachat backup create --config configs/config.example.yaml
bin/pachat storage integrity --config configs/config.example.yaml
```

Current CLI limitation: the command now runs local memory/RAG/verification synthesis through Runtime, but it does not yet automate the browser, execute model-backed Leader DAG planning, meter live provider calls, or use public model escalation.

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

P7 MVP acceptance covers the removal of the no-op task path, local-first evidence, zero public model usage when local evidence is enough, Runtime-backed CLI/API task execution, verification reports, persisted final answers, and secret-leak regression coverage. Remaining P7 increments cover model-backed Leader DAG planning, BrowserAgent execution, API background cancellation, public model escalation, and live token/cost accounting.

### P8 External Coding Agents

P8 is documented in `docs/projdocs/P0_P8_EXECUTION_PLAN.md`, `docs/p8_external_coding_agents.md`, and `docs/projdocs/task/P8.md`. The first library slice adds a governed External Coding Agent boundary for Codex CLI and Claude Code CLI style backends.

P8 includes configurable backend enablement, independent execution location and inference trust, capability-based backend selection with fallback, safe subprocess execution with timeout/cancellation, minimal environment sanitization, Git worktree isolation, Codex/Claude adapter request mapping, repository privacy denial for confidential repositories using public remote inference, and evidence validation requiring actual diff/files/tests instead of self-reported completion.

Current P8 limitation: the package is ready for Runtime integration, but `pachat run` and local REST APIs do not yet automatically dispatch coding task nodes to real Codex/Claude CLI processes.

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

The current P9 boundary persists workflow state and exposes safe lifecycle transitions. A full background worker, external MCP transport, and Skill-to-Scheduler execution remain follow-up increments.

### P10 Proactive Trigger/Event Core Slice

P10 is documented in `docs/p10_core_release_slice.md`, `docs/projdocs/P0_P10_EXECUTION_PLAN.md`, and `docs/projdocs/task/P10.md`. This first proactive slice adds persistent trigger definitions and state, one-shot/interval/simple-cron schedule calculation, deterministic execution keys, debounce, cooldown, jitter, exponential backoff helpers, untrusted event envelopes, event deduplication, and an in-memory event bus with SQLite persistence.

Current P10 boundary: the package provides the proactive foundation for later waves, but it does not yet create workflows from triggers, run a background scheduler, expose trigger CLI/API commands, deliver notifications, manage goals, or watch the filesystem.

### P11 Runtime Integration Closure Slice

P11 is documented in `docs/p11_runtime_integration_closure.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P11.md`. The first closure slice adds a Runtime-owned workflow engine that loads persisted workflow runs, recovers non-terminal nodes, checks project capability policy, checks registered capability availability, runs local read-only nodes through the existing scheduler, persists per-node checkpoints, and marks workflows completed, failed, or cancelled.

Current P11 boundary: persisted workflows can execute `memory.search`, `rag.search`, and `verification.verify` through Runtime dispatch. Full P11 completion still requires model-backed Leader planning, complete Scheduler replacement for the direct Runtime fast path, BrowserAgent/CodingAgent/MCP execution from user-facing commands, public model escalation, live provider usage metering, and Runtime-backed multi-turn chat.

### P12 Operator Experience Core Slice

P12 is documented in `docs/p12_operator_experience.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P12.md`. This core slice adds versioned strict config validation, `pachat init`, `pachat config validate`, `pachat doctor`, local knowledge indexing/list/status/reindex/remove commands, project create/list/show/update/archive/use commands, task/workflow JSON output, workflow checkpoint event inspection, a packaged local `pachat serve` command with health/readiness endpoints, backup create/restore dry-run, event retention cleanup, and SQLite integrity/vacuum/checkpoint maintenance commands.

Current P12 boundary: the operator CLI is functional for local SQLite-backed workflows, but the full P12 product surface still needs a web dashboard, SSE dashboard updates, persistent approval inbox storage wired into Runtime confirmations, real external service health probes, model discovery against live local endpoints, and a filesystem watcher daemon.

### P13 Reliability / Security / Observability Core Slice

P13 is documented in `docs/p13_reliability_security_observability.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P13.md`. This core slice adds centralized redaction for structured logs and API-facing text, in-process local runtime metrics, JSON `/healthz` and `/readyz` reports for `pachat serve`, a JSON `/metrics` endpoint, request/error/activity counters, and secret-leak regression coverage for observability output.

Current P13 boundary: this is not the full P13 release gate. OpenTelemetry tracing, global resource governance, disk pressure handling, full graceful shutdown checkpointing, crash/chaos tests, race/load hardening, prompt-injection fixtures, shell/path/network/API security hardening, external-agent/browser security suites, durable audit logs, vulnerability CI, golden evals, and measurable quality gates remain follow-up P13 work.

### Validation

Run:

```sh
go test ./...
make build
make smoke
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

本仓库是一个 Go 版本本地优先并行个人 Agent。当前实现包含 P0 基础层、P1 模型/隐私基础层、P2 RAG/记忆基础层、P3 编排/Sub-Agent 基础层、P4 浏览器运行时基础层、P5 权限/验证/成本基础层、P6 API/E2E/加固基础层、P7 Runtime MVP 第一条 vertical slice、P8 External Coding Agent 库层基础、P9 Capability/Skill/Workflow 核心 slice、P10 proactive trigger/event 核心 slice、第一条 P11 Runtime Integration Closure slice、P12 Operator Experience 核心 slice，以及 P13 Reliability/Security/Observability 核心 slice：`pachat` CLI 打包、配置加载、SQLite 迁移、核心 agent/browser 契约、交互式本地记忆、长任务状态跟踪、模型注册/策略契约、OpenAI-compatible provider 边界、面向 public remote 模型调用的 fail-closed Privacy Gateway、本地 BM25 检索、RRF 结果融合、Qdrant 边界、上下文压缩、working/episodic/semantic memory store、可取消 DAG 编排、依赖感知并行调度、Sub-Agent retry、early stop、有序节点生命周期 evidence event、受治理的 chromedp 浏览器运行时边界、全局权限策略评估、确认审计记录、claim/evidence 验证、冲突检测、基于 registry 的成本核算、本地 REST 任务 API、确认 approve/deny API、基于 mock 的完整本地 E2E 覆盖、secret-leak 回归 fixture、CLI/API 任务执行使用的统一本地优先 Runtime、面向 Codex/Claude 风格 CLI backend 的受治理 External Coding Agent 边界、确定性 capability/skill registry、持久化 workflow checkpoint、只读 MCP metadata adapter、持久化 proactive trigger、不可信 event envelope、由 Runtime 持有的本地只读 capability workflow dispatch、严格版本化配置校验、本地 doctor 诊断、knowledge/project 运维命令、打包本地 HTTP server、backup/restore dry-run、retention cleanup、SQLite maintenance 命令、structured log redaction、本地 runtime metrics 和 JSON health/readiness 响应。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏、Evidence Bus 记录、模型选择、provider enforcement、本地检索/记忆索引、编排、语义浏览器定位、accessibility 读取、截图、坐标兜底、带确认 gate 的浏览器执行、全局权限决策、验证 gate、预算控制、本地 HTTP handler wiring、接入 Runtime 的本地 RAG/Memory/Verification synthesis、coding agent adapter 选择、安全进程执行、环境脱敏、Git worktree 隔离、CLI adapter request mapping、coding evidence 收集、repository privacy 检查、trigger scheduling helper、trigger state persistence、event deduplication、内存 event bus 和本地运维工具。仓库暂不包含 cookie 读取器、密码读取器、生产模型调用接线、接入 CLI 的浏览器执行、模型驱动的 Leader DAG planning、API 后台任务取消、实时模型调用计量、public model escalation、从用户命令自动调度真实 Codex/Claude CLI 进程、proactive 后台 daemon、trigger CLI/API、通知投递、长期目标规划、完整 Web Dashboard、基于 SSE 的 Dashboard 实时事件、持久化 approval inbox 或文件系统 watcher。

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
- `internal/api`：本地 REST handler，支持 task create/list/status/cancel/events 和 confirmation inspect/approve/deny。
- `internal/runtime`：本地优先 Personal Agent Runtime，连接 task execution、memory、RAG、evidence、verification、synthesis 和本地只读 capability 的持久化 workflow dispatch。
- `internal/codingagent`：External Coding Agent 契约、registry、安全进程执行器、worktree 隔离、Codex/Claude CLI adapter、环境脱敏和 evidence 验证。
- `internal/capability`：Capability 描述、health 状态、确定性 policy resolver 和 Runtime capability 注册。
- `internal/skill`：版本化 Skill manifest、依赖/权限校验、registry 和 skill-first matching。
- `internal/workflow`：持久化 workflow/node 状态、checkpoint、recovery 和 pause/resume/cancel 状态转换。
- `internal/project`：通过 SQLite 持久化的 Project context 与 allowlist policy。
- `internal/mcp`：只读 MCP server/tool metadata 和 capability adapter 边界。
- `internal/trigger`：proactive trigger 模型、校验、持久化、调度计算、deduplication、debounce、cooldown、jitter 和 backoff helper。
- `internal/event`：不可信 event envelope、内存 event bus、SQLite event store 和 event deduplication。
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
bin/pachat serve --config configs/config.example.yaml
curl http://127.0.0.1:8787/healthz
curl http://127.0.0.1:8787/readyz
curl http://127.0.0.1:8787/metrics
bin/pachat backup create --config configs/config.example.yaml
bin/pachat storage integrity --config configs/config.example.yaml
```

当前 CLI 限制：该命令已经通过 Runtime 执行本地 memory/RAG/verification synthesis，但尚未自动化浏览器、执行模型驱动的 Leader DAG planning、计量实时 provider 调用或使用 public model escalation。

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

P7 MVP 验收覆盖移除 no-op task path、local-first evidence、本地证据足够时 public model usage 为零、Runtime-backed CLI/API task execution、verification report、持久化 final answer 和 secret-leak 回归覆盖。后续 P7 增量继续覆盖模型驱动 Leader DAG planning、BrowserAgent execution、API background cancellation、public model escalation 和实时 token/cost accounting。

### P8 External Coding Agents

P8 已记录在 `docs/projdocs/P0_P8_EXECUTION_PLAN.md`、`docs/p8_external_coding_agents.md` 和 `docs/projdocs/task/P8.md` 中。第一条库层 slice 增加了面向 Codex CLI 与 Claude Code CLI 风格 backend 的受治理 External Coding Agent 边界。

P8 包含可配置 backend enablement、独立的 execution location 与 inference trust、基于 capability 的 backend selection 与 fallback、安全 subprocess 执行及 timeout/cancellation、最小环境脱敏、Git worktree 隔离、Codex/Claude adapter request mapping、针对 confidential repository + public remote inference 的隐私拒绝，以及要求真实 diff/files/tests 而非 self-reported completion 的 evidence 验证。

当前 P8 边界：包已为 Runtime integration 准备好，但 `pachat run` 和本地 REST API 尚不会自动把 coding task node 调度到真实 Codex/Claude CLI 进程。

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

当前 P9 边界持久化 Workflow 状态并提供安全的生命周期转换；完整后台 worker、外部 MCP transport 和 Skill 到 Scheduler 的执行接线作为后续增量。

### P10 Proactive Trigger/Event 核心 Slice

P10 已记录在 `docs/p10_core_release_slice.md`、`docs/projdocs/P0_P10_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P10.md` 中。第一条 proactive slice 增加持久化 trigger definition/state、one-shot/interval/simple-cron 调度计算、确定性 execution key、debounce、cooldown、jitter、指数 backoff helper、不可信 event envelope、event deduplication，以及带 SQLite 持久化的内存 event bus。

当前 P10 边界：包已为后续 proactive wave 准备好基础能力，但尚未从 trigger 创建 workflow、运行后台 scheduler、暴露 trigger CLI/API、投递通知、管理长期目标或 watch 文件系统。

### P11 Runtime Integration Closure Slice

P11 已记录在 `docs/p11_runtime_integration_closure.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P11.md` 中。第一条 closure slice 增加 Runtime 持有的 workflow engine：它会加载持久化 workflow run，恢复非终态 node，检查 project capability policy，检查已注册 capability 可用性，通过现有 scheduler 执行本地只读 node，写入 per-node checkpoint，并将 workflow 标记为 completed、failed 或 cancelled。

当前 P11 边界：持久化 workflow 可以通过 Runtime dispatch 执行 `memory.search`、`rag.search` 和 `verification.verify`。完整 P11 仍需继续实现模型驱动 Leader planning、用 Scheduler 完整替代直接 Runtime fast path、从用户命令执行 BrowserAgent/CodingAgent/MCP、public model escalation、实时 provider usage metering，以及接入 Runtime 的多轮 chat。

### P12 Operator Experience 核心 Slice

P12 已记录在 `docs/p12_operator_experience.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P12.md` 中。本核心 slice 增加版本化严格配置校验、`pachat init`、`pachat config validate`、`pachat doctor`、本地 knowledge index/list/status/reindex/remove 命令、project create/list/show/update/archive/use 命令、task/workflow JSON 输出、workflow checkpoint event 查看、带 health/readiness endpoint 的本地 `pachat serve` 命令、backup create/restore dry-run、event retention cleanup，以及 SQLite integrity/vacuum/checkpoint maintenance 命令。

当前 P12 边界：运维 CLI 已可用于本地 SQLite-backed workflow，但完整 P12 产品体验仍需要 Web Dashboard、Dashboard SSE 实时更新、接入 Runtime confirmation 的持久化 approval inbox、真实外部服务健康探测、针对本地 endpoint 的模型发现，以及文件系统 watcher daemon。

### P13 Reliability / Security / Observability 核心 Slice

P13 已记录在 `docs/p13_reliability_security_observability.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P13.md` 中。本核心 slice 增加 structured log 与 API-facing text 的集中脱敏、进程内本地 runtime metrics、`pachat serve` 的 JSON `/healthz` 和 `/readyz`、JSON `/metrics` endpoint、请求/错误/活动请求计数，以及 observability 输出的 secret-leak 回归覆盖。

当前 P13 边界：这不是完整 P13 release gate。OpenTelemetry tracing、全局资源治理、磁盘压力处理、完整 graceful shutdown checkpoint、crash/chaos 测试、race/load 加固、prompt-injection fixture、shell/path/network/API 安全加固、external-agent/browser security suite、持久化 audit log、漏洞 CI、golden eval 和可度量 quality gate 仍是后续 P13 工作。

### 验证

运行：

```sh
go test ./...
make build
make smoke
```

### 默认安全策略

- 浏览器 session、cookie、token、密码和 profile 数据仅保留在本地。
- 公网 LLM 参与浏览器规划时，只能接收 Privacy Gateway 脱敏后的摘要。
- 所有浏览器动作执行前都必须经过 Permission Layer。
- 登录提交、支付、删除、发送消息、上传、OAuth 授权、接受法律条款等高风险动作需要用户确认或更高权限策略。
- Evidence Bus 写入前必须完成敏感信息脱敏。
- API 响应不会回显原始 task input，并会对 title、final answer、event 和 confirmation 字段中的 secret-like 文本做脱敏。
