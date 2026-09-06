# P0-P7 Execution Plan

This file converts the project documents into an implementation sequence for this repository. It is a local planning artifact under ignored `docs/`, so code commits remain focused on tracked source files.

## How To Use This Tool

The current runnable tool is the P0 CLI entrypoint. The packaged binary name is `pachat`:

```sh
go run ./cmd/pachat run --config configs/config.example.yaml --task "smoke test"
```

What it does today:

- Loads and validates `configs/config.example.yaml`.
- Opens the configured SQLite database.
- Applies embedded migrations from `internal/storage/migrations`.
- Creates one task row in the `tasks` table.
- Marks that task as completed with a no-op answer.
- Prints the task ID, status, and answer.
- Supports an interactive chat shell that stores local episodic memory.
- Supports long-task status commands for list/show/cancel.
- Includes a P4 library-level governed browser runtime boundary backed by chromedp.

Expected output shape:

```text
task_id=task_<generated_id> status=completed answer="No-op task completed."
```

The default config stores local runtime data under `./data`, including `./data/personal-agent.db`. The `data/` directory is ignored by git.

Use a custom task:

```sh
go run ./cmd/pachat run --config configs/config.example.yaml --task "summarize my local notes"
```

Use a custom config:

```sh
go run ./cmd/pachat run --config /path/to/config.yaml --task "your task"
```

Use interactive chat with local memory:

```sh
go run ./cmd/pachat chat --config configs/config.example.yaml
```

Inside chat:

- Type normal text to persist a user message and a local no-op assistant response.
- Use `/memory` to show recent episodic memory.
- Use `/exit` or `/quit` to leave.

Build the package:

```sh
make build
bin/pachat run --config configs/config.example.yaml --task "smoke test"
bin/pachat chat --config configs/config.example.yaml
```

Long-task state commands:

```sh
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```

Current limitation:

- P0 remains the CLI runner with local task state and episodic memory. It does not yet call models, run RAG, automate the browser from the CLI, verify claims, or execute Sub-Agents.
- P4 browser runtime code is available as a library boundary, but it is not wired into CLI task execution yet.
- Those capabilities are planned for P1-P7 below, with P7 wiring the runtime into real task execution.

Development workflow:

1. Pick the next phase from this file.
2. Implement only that phase's deliverables.
3. Run the validation commands listed for the phase.
4. Commit tracked source/config/README changes.
5. Keep `docs/` and `data/` local because both are ignored by git.

## P0: Foundation

Goal: make the repository runnable and testable as a local-first agent shell.

Deliverables:

- `configs/config.example.yaml` copied from the project spec and kept free of secrets.
- `internal/config` with YAML loading, environment indirection, duration parsing, and validation.
- `internal/storage` with SQLite opening, a mockable PostgreSQL connector boundary, embedded migrations, and a migration runner.
- Initial migrations for tasks, task nodes, evidence, claims, memory, documents, model usage, permission decisions, and browser session metadata.
- `internal/cli` and `cmd/pachat` with `pachat run --config ... --task ...`.
- `pachat chat --config ...` interactive shell with local episodic memory.
- `pachat task list/show/cancel --config ...` long-task state commands.
- No-op task execution that persists a completed task row.
- Tests for config validation, migration smoke, CLI startup smoke, chat smoke, memory persistence, and task status commands.
- README updates in English and Chinese.

Validation:

- `go test ./...`
- `go run ./cmd/pachat run --config configs/config.example.yaml --task "smoke test"`
- `go test ./...`
- `make build`

Commit boundary:

- One commit: `pachat` package, interactive P0 memory, and long-task state commands.

## P1: Model And Privacy Foundation

Goal: make model selection explicit and make public model calls fail closed without privacy filtering.

Deliverables:

- `internal/model` trust levels, capabilities, provider metadata, model registry, model policy, and mockable provider interfaces.
- OpenAI-compatible provider boundary with mockable transport.
- `internal/privacy` HMAC-SHA256 pseudonymization, redaction, credential dump blocking, and audit metadata.
- Public provider enforcement that requires Privacy Gateway.

Validation:

- HMAC stability tests.
- Redaction and blocking fixtures.
- Model capability/trust selection tests.
- Public provider bypass prevention tests.

Commit boundary:

- Split into registry/policy, privacy gateway, and provider enforcement commits if large.

## P2: RAG And Memory

Goal: provide local-first retrieval and three memory stores with traceable evidence.

Deliverables:

- `internal/rag` document/chunk model, chunker, local BM25, Qdrant client wrapper, RRF fusion, reranker interface, and context compressor.
- `internal/memory` working, episodic, and semantic memory stores.
- Semantic memory indexing hook into RAG.
- Evidence-preserving compression and retrieval result packaging.

Validation:

- BM25 ranking test.
- RRF deterministic ordering test.
- Qdrant integration test skipped cleanly when unavailable.
- Memory lifecycle tests.
- Context compression evidence preservation test.

Commit boundary:

- Separate commits for document/chunk storage, BM25/RRF, Qdrant boundary, memory stores, and compressor.

## P3: Orchestration And Parallel Sub-Agents

Goal: execute bounded work through a cancellable task DAG.

Deliverables:

- `internal/orchestrator` DAG nodes, validation, dependency-aware scheduler, retry policy, early stop, and cancellation propagation.
- `internal/agent` LeaderAgent/SubAgent contracts, roles, structured results, claims, evidence IDs, usage, and error categories.
- Evidence Bus integration for node lifecycle events.

Validation:

- DAG cycle rejection.
- Ready-node calculation.
- Parallel scheduling overlap test.
- Cancellation propagation test.
- Early stop test.
- Evidence ordering and persistence tests.

Commit boundary:

- Separate commits for DAG, SubAgent contracts, scheduler, and early stop/evidence integration.

## P4: Browser Runtime

Goal: connect existing browser contracts to a real local runtime while preserving privacy and permission boundaries.

Status: completed locally on 2026-09-06. See `docs/projdocs/task/P4.md`.

Deliverables:

- `internal/browser/runtime_adapter.go`
- `internal/browser/chromedp_adapter.go`
- `internal/browser/profile_store.go`
- `internal/browser/locator.go`
- `internal/browser/a11y.go`
- `internal/browser/screenshot.go`
- `internal/browser/coordinate_fallback.go`
- `internal/browser/confirmation.go`
- Local test pages under `testdata/browser`.
- Browser evidence publishing to global Evidence Bus.

Validation:

- Runtime adapter mock tests.
- Local browser E2E tests.
- Session reuse policy tests.
- Coordinate fallback tests.
- Domain allowlist denial tests.

Commit boundary:

- Separate commits for runtime adapter, semantic locator, screenshot/A11y reads, input actions, and confirmation/evidence integration.

## P5: Permissions, Verification, And Cost

Goal: enforce side-effect policy, verify claims, and account for model/runtime usage.

Status: completed locally on 2026-09-06. See `docs/projdocs/task/P5.md`.

Deliverables:

- `internal/permission` global policy, decisions, confirmation workflow, evaluator, and audit records.
- `internal/verification` claim extraction, claim/evidence coverage, conflict detection, and confidence policy.
- `internal/cost` budgets, usage records, pricing lookup from config/registry, soft and hard limit enforcement.

Validation:

- Permission decision tests.
- Confirmation flow integration test.
- Claim/evidence coverage tests.
- Conflict fixture tests.
- Budget soft/hard limit tests.

Commit boundary:

- Separate commits for permission layer, verification, and cost accounting.

## P6: API, E2E, And Hardening

Goal: expose usable APIs and prove the integrated local-first flow works.

Status: completed locally on 2026-09-06. See `docs/projdocs/task/P6.md`.

Deliverables:

- `internal/api` REST task create/status/cancel/events and confirmation approve/deny endpoints.
- Full local E2E using provider mocks, SQLite, local test pages, RAG, memory, browser, verification, and final synthesis.
- Optional PostgreSQL mode integration tests.
- Secret-leak regression fixtures and operational hardening.

Validation:

- REST API tests.
- CLI E2E test.
- Full agent E2E with mocks.
- Browser high-risk denial E2E.
- Logs/evidence secret-leak regression tests.
- PostgreSQL integration tests skipped cleanly when unavailable.

Commit boundary:

- Separate commits for API surface, confirmation endpoints, full E2E, and hardening.

## P7: Runtime Integration / Personal Agent MVP


### 1. P7 核心目标

P7 的唯一核心目标：

> 将 P0～P6 已实现的 Model、Privacy、RAG、Memory、Orchestrator、Browser、Permission、Verification、Cost、API 等组件连接成真正可以执行任务的 Personal Agent Runtime。

P7 完成后：

```bash
pachat run \
  --config configs/config.yaml \
  --task "从我的本地资料中找出以前关于 NFS 性能问题的记录，并总结处理方法"
```

不能再返回：

```text
No-op task completed.
```

而必须实际执行：

```text
User Query
    ↓
Local-first Router
    ↓
Memory + Local RAG
    ↓
Evidence
    ↓
是否足够？
 ┌──┴───┐
YES     NO
 │       │
 │     Leader Agent
 │       ↓
 │    Task DAG
 │       ↓
 │   Sub-Agents
 │       ↓
 └──→ Evidence Bus
         ↓
     Verification
         ↓
      Early Stop
         ↓
      Synthesis
         ↓
      Final Answer
```

---

### 2. P7 不再新增大规模 Foundation

P7 原则：

```text
Reuse P0-P6
Integrate
Execute
Observe
Optimize
```

禁止为了实现 P7 再额外引入大量新的 abstraction/framework。

已有组件应直接复用：

```text
internal/model
internal/privacy
internal/rag
internal/memory
internal/orchestrator
internal/browser
internal/permission
internal/verification
internal/cost
internal/storage
internal/api
```

P7 主要新增：

```text
internal/runtime
```

以及真正的 Agent 实现。

---

### 3. 新增目录

建议：

```text
internal/

├── runtime/
│   ├── runtime.go
│   ├── builder.go
│   ├── runner.go
│   ├── router.go
│   ├── state.go
│   ├── context.go
│   ├── early_stop.go
│   ├── evidence.go
│   └── runtime_test.go
│
├── agents/
│   ├── leader.go
│   ├── retrieval.go
│   ├── memory.go
│   ├── reasoning.go
│   ├── browser.go
│   ├── verification.go
│   ├── synthesis.go
│   └── agents_test.go
│
└── evidence/
    ├── types.go
    ├── bus.go
    ├── store.go
    ├── scorer.go
    └── conflict.go
```

如果不希望新增 `internal/agents`，也可以放到：

```text
internal/agent/impl/
```

但必须区分：

```text
Agent Contract
```

和：

```text
Agent Implementation
```

---

### 4. Runtime 核心对象

建议新增：

```go
type Runtime struct {
    Router       Router
    Leader       agent.LeaderAgent

    Scheduler    *orchestrator.Scheduler

    Memory       MemoryService
    RAG          RetrievalService

    Evidence     EvidenceStore
    Verifier     VerificationService

    Permission   PermissionService
    Cost         CostController

    Models       *model.Registry

    Browser      BrowserService
}
```

统一入口：

```go
func (r *Runtime) Run(
    ctx context.Context,
    req RunRequest,
) (*RunResult, error)
```

其中：

```go
type RunRequest struct {
    TaskID       string
    Input        string
    PrivacyClass string

    LeaderModelID string

    MaxIterations int
    MaxTokens     int
    MaxCostUSD    float64
}
```

输出：

```go
type RunResult struct {
    TaskID string

    Answer string

    Confidence float64

    EvidenceIDs []string

    Usage UsageSummary

    EarlyStopped bool

    Verification verification.Report
}
```

---

### 5. Runtime Builder

新增：

```go
func Build(
    ctx context.Context,
    cfg config.Config,
) (*Runtime, error)
```

负责初始化：

```text
Config
 ↓
Storage
 ↓
Privacy Gateway
 ↓
Model Registry
 ↓
Model Providers
 ↓
RAG
 ↓
Memory
 ↓
Permission
 ↓
Browser
 ↓
Verification
 ↓
Cost
 ↓
Agents
 ↓
Orchestrator
 ↓
Runtime
```

禁止 CLI/API 自己逐个拼组件。

所有运行时依赖都统一从：

```text
runtime.Builder
```

构建。

---

### 6. Local-first Router

这是 P7 最重要的组件之一。

新增：

```go
type RouteType string

const (
    RouteDirect    RouteType = "direct"
    RouteMemory    RouteType = "memory"
    RouteRAG       RouteType = "rag"
    RouteTool      RouteType = "tool"
    RouteBrowser   RouteType = "browser"
    RouteAgent     RouteType = "agent"
    RouteMixed     RouteType = "mixed"
)
```

执行优先级：

```text
1 Local Memory
2 Local RAG
3 Local Tool
4 Local Model
5 Internal Model
6 Public Model
```

不能默认：

```text
User → Leader LLM
```

而应该：

```text
User
 ↓
Cheap Router
 ↓
Local retrieval
```

---

### 7. Local-first 快速路径

对于：

```text
“上次 NFS 延迟怎么处理的？”
```

应执行：

```text
Memory Search
      +
Local RAG
      ↓
Evidence
      ↓
Evidence sufficient?
      ↓
YES
      ↓
Local Synthesis
```

不调用公网 Leader。

必须支持：

```go
if localEvidenceEnough {
    return localSynthesis()
}
```

这是节省 Token 的核心机制。

---

### 8. Leader Agent 真正实现

当前只有 Leader contract。

P7 必须新增真实实现：

```go
type ModelLeader struct {
    Models *model.Registry

    ModelPolicy model.Policy
}
```

需要实现：

```go
Plan(...)
Synthesize(...)
```

Leader Plan 输出必须是结构化 JSON，不允许通过自由文本解析 Task DAG。

例如：

```json
{
  "goal": "分析 node03 性能问题",
  "tasks": [
    {
      "id": "memory",
      "role": "memory",
      "query": "node03 历史性能问题",
      "dependencies": []
    },
    {
      "id": "rag",
      "role": "retrieval",
      "query": "NFS simv Verdi performance",
      "dependencies": []
    },
    {
      "id": "reason",
      "role": "reasoning",
      "dependencies": [
        "memory",
        "rag"
      ]
    }
  ]
}
```

Leader 不允许直接决定任意模型 endpoint。

模型必须通过：

```text
Model Policy
→ Registry
→ Provider
```

选择。

---

### 9. Agent Role 扩展

建议将当前 Role 扩展成：

```text
leader

retrieval
memory
reasoning
browser
tool
verification
synthesis
```

旧的：

```text
research
```

可以兼容，但不要继续作为万能角色。

---

### 10. Retrieval Sub-Agent

实现：

```go
type RetrievalAgent struct {
    RAG RetrievalPipeline
}
```

输入：

```text
query
topK
task context
```

执行：

```text
BM25
  +
Embedding
  +
Qdrant
  ↓
RRF
  ↓
Rerank
  ↓
Compression
```

输出必须是：

```text
Evidence
```

而不是长篇自然语言。

---

### 11. RAG Pipeline

当前已有：

```text
BM25
Qdrant
RRF
Compressor
```

P7 必须增加统一入口：

```go
type Pipeline interface {
    Retrieve(
        ctx context.Context,
        query string,
        opts RetrieveOptions,
    ) ([]Evidence, error)
}
```

实际路径：

```text
Query
 ├─ BM25
 └─ Embed
      ↓
    Qdrant
      ↓
     RRF
      ↓
   Reranker
      ↓
 Compressor
      ↓
 Evidence
```

如果 Qdrant 不可用：

```text
fallback → BM25
```

不应导致整个 Agent 失败。

---

### 12. Memory Sub-Agent

实现：

```go
type MemoryAgent struct {
    Working  WorkingStore
    Episodic EpisodicStore
    Semantic SemanticStore
}
```

查询优先顺序：

```text
Semantic
 ↓
Episodic
 ↓
Vectorized memory
```

输出同样必须转换成统一 Evidence。

---

### 13. Reasoning Sub-Agent

Reasoning Agent 负责：

```text
已有 Evidence
      ↓
判断
      ↓
产生新 Claim
```

但不能重复全文 Context。

输入：

```text
Goal
Relevant Evidence IDs
Compressed Evidence
```

输出：

```go
agent.Result{
    Claims: ...
    EvidenceIDs: ...
}
```

不允许输出完整 Chain-of-Thought。

仅保留：

```text
decision_summary
claims
recommended_next_action
confidence
```

---

### 14. Browser Sub-Agent

Browser Agent：

```text
Leader
 ↓
Browser Task
 ↓
Permission
 ↓
Session
 ↓
Governed Browser Tool
 ↓
chromedp
 ↓
Evidence
```

P7 必须把现有 Browser Runtime 真正接进 Agent。

最少支持：

```text
navigate
read visible text
read DOM
read accessibility tree
locate
click
mouse
keyboard
screenshot
```

优先：

```text
semantic locator
```

失败才：

```text
coordinate fallback
```

---

### 15. Session Reuse

P7 应完成：

```text
ProfileStore
      ↓
Session
      ↓
Chromedp allocator
```

例如：

```go
chromedp.UserDataDir(session.ProfileDir)
```

或者支持：

```text
Existing Chrome CDP
```

配置：

```yaml
browser:
  profile_reuse:
    enabled: true
    profile_dir_env: PERSONAL_AGENT_BROWSER_PROFILE_DIR
```

Session/Profile/Cookie：

```text
只能本地使用
不得进入 Model Context
不得进入 Evidence raw content
不得发送公网
```

---

### 16. Global Evidence Model

P7 应统一不同模块目前分散的 evidence。

新增：

```go
type Evidence struct {
    ID string

    TaskID string
    NodeID string

    Claim string

    SourceType SourceType
    SourceID string

    Content string

    Score float64

    Trust float64

    PrivacyClass string

    CreatedAt time.Time
}
```

SourceType：

```text
user
rag
memory
browser
tool
model
system
```

---

### 17. Evidence Store

建议第一版直接 SQLite。

新增：

```go
type EvidenceStore interface {
    Put(ctx context.Context, evidence Evidence) error

    ListByTask(
        ctx context.Context,
        taskID string,
    ) ([]Evidence, error)

    Get(
        ctx context.Context,
        evidenceID string,
    ) (Evidence, error)
}
```

Sub-Agent 之间只传：

```text
EvidenceID
```

必要时再查询 EvidenceStore。

减少 Context Copy。

---

### 18. Verification → Early Stop

P7 必须把两个现有模块正式连接。

现在已有：

```text
Verification
+
Scheduler EarlyStop
```

P7 变成：

```text
Sub-Agent Results
      ↓
Claim Extraction
      ↓
Evidence Verification
      ↓
Verification Report
      ↓
Stop Policy
```

推荐 Early Stop 条件：

```text
Coverage >= 0.85
AND
ConflictCount == 0
AND
CriticalClaimsSupported == true
AND
Confidence >= 0.85
```

满足：

```go
cancelAll()
```

立即停止剩余 Sub-Agent。

---

### 19. Early Stop 必须真正节约 Token

要求所有：

```text
Model request
Retriever
Browser
Tool
Sub-Agent
```

透传：

```go
context.Context
```

一旦：

```go
cancel()
```

必须停止：

```text
LLM Stream
HTTP request
Qdrant search
Browser action
Tool execution
```

不能只停止 Scheduler 等待。

---

### 20. 修正 Browser cancellation

现有 Chromedp Runtime 要保证：

```text
Scheduler ctx cancelled
 ↓
chromedp operation cancelled
```

不要只：

```text
caller return
```

但 CDP 动作仍在后台执行。

P7 必须增加对应 regression test。

---

### 21. Cost / Token 实时接线

现有：

```text
cost.Estimator
cost.Enforcer
```

要真正接 Model usage。

运行流程：

```text
Model Call
 ↓
response.Usage
 ↓
Cost Estimate
 ↓
Usage Accountant
 ↓
Budget
```

新增：

```go
type RunBudget struct {
    MaxInputTokens int
    MaxOutputTokens int

    SoftCostUSD float64
    HardCostUSD float64

    UsedInputTokens int
    UsedOutputTokens int
    UsedCostUSD float64
}
```

---

### 22. Budget 与 Scheduler 联动

启动 Agent 前：

```text
Budget.Check()
```

如果预计下一任务：

```text
超过 Hard Limit
```

则禁止启动。

Soft Limit：

```text
优先停止低优先级任务
禁用 Public Model fallback
减少 MaxOutputTokens
```

Hard Limit：

```text
直接 Cancel
```

---

### 23. Leader / Sub-Agent 模型独立配置

必须保持之前确定的设计。

例如：

```yaml
agent:

  leader:
    model_id: internal-main

  subagents:

    retrieval:
      model_id: local-small

    memory:
      model_id: local-small

    reasoning:
      model_id: local-reasoner

    browser:
      model_id: local-small

    verification:
      model_id: internal-fast

    synthesis:
      model_id: internal-main
```

不能退化为：

```yaml
model: xxx
```

全 Agent 共用一个模型。

---

### 24. Public Model Escalation

默认：

```text
Sub-Agent
allow_public = false
```

Leader：

```text
allow_public = configurable
```

升级条件：

```text
local evidence insufficient
OR
verification failed
OR
conflict exists
OR
reasoning complexity high
```

执行：

```text
Context Builder
 ↓
Minimum Necessary Context
 ↓
Privacy Gateway
 ↓
Public OpenAI-compatible Model
```

---

### 25. Public LLM 强制 Privacy Gateway

禁止任何 Public Provider 绕过：

```text
Privacy Gateway
```

需要加入 Runtime 级检查：

```go
if model.TrustLevel == PublicRemote &&
   privacyGateway == nil {
    return error
}
```

Public Model 永远不能接收：

```text
raw browser profile
cookie
password
token
API key
private key
full shell dump
raw credential files
```

---

### 26. CLI 真正执行 Runtime

P7 必须修改：

```text
internal/cli/run.go
```

现在：

```text
No-op task completed.
```

删除。

替换成：

```go
runtime, err := runtime.Build(...)
result, err := runtime.Run(...)
```

最终：

```bash
pachat run \
  --config configs/config.yaml \
  --task "..."
```

输出：

```text
task_id=xxx
status=completed
confidence=0.91
remote_tokens=0

answer:
...
```

---

### 27. Chat 也开始使用 Runtime

目前 chat：

```text
Recorded message in local memory.
```

P7 建议改成：

```text
User
 ↓
Runtime.Run()
 ↓
Answer
 ↓
append episodic memory
```

但可以作为 P7 后半阶段。

优先保证：

```text
pachat run
```

先工作。

---

### 28. API 真正启动 Agent

现在：

```text
POST /tasks
```

主要只是创建状态。

P7 修改：

```text
POST /tasks
      ↓
Create Task
      ↓
Task Runner
      ↓
Runtime.Run()
```

Long task：

```text
status=running
```

执行结束：

```text
status=completed
final_answer=...
final_confidence=...
```

失败：

```text
status=failed
```

---

### 29. Background Task Runner

API 需要：

```go
type TaskRunner struct {
    Runtime *runtime.Runtime
}
```

维护：

```text
task ID
→ cancel func
```

从而：

```http
POST /tasks/{id}/cancel
```

真正能：

```text
cancel Agent Runtime
```

而不是仅更新 DB 状态。

---

### 30. Event Stream

P7 可以先保留：

```text
GET /tasks/{id}/events
```

至少事件应包含：

```text
task.started
router.completed
rag.started
rag.completed
agent.started
agent.completed
verification.completed
early_stop
model.called
model.cancelled
task.completed
```

未来再考虑 SSE/WebSocket。

---

### 31. Permission 全局接线

所有 side-effect action 必须：

```text
Agent
 ↓
Permission Evaluator
 ↓
Allow / Deny / Confirm
```

Browser 已有自己的治理机制，P7 应逐渐统一到 global permission。

特别是：

```text
login submit
send message
delete
upload
payment
OAuth
legal acceptance
```

都不能直接执行。

---

### 32. Memory Write Permission

建议：

```text
普通 Episodic memory
→ auto allow
```

但：

```text
永久 Semantic preference 修改
```

可根据 policy：

```text
auto / confirm
```

配置。

---

### 33. P7 测试要求

至少增加以下测试。

##### Local-only RAG

输入：

```text
已有本地文档能够回答问题
```

断言：

```text
Public Model Calls == 0
```

这是 P7 最重要测试之一。

---

##### Parallel Agent

启动：

```text
3 SubAgents
```

断言至少两个出现执行时间 overlap。

---

##### Early Stop

设置：

```text
Agent A + B 已满足 Verification
```

断言：

```text
Agent C context cancelled
Agent D context cancelled
```

并验证：

```text
C/D token usage 不继续增长
```

---

##### Privacy

Public Model Mock 接收到的 Prompt：

```text
不得出现：
username raw
hostname raw
password
token
cookie
private key
```

---

##### Browser Permission

高风险：

```text
delete account
```

必须产生：

```text
confirmation required
```

且 Runtime 不执行。

---

##### Browser cancellation

执行慢 Browser action：

```text
cancel ctx
```

确认真正取消底层 CDP 动作。

---

##### Cost

设置：

```text
HardLimitUSD
```

运行多 Agent。

达到阈值后：

```text
不再启动新 Model calls
```

---

##### Verification

制造冲突：

```text
Evidence A supports
Evidence B contradicts
```

断言：

```text
EarlyStop == false
```

---

### 34. P7 第一条 Vertical Slice

第一阶段不要先做 Browser。

先打通：

```text
CLI
 ↓
Runtime
 ↓
Memory
 ↓
RAG
 ↓
Reasoner
 ↓
Verification
 ↓
Answer
```

验收：

```bash
pachat run \
 --config configs/config.yaml \
 --task "找出本地知识库里的 NFS 性能故障记录并总结"
```

要求：

```text
Local RAG hit
Evidence generated
Answer generated
Verification passed
Public calls = 0
```

---

### 35. P7 第二条 Vertical Slice

增加：

```text
Leader
 ↓
Task DAG
 ↓
Parallel SubAgents
```

测试任务：

```text
结合历史记录和知识库分析 node03 性能问题
```

并行：

```text
Memory Agent
Retrieval Agent
Reasoning Agent
```

---

### 36. P7 第三条 Vertical Slice

增加 Browser：

```text
Leader
 ↓
BrowserAgent
 ↓
Governed Tool
 ↓
Chromedp
```

任务：

```text
打开已经登录的网站并读取指定页面信息
```

要求复用本地 Session。

---

### 37. P7 第四条 Vertical Slice

增加公网模型 Escalation。

场景：

```text
Local model insufficient
```

执行：

```text
Privacy Gateway
 ↓
Public OpenAI-compatible API
```

断言：

```text
Sensitive Data Leak = 0
```

---

### 38. P7 Definition of Done

P7 完成必须同时满足：

##### Runtime

```text
pachat run
```

真正运行 Agent，而不是 No-op。

##### Local-first

本地 Evidence 足够时：

```text
Public calls = 0
```

##### Leader

真实模型可生成 Task DAG。

##### Sub-Agent

至少：

```text
retrieval
memory
reasoning
verification
browser
```

有真实实现。

##### Parallel

DAG 能并发执行无依赖节点。

##### Early Stop

Verification 达标后：

```text
context.Cancel()
```

真正停止其他任务和模型调用。

##### RAG

至少：

```text
BM25 + Vector + RRF + Compressor
```

形成统一 Pipeline。

##### Memory

Working/Episodic/Semantic 可进入 Runtime。

##### Browser

能真实：

```text
navigate
read
click
mouse
keyboard
```

并受 Permission 控制。

##### Privacy

Public Model 所有请求强制经过 Privacy Gateway。

##### Cost

真实 Model usage 进入 Budget。

##### Verification

Final Answer 必须有 Verification Report。

##### API

```text
POST /tasks
```

能够实际触发 Runtime。

##### Cancel

```text
POST /tasks/{id}/cancel
```

能真正取消 Runtime。

##### Security

Secret leak regression 全部通过。

---

### 39. P7 建议拆成 Commit / PR

#### P7-01

```text
Runtime Builder + Runtime State
```

#### P7-02

```text
Global Evidence Model
```

#### P7-03

```text
Hybrid RAG Pipeline
```

#### P7-04

```text
Memory + Retrieval SubAgents
```

#### P7-05

```text
Reasoning + Verification SubAgents
```

#### P7-06

```text
Model-backed Leader Agent
```

#### P7-07

```text
Runtime → Orchestrator integration
```

#### P7-08

```text
Verification-based Early Stop
```

#### P7-09

```text
Live Token/Cost Accounting
```

#### P7-10

```text
BrowserAgent + Session Reuse
```

#### P7-11

```text
CLI → Runtime
```

#### P7-12

```text
API → Runtime + real cancellation
```

#### P7-13

```text
Public Model Escalation + Privacy regression
```

#### P7-14

```text
Full Personal Agent E2E
```

---

### 40. P7 最终架构

```text
                         User
                          │
                    CLI / REST API
                          │
                          ▼
                  Personal Runtime
                          │
                  Local-first Router
                          │
            ┌─────────────┼─────────────┐
            │             │             │
         Memory          RAG           Tool
            │             │             │
            └─────────────┼─────────────┘
                          │
                    Enough Evidence?
                     ┌────┴────┐
                    YES       NO
                     │         │
                     │      Leader
                     │         │
                     │      Task DAG
                     │         │
                     │    Orchestrator
                     │         │
            ┌────────┼─────────┼────────┐
            │        │         │        │
         Memory   Retrieval  Browser  Reasoning
          Agent     Agent     Agent     Agent
            │        │         │        │
            └────────┴────┬────┴────────┘
                          │
                    Evidence Store
                          │
                     Verification
                          │
                 ┌────────┴─────────┐
                 │                  │
             Good Enough         Not Enough
                 │                  │
             cancel()          More work /
                 │             Escalation
                 │                  │
                 │           Privacy Gateway
                 │                  │
                 │             Public Model
                 │
                 ▼
              Synthesis
                 │
             Cost Record
                 │
             Final Answer
```

---

### P7 最重要的验收原则

P7 最核心不是：

> “能调用很多 Agent。”

而是以下四件事：

```text
Local-first
+
Evidence-driven
+
Cancellable Parallel Execution
+
Privacy-preserving Escalation
```

其中第一条硬性验收应当是：

```text
当本地 RAG / Memory 足够回答问题时：
Public Model Call Count MUST == 0
```

第二条硬性验收：

```text
当 Verification 达到 Early Stop 标准时：
所有剩余 Sub-Agent MUST 被取消，
且不得继续增加 Token Usage。
```

第三条硬性验收：

```text
所有 Public Model 请求 MUST 经过 Privacy Gateway。
```

第四条硬性验收：

```text
pachat run 不再存在 No-op Answer。
```

P7 完成后，项目才正式从：

```text
Personal Agent Framework
```

升级为：

```text
Personal Agent MVP
```

---

## P8: External Coding Agents / Developer Agent Ecosystem

Status: library foundation completed locally on 2026-09-06. See `docs/projdocs/task/P8.md`.

### 1. P8 核心目标

P8 在 P7 Personal Agent MVP 完成之后实施。

P8 的核心目标：

> 允许 Personal Agent 的 Sub-Agent 调用用户本机已经安装和配置好的 Codex CLI、Claude Code CLI 等专业 Coding Agent，并将它们作为受 Personal Agent Runtime 统一调度、权限控制、预算控制、Evidence 验证和取消机制管理的 External Coding Agent Runtime。

第一期必须支持：

```text
Codex CLI
Claude Code CLI
```

未来可以扩展：

```text
Aider
Gemini CLI
OpenCode
内部 Coding CLI
自研 Coding Agent
其他兼容 Adapter
```

P8 不改变 P7 的核心编排关系：

```text
Personal Agent
      │
      ▼
Leader Agent
      │
      ▼
Orchestrator
      │
 ┌────┼───────────────┐
 │    │               │
RAG  Browser       Coding Agent
                    │
             ┌──────┴──────┐
             │             │
          Codex         Claude Code
             │             │
          Local CLI      Local CLI
```

Codex / Claude Code 在 P8 中属于：

```text
External Coding Agent Runtime
```

或：

```text
Sub-Agent Execution Backend
```

而不是普通 `Model Provider`。

---

### 2. P8 设计原则

P8 必须遵循：

```text
Personal Agent remains the orchestrator.
```

禁止：

```text
Leader
 ↓
Codex / Claude
 ↓
External Agent 完全接管 Personal Agent Runtime
```

正确方式：

```text
Leader
 ↓
Task Slice
 ↓
CodingSubAgent
 ↓
CodingAgentAdapter
 ↓
Codex / Claude
 ↓
Structured Result
 ↓
Evidence Store
 ↓
Verification
 ↓
Leader / Runtime
```

External Coding Agent 只能处理被分配给它的任务切片。

---

### 3. 本地 CLI 与推理 Trust 必须分离

必须明确：

> 本地运行 CLI 不代表模型推理发生在本地。

因此新增两个独立属性：

```go
type ExecutionLocation string

const (
    ExecutionLocal  ExecutionLocation = "local"
    ExecutionRemote ExecutionLocation = "remote"
)

type InferenceTrust string

const (
    InferenceLocalPrivate  InferenceTrust = "local_private"
    InferenceTrustedRemote InferenceTrust = "trusted_remote"
    InferencePublicRemote  InferenceTrust = "public_remote"
)
```

例如：

```text
Codex CLI
ExecutionLocation = local
InferenceTrust     = public_remote
```

Claude Code 同样必须独立配置。

Runtime 不允许通过 executable path 推断 trust level。

---

### 4. 新增 Agent Roles

在 P7 的：

```text
leader
retrieval
memory
reasoning
browser
tool
verification
synthesis
```

基础上新增：

```text
coding
code_review
code_test
code_debug
```

建议：

```go
const (
    RoleCoding     Role = "coding"
    RoleCodeReview Role = "code_review"
    RoleCodeTest   Role = "code_test"
    RoleCodeDebug  Role = "code_debug"
)
```

后续可以继续扩展：

```text
code_explore
code_refactor
code_document
code_security_review
```

---

### 5. 新增项目目录

建议：

```text
internal/

├── codingagent/
│   ├── types.go
│   ├── adapter.go
│   ├── registry.go
│   ├── executor.go
│   ├── process.go
│   ├── workspace.go
│   ├── result.go
│   ├── evidence.go
│   ├── permission.go
│   ├── environment.go
│   ├── health.go
│   └── codingagent_test.go
│
├── codingagent/adapters/
│   ├── codex/
│   │   ├── adapter.go
│   │   ├── config.go
│   │   ├── parser.go
│   │   ├── health.go
│   │   └── adapter_test.go
│   │
│   └── claude/
│       ├── adapter.go
│       ├── config.go
│       ├── parser.go
│       ├── health.go
│       └── adapter_test.go
│
└── runtime/agents/
    └── coding.go
```

禁止在 Runtime 中直接：

```go
exec.Command("codex", ...)
exec.Command("claude", ...)
```

必须经过 Adapter。

---

### 6. CodingAgent Adapter Contract

统一接口：

```go
type CodingAgent interface {
    ID() string

    Capabilities(ctx context.Context) ([]Capability, error)

    Health(ctx context.Context) HealthStatus

    Execute(ctx context.Context, req Request) (*Result, error)
}
```

请求：

```go
type Request struct {
    TaskID string
    NodeID string

    Goal string

    Workspace string

    InputFiles []string
    EvidenceIDs []string

    Constraints Constraints
    Budget Budget

    PermissionContext PermissionContext
}
```

Adapter 必须只接收当前任务所需的最小上下文。

---

### 7. Structured Result

Codex / Claude CLI 的 stdout/stderr 不允许直接成为最终答案。

统一转换为：

```go
type Result struct {
    AgentID string

    Summary string

    FilesChanged []FileChange
    CommandsRun []CommandRecord
    Tests []TestResult

    Claims []agent.Claim
    EvidenceIDs []string

    GitDiff string

    ExitCode int
    Usage Usage
    Duration time.Duration
}
```

Personal Agent Runtime 根据：

```text
Result
+
Git Diff
+
Tests
+
Verification
```

决定：

```text
accepted
needs_review
retry
fallback
conflict
failed
```

---

### 8. Coding Agent Registry

类似 Model Registry，新增：

```go
type Registry struct {
    agents map[string]CodingAgent
}
```

职责：

```text
register
lookup
health
capability matching
availability
backend selection
fallback selection
```

配置示例：

```yaml
coding_agents:

  codex-local:
    type: codex
    enabled: true
    executable: codex
    execution_location: local
    inference_trust: public_remote
    workspace:
      isolation: git_worktree
    concurrency: 2
    timeout: 30m

  claude-local:
    type: claude_code
    enabled: true
    executable: claude
    execution_location: local
    inference_trust: public_remote
    workspace:
      isolation: git_worktree
    concurrency: 2
    timeout: 30m
```

---

### 9. CodingAgentPolicy

新增独立 Policy：

```go
type CodingAgentPolicy struct {
    Primary string
    Fallbacks []string
    Allowed []string
    RequiredCapabilities []Capability
    MaxParallel int
    AllowPublicInference bool
}
```

示例：

```yaml
agent:
  subagents:

    coding:
      backend_policy:
        primary: codex-local
        fallbacks:
          - claude-local
        allowed:
          - codex-local
          - claude-local
        max_parallel: 2

    code_review:
      backend_policy:
        primary: claude-local
        fallbacks:
          - codex-local
        max_parallel: 1
```

---

### 10. Capability Discovery

推荐 Capability：

```text
code_read
code_write
code_review
code_debug
test_run
shell
git
refactor
documentation
architecture_analysis
security_review
long_task
```

Adapter 启动时执行：

```text
binary discovery
version discovery
health check
capability discovery
```

记录：

```go
type AgentMetadata struct {
    ID string
    Version string
    Capabilities []Capability
    Available bool
    LastHealthCheck time.Time
}
```

---

### 11. CLI 参数由 Adapter 管理

Codex / Claude CLI 可能持续升级。

禁止 Runtime 全局硬编码 CLI flags。

Runtime 只调用：

```go
adapter.Execute(ctx, req)
```

Codex 和 Claude 的参数、输出解析、版本差异全部留在对应 Adapter 中。

---

### 12. Workspace Isolation

禁止多个 Coding Agent 同时直接修改同一个 working tree。

禁止：

```text
Codex ──┐
        ├── same repository working tree
Claude ─┘
```

默认必须使用：

```text
git worktree isolation
```

推荐：

```text
repo/
│
├── main working tree
│
└── .agent-worktrees/
    ├── task-123-codex/
    └── task-123-claude/
```

每个 Agent：

```text
独立 worktree
独立 branch
独立 process group
```

---

### 13. Workspace Manager

新增：

```go
type WorkspaceManager interface {
    Create(
        ctx context.Context,
        taskID string,
        agentID string,
        source string,
    ) (*Workspace, error)

    Diff(
        ctx context.Context,
        workspaceID string,
    ) (*Diff, error)

    Cleanup(
        ctx context.Context,
        workspaceID string,
    ) error
}
```

默认：

```text
Source repository
      ↓
git worktree add
      ↓
isolated workspace
      ↓
Coding Agent
```

---

### 14. 禁止自动 Merge

External Coding Agent 完成后，不允许直接：

```text
merge main
```

必须经过：

```text
Coding Agent
 ↓
Git Diff
 ↓
Build / Tests
 ↓
Verifier
 ↓
Optional Code Review
 ↓
Permission
 ↓
Apply / Merge
```

默认：

```text
AutoMerge = false
```

任何影响原始 working tree 的动作必须由明确 policy 或用户确认控制。

---

### 15. Git Diff 是核心 Evidence

不能只相信：

```text
Agent: "I modified scheduler.go"
```

必须读取真实 workspace：

```bash
git diff
```

Coding Evidence 至少包含：

```text
files_read
files_changed
git_diff
commands_run
tests_run
test_results
lint_results
exit_code
agent_summary
```

External Coding Agent 自报完成属于弱证据。

---

### 16. Coding Evidence

统一写入 P7 Global Evidence Store：

```go
Evidence{
    SourceType: "coding_agent",
    SourceID:   "codex-local",
}
```

推荐 subtype：

```text
coding_agent_summary
coding_agent_diff
coding_agent_test
coding_agent_command
coding_agent_review
```

---

### 17. Task Completion 规则

External Coding Agent 返回：

```text
done
```

不能代表 Task 完成。

必须：

```text
External Agent finished
      ↓
Expected change exists
      ↓
Git Diff / generated artifact
      ↓
Build / Tests
      ↓
Verification
      ↓
Acceptance Criteria
      ↓
Task Complete
```

如果任务要求代码修改但：

```text
git diff == empty
```

则不能判定任务成功。

---

### 18. Codex / Claude Cross Review

P8 支持独立交叉验证：

```text
Codex
 ↓
Implementation
 ↓
Git Diff
 ↓
Claude
 ↓
Review
 ↓
Verifier
```

也支持反向 Review。

Cross Review 默认只在以下条件启用：

```text
high complexity
security-sensitive code
critical production code
verification failed
large diff
conflicting implementation
```

禁止所有 coding task 默认双 Agent Review，以免浪费 Token。

---

### 19. External Agent Early Stop

P7 Early Stop 必须扩展到 External Coding Agent。

如果并发 speculative coding agents 中一个结果已经满足：

```text
implementation complete
AND
tests pass
AND
verification pass
AND
required confidence reached
```

则：

```text
cancel remaining coding agents
```

避免继续消耗远程模型 Token 和本地资源。

---

### 20. Process Cancellation

External Agent Adapter 必须真正支持 cancellation。

推荐：

```go
exec.CommandContext(...)
```

或等价机制。

取消路径：

```text
Runtime cancel()
      ↓
CodingAgentAdapter
      ↓
SIGTERM process group
      ↓
grace period
      ↓
SIGKILL if still alive
```

禁止：

```text
Runtime 已取消
但 codex / claude 仍继续运行
```

---

### 21. Process Group

Linux 下每个 External Coding Agent 应运行在独立 process group。

例如：

```text
codex
 └─ shell
     └─ go test
```

取消时必须终止整个 process tree。

推荐：

```text
Setpgid
 ↓
kill(-pgid, SIGTERM)
 ↓
grace timeout
 ↓
kill(-pgid, SIGKILL)
```

其他平台通过 platform-specific executor 封装。

---

### 22. Timeout

配置：

```yaml
coding_agents:
  codex-local:
    timeout: 30m

  claude-local:
    timeout: 30m
```

Task 可覆盖：

```text
5m
15m
30m
1h
```

Timeout 到达后：

```text
context cancel
 ↓
process group termination
 ↓
Evidence event
```

---

### 23. Global Permission Layer

Codex / Claude 不允许绕过 P7 Global Permission Layer。

自动允许：

```text
read files
grep/search
git status
git diff
run unit tests
run lint
```

可配置允许：

```text
create files
modify source files
local dependency changes
git branch
```

必须确认：

```text
git push
git force push
delete repository files
modify credentials
sudo
deployment
production changes
publish package
create remote PR
merge PR
```

---

### 24. 双层安全模型

External Coding Agent 内部可能自行调用：

```text
shell
git
network
```

Personal Agent 不一定能逐命令拦截。

因此必须采用：

```text
Layer 1
Personal Agent Permission + Workspace Isolation

Layer 2
External Coding Agent 自身 sandbox / permission
```

同时增加：

```text
filesystem restriction
environment sanitization
network policy
workspace isolation
```

不得主动关闭 Codex / Claude 自身安全机制。

---

### 25. Environment Sanitizer

禁止：

```go
cmd.Env = os.Environ()
```

默认直接透传所有环境变量。

必须构造最小环境。

默认可传：

```text
PATH
HOME
LANG
TERM
TMPDIR
必要 Git 配置
必要 External Agent 配置
```

默认过滤：

```text
OPENAI_API_KEY
ANTHROPIC_API_KEY
AWS_SECRET_ACCESS_KEY
AWS_SESSION_TOKEN
DATABASE_PASSWORD
SSH_AUTH_SOCK
internal credentials
arbitrary *_TOKEN
arbitrary *_SECRET
```

某个变量确需传递时，必须由配置与 Permission / Secret Policy 显式允许。

---

### 26. Authentication

优先复用：

```text
Codex CLI existing login state
Claude Code existing login state
```

Personal Agent 不应：

```text
读取 Codex token
读取 Claude token
读取 API key
解析 credential store
复制 credential
记录 credential
```

Runtime 只检测：

```text
available
authenticated
not_authenticated
misconfigured
```

不读取具体认证材料。

---

### 27. External Coding Agent Privacy Policy

External CLI 在本地执行，但推理可能属于：

```text
public_remote
trusted_remote
local_private
```

因此新增：

```go
type DataPolicy struct {
    InferenceTrust InferenceTrust
    RequireSanitization bool
    AllowRepositoryCode bool
    AllowSecrets bool
    BlockPrivateKeys bool
}
```

配置示例：

```yaml
coding_agents:

  codex-local:
    inference_trust: public_remote

    privacy:
      sanitize_prompt: true
      allow_source_code: true
      allow_secrets: false
      block_private_keys: true
```

---

### 28. Source Code 不进行普通 Identifier HMAC

不能将全部：

```text
function name
class name
variable name
source code
```

像 username/hostname 一样 HMAC，否则 Coding Agent 无法正常工作。

应该引入 Repository Privacy Classification：

```text
public_source
internal_source
confidential_source
secret
credential
```

再根据 External Agent InferenceTrust 决定是否允许处理。

---

### 29. Repository Privacy Classification

示例：

```yaml
repositories:

  personal-agent:
    privacy_class: internal_source
    allowed_coding_agents:
      - codex-local
      - claude-local

  chip-design:
    privacy_class: confidential_source
    allowed_coding_agents:
      - internal-coding
    denied_coding_agents:
      - codex-local
      - claude-local
```

对于 EDA、芯片设计、企业私有项目，必须可以完全禁止 `public_remote` inference coding agent。

---

### 30. Coding Task Routing

P8 Router 应根据任务类型选择 External Coding Agent。

示例：

```text
simple code search
→ local grep/tool

small code edit
→ one coding agent

architecture review
→ code_review backend

implementation
→ coding backend

debug
→ code_debug backend

critical change
→ implementation + independent review
```

禁止：

```text
所有 coding task → Codex + Claude 同时执行
```

---

### 31. Coding Task Types

建议：

```text
code_explore
code_edit
code_review
code_debug
code_test
code_refactor
code_document
code_security_review
```

Leader Plan 可以生成：

```json
{
  "role": "coding",
  "task_type": "code_edit"
}
```

Scheduler 根据 Role + Capability + Policy 选择 backend。

---

### 32. 示例工作流

用户：

```text
给 personal-agent 的 scheduler 增加优先级队列，并确保现有测试通过。
```

运行：

```text
Leader
 ↓
Task decomposition
 ↓
Coding Task
 ↓
WorkspaceManager
 ↓
create isolated worktree
 ↓
CodexAgent
 ↓
implementation
 ↓
git diff
 ↓
go test
 ↓
Verifier
 ↓
optional Claude Review
 ↓
Permission
 ↓
向用户展示结果
```

---

### 33. External Coding Agent 不作为最终用户出口

Codex / Claude 返回的文本不能直接发送给用户作为最终结果。

必须：

```text
Codex / Claude
 ↓
Structured Result
 ↓
Evidence
 ↓
Verifier
 ↓
Leader / Synthesis
 ↓
Final Answer
```

Personal Agent 始终保持：

```text
single user-facing authority
```

---

### 34. Session / Continuation

P8 第一版建议：

```text
Task-scoped external coding session
```

不要默认无限期复用 External Agent session。

如果 CLI 支持可靠 continuation ID，可保存为 task-scoped metadata，但不得保存 credential。

---

### 35. 数据模型

建议新增：

```sql
CREATE TABLE coding_agent_runs (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    node_id TEXT,
    agent_id TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    workspace_id TEXT,
    status TEXT NOT NULL,
    started_at DATETIME,
    finished_at DATETIME,
    exit_code INTEGER,
    input_tokens INTEGER,
    output_tokens INTEGER,
    estimated_cost REAL,
    cancelled INTEGER DEFAULT 0
);
```

以及：

```sql
CREATE TABLE coding_workspaces (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    repo_ref TEXT,
    worktree_path TEXT,
    branch_name TEXT,
    status TEXT NOT NULL,
    created_at DATETIME,
    cleaned_at DATETIME
);
```

本地路径属于 local metadata，不允许直接进入 Public Model Context。

---

### 36. Observability Events

新增：

```text
coding_agent.selected
coding_agent.started
coding_agent.health_failed
coding_agent.workspace_created
coding_agent.workspace_cleaned
coding_agent.command_started
coding_agent.command_completed
coding_agent.files_changed
coding_agent.tests_started
coding_agent.tests_completed
coding_agent.review_started
coding_agent.review_completed
coding_agent.cancel_requested
coding_agent.cancelled
coding_agent.completed
coding_agent.failed
```

这些 Event 必须接入 P7 Global Evidence / Event Store。

---

### 37. Cost / Usage

External Coding Agent 也必须进入 P7 Run Budget。

如果 CLI/API 能提供可靠 usage：

```text
input tokens
output tokens
cost
```

则直接记录。

如果无法可靠获得：

```text
Usage = unknown
```

至少记录：

```text
wall time
process duration
exit code
commands count
test runtime
```

禁止伪造 Token 数字。

对于 usage unknown 的 public inference backend，可以使用更严格的：

```text
max duration
max parallelism
```

---

### 38. P8.1 External Coding Agent Contract

Goal: 建立统一 External Coding Agent Adapter 层。

Deliverables：

```text
internal/codingagent/types.go
internal/codingagent/adapter.go
internal/codingagent/registry.go
```

Validation：

```text
register / lookup
capability matching
fallback
disabled backend
health failure
```

---

### 39. P8.2 Safe Process Executor

Goal: 安全运行和取消本地 CLI。

Deliverables：

```text
executor.go
process.go
platform process-group control
timeout
stdout/stderr capture
exit code
```

Validation：

```text
normal completion
timeout
context cancellation
child process termination
SIGTERM grace
SIGKILL fallback
```

---

### 40. P8.3 Workspace Isolation

Goal: 每个 Coding Agent 使用独立 workspace。

Deliverables：

```text
git worktree manager
task branch
diff collection
cleanup
dirty source repo handling
```

Validation：

```text
two coding agents cannot overwrite each other
original worktree remains unchanged
cleanup works
diff is preserved
```

---

### 41. P8.4 Codex Adapter

Goal: 支持 Personal Agent 调用本机 Codex CLI。

Deliverables：

```text
binary discovery
version check
health check
request mapping
execution
result parser
cancellation
evidence extraction
```

Codex credential 必须由 Codex 自己管理。

---

### 42. P8.5 Claude Code Adapter

Goal: 支持 Personal Agent 调用本机 Claude Code CLI。

Deliverables：

```text
binary discovery
version check
health check
request mapping
execution
result parser
cancellation
evidence extraction
```

Claude credential 必须由 Claude Code 自己管理。

---

### 43. P8.6 CodingSubAgent Runtime Integration

接入：

```text
Runtime
 ↓
CodingSubAgent
 ↓
CodingAgentRegistry
 ↓
Codex / Claude
```

Leader 可产生：

```text
coding
code_review
code_test
code_debug
```

Task Node。

---

### 44. P8.7 Permission + Environment Sanitizer

必须完成：

```text
minimal environment
secret stripping
workspace path restriction
repository privacy policy
push/deploy/delete permission
```

External Coding Agent 不能绕过 P7 安全边界。

---

### 45. P8.8 Evidence + Cross Review

必须形成：

```text
Git Diff
Test Results
Commands
Files Changed
Agent Summary
```

支持：

```text
Codex implementation → Claude review
```

和反向 review。

Cross Review 受 policy 控制。

---

### 46. P8.9 Early Stop + Cancellation + Cost

External Coding Agent 接入：

```text
P7 Verification
P7 Early Stop
P7 Budget
P7 Cancellation
```

满足 `good enough` 后：

```text
cancel remaining coding agent process groups
```

必须验证取消后：

```text
no further output
no remaining child process
no additional recorded usage
```

---

### 47. P8.10 Full Coding Agent E2E

使用 fixture repository：

```text
Personal Agent
 ↓
Leader
 ↓
Coding task
 ↓
isolated worktree
 ↓
Codex or Claude
 ↓
source modification
 ↓
tests
 ↓
git diff
 ↓
verification
 ↓
final answer
```

测试不得污染原 repository。

真实 Codex / Claude E2E 通过环境变量 opt-in；普通 CI 使用 mock adapters。

---

### 48. P8 测试要求

#### Adapter Fallback

```text
Codex unavailable → Claude selected
```

以及反向 fallback。

#### Workspace Isolation

两个 Agent 同时修改同一 source repository：

```text
worktree A != worktree B
```

原 working tree 不变化。

#### Cancellation

External Agent 执行长期命令后取消 context，必须结束 parent + child process。

#### Privacy

Confidential repository 调度到 `public_remote` Coding Agent 时必须拒绝。

#### Credentials

Personal Agent 不允许读取或记录：

```text
Codex credential
Claude credential
API key
token
```

#### Permission

```text
git push
sudo
deploy
delete
```

没有 approval 时必须拒绝。

#### Evidence

Agent 声称修改文件，但 `git diff == empty` 时不能判定任务成功。

#### Cross Review

高风险配置下：

```text
implementation agent != review agent
```

#### Early Stop

一个 Agent 已通过 tests + verification 后，其他 speculative coding agents 必须取消。

---

### 49. P8 Definition of Done

P8 完成必须满足：

#### Codex

Personal Agent 能调用本机 Codex CLI。

#### Claude Code

Personal Agent 能调用本机 Claude Code CLI。

#### Adapter Abstraction

Runtime 不直接依赖 Codex / Claude CLI 参数。

#### Independent Configuration

Codex / Claude 可独立：

```text
enable
disable
timeout
trust
concurrency
privacy
```

#### Backend Routing

Leader / Scheduler 可通过 CodingAgentPolicy 选择 backend。

#### Fallback

```text
primary unavailable → fallback
```

#### Workspace Isolation

每个写操作 Coding Agent 默认使用独立 Git worktree。

#### Original Workspace Safety

未经策略允许不得直接修改用户原始 working tree。

#### No Auto Merge

External Coding Agent 不允许自行 merge 主分支。

#### Evidence

代码结果必须包含实际：

```text
git diff
tests
commands
files changed
```

#### Verification

External Agent 自报完成不能作为完成条件。

#### Cross Review

高复杂度/高风险任务支持 Codex / Claude 交叉 Review。

#### Cancellation

用户取消、Early Stop、Budget Hard Limit 可以真正终止：

```text
CLI + child processes
```

#### Privacy

ExecutionLocation 与 InferenceTrust 独立判断。

#### Repository Privacy

Confidential repository 可以禁止 public inference Coding Agent。

#### Credential Boundary

Personal Agent 不读取 Codex / Claude credential store。

#### Permission

push / deploy / sudo / destructive operation 必须经过 Global Permission。

#### Cost

Coding Agent 进入 Task / Run Budget。

#### E2E

Fixture repository Full E2E 通过，并且原 working tree 无污染。

---

### 50. P8 建议 Commit / PR 拆分

#### P8-01

```text
Coding Agent contracts + registry
```

#### P8-02

```text
Safe subprocess executor + process group cancellation
```

#### P8-03

```text
Git worktree workspace manager
```

#### P8-04

```text
Codex CLI adapter
```

#### P8-05

```text
Claude Code CLI adapter
```

#### P8-06

```text
CodingSubAgent + Runtime integration
```

#### P8-07

```text
Permission + environment sanitizer + repository privacy
```

#### P8-08

```text
Coding Evidence + git diff/test collection
```

#### P8-09

```text
Codex/Claude independent cross-review
```

#### P8-10

```text
Early stop + cancellation + cost integration
```

#### P8-11

```text
Coding Agent persistence + observability
```

#### P8-12

```text
Full External Coding Agent E2E
```

---

### 51. P8 最终生态架构

```text
                           User
                            │
                       CLI / REST
                            │
                            ▼
                    Personal Runtime
                            │
                      Leader Agent
                            │
                      Orchestrator
                            │
        ┌─────────────┬─────┼──────┬──────────────┐
        │             │     │      │              │
      Memory         RAG  Browser Reasoning      Coding
        │             │     │      │              │
        │             │     │      │       CodingSubAgent
        │             │     │      │              │
        │             │     │      │      CodingAgentRegistry
        │             │     │      │          ┌───┴────┐
        │             │     │      │          │        │
        │             │     │      │        Codex    Claude
        │             │     │      │          │        │
        │             │     │      │     Worktree  Worktree
        │             │     │      │          │        │
        └─────────────┴─────┴──────┴──────────┴───┬────┘
                                                  │
                                             Evidence Store
                                                  │
                                             Verification
                                                  │
                                      ┌───────────┴───────────┐
                                      │                       │
                                 Good Enough               Not Enough
                                      │                       │
                                  Early Stop             More work /
                                      │                   fallback /
                                   cancel()              cross-review
                                      │
                                      ▼
                                   Synthesis
                                      │
                                  Cost Record
                                      │
                                  Final Answer
```

---

### 52. P8 最重要的验收原则

P8 的核心不是：

> “Personal Agent 能运行两个 CLI。”

而是：

```text
Pluggable External Coding Agent Ecosystem
+
Workspace Isolation
+
Evidence-based Completion
+
Permission-governed Side Effects
+
Cancellable Execution
+
Inference-aware Privacy
```

第一条硬性验收：

```text
Codex / Claude MUST NOT directly modify the user's original working tree
when isolated write execution is required.
```

第二条硬性验收：

```text
External Coding Agent self-reported completion MUST NOT be sufficient.
Actual Git Diff + Tests + Verification are required.
```

第三条硬性验收：

```text
Runtime cancellation MUST terminate the External Coding Agent process
and its child process tree.
```

第四条硬性验收：

```text
Local CLI execution MUST NOT automatically imply local_private inference.
InferenceTrust MUST be independently configured.
```

第五条硬性验收：

```text
Personal Agent MUST NOT read Codex / Claude credential stores.
Authentication remains owned by the respective CLI.
```

第六条硬性验收：

```text
Confidential repositories MUST be able to prohibit public_remote
External Coding Agents.
```

P8 完成后，项目从：

```text
Personal Agent MVP
```

扩展为：

```text
Personal Agent Developer Ecosystem
```

并为未来接入更多专业 External Agents 提供稳定 Adapter 层。
