# personal-agent

## English

AI agent for local user workflows.

This repository is a Go implementation of a local-first parallel personal agent. The current implementation includes the P0 foundation, P1 model/privacy foundation, P2 RAG/memory foundation, P3 orchestration/Sub-Agent foundation, P4 browser runtime foundation, P5 permissions/verification/cost foundation, and P6 API/E2E/hardening foundation: `pachat` CLI packaging, configuration loading, SQLite migrations, core agent/browser contracts, interactive local memory, long-task state tracking, model registry/policy contracts, OpenAI-compatible provider boundaries, a fail-closed Privacy Gateway for public remote model calls, local BM25 retrieval, RRF result fusion, Qdrant boundary code, context compression, working/episodic/semantic memory stores, cancellable DAG orchestration, dependency-aware parallel scheduling, Sub-Agent retry handling, early stop, ordered node lifecycle evidence events, a governed chromedp browser runtime boundary, global permission policy evaluation, confirmation audit records, claim/evidence verification, conflict detection, registry-backed cost accounting, local REST task APIs, confirmation approve/deny APIs, full mock-based local E2E coverage, and secret-leak regression fixtures.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, Evidence Bus records, model selection, provider enforcement, local retrieval/memory indexing, orchestration, semantic browser location, accessibility reads, screenshot capture, coordinate fallback, confirmation-gated browser execution, global permission decisions, verification gates, budget enforcement, and local HTTP handler wiring. It does not yet include cookie reading, password reading, production model caller wiring, CLI-connected RAG execution, CLI-connected browser execution, CLI-connected Sub-Agent task execution, CLI-connected verification, live model-call metering, or a packaged long-running HTTP server command.

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

Expected output:

```text
task_id=task_<generated_id> status=completed answer="No-op task completed."
```

What happens:

- The CLI loads and validates `configs/config.example.yaml`.
- SQLite opens at `./data/personal-agent.db`.
- Embedded migrations under `internal/storage/migrations` are applied.
- One row is inserted into the `tasks` table.
- The task is marked `completed` with a no-op answer.

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

Current CLI limitation: the command does not yet call models, run RAG as part of task execution, automate the browser, verify claims, meter live model calls, or execute Sub-Agents. P1-P5 add library boundaries and the local browser runtime adapter only; CLI/runtime wiring is planned for later phases.

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

本仓库是一个 Go 版本本地优先并行个人 Agent。当前实现包含 P0 基础层、P1 模型/隐私基础层、P2 RAG/记忆基础层、P3 编排/Sub-Agent 基础层、P4 浏览器运行时基础层、P5 权限/验证/成本基础层和 P6 API/E2E/加固基础层：`pachat` CLI 打包、配置加载、SQLite 迁移、核心 agent/browser 契约、交互式本地记忆、长任务状态跟踪、模型注册/策略契约、OpenAI-compatible provider 边界、面向 public remote 模型调用的 fail-closed Privacy Gateway、本地 BM25 检索、RRF 结果融合、Qdrant 边界、上下文压缩、working/episodic/semantic memory store、可取消 DAG 编排、依赖感知并行调度、Sub-Agent retry、early stop、有序节点生命周期 evidence event、受治理的 chromedp 浏览器运行时边界、全局权限策略评估、确认审计记录、claim/evidence 验证、冲突检测、基于 registry 的成本核算、本地 REST 任务 API、确认 approve/deny API、基于 mock 的完整本地 E2E 覆盖和 secret-leak 回归 fixture。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏、Evidence Bus 记录、模型选择、provider enforcement、本地检索/记忆索引、编排、语义浏览器定位、accessibility 读取、截图、坐标兜底、带确认 gate 的浏览器执行、全局权限决策、验证 gate、预算控制和本地 HTTP handler wiring 的 Go 契约与安全骨架。仓库暂不包含 cookie 读取器、密码读取器、生产模型调用接线、接入 CLI 任务执行的 RAG runtime、接入 CLI 的浏览器执行、接入 CLI 的 Sub-Agent 任务执行、接入 CLI 的 claim 验证、实时模型调用计量或打包后的常驻 HTTP server 命令。

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

预期输出：

```text
task_id=task_<generated_id> status=completed answer="No-op task completed."
```

执行过程：

- CLI 加载并校验 `configs/config.example.yaml`。
- SQLite 打开位置为 `./data/personal-agent.db`。
- 执行 `internal/storage/migrations` 中的内置迁移。
- 向 `tasks` 表写入一条记录。
- 将该任务标记为 `completed`，并写入 no-op answer。

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

当前 CLI 限制：该命令还不会调用模型、在任务执行中运行 RAG、自动化浏览器、验证 claims、计量实时模型调用或执行 Sub-Agent。P1-P5 只新增库层边界和本地浏览器 runtime adapter；CLI/runtime 接线会在后续阶段实现。

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
