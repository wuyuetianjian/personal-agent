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

Status: P7 Runtime MVP completed locally on 2026-09-06 for the first vertical slice. See `docs/projdocs/task/P7.md`.


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
