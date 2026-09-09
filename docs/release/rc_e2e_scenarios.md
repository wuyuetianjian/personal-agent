# RC End-To-End Scenarios

## English

Default executable gate:

```sh
go test ./internal/e2e -run FunctionalRC
```

The default RC gate runs locally with mock-backed external executors and real Runtime persistence/scheduling. It covers local RAG/memory with zero public calls, model-backed Leader planning into a DAG with parallel-ready nodes, Browser read/write through the scheduler with approval context, Codex-style coding evidence, Claude-style review evidence, MCP call evidence, public escalation through Privacy Gateway, restart/idempotency behavior for side-effect workflows, and false-to-true watcher notification deduplication.

- Local knowledge: ingest a file, ask a question, verify local evidence and zero public calls.
- Linux diagnosis: run a read-only diagnostic task using memory, RAG, and verification.
- Coding: run governed Codex/Claude-style backends only when explicitly enabled and isolated.
- Browser approval: read a page, propose a write, require approval, then execute.
- MCP: discover metadata, request permission, record evidence, and fail closed if unavailable.
- Workflow restart: kill and restart the service; verify non-terminal workflows resume without duplicate side effects.
- Proactive watcher: false-to-true condition emits one notification and does not duplicate after restart.

## 中文

默认可执行 gate：

```sh
go test ./internal/e2e -run FunctionalRC
```

默认 RC gate 在本地运行，外部 executor 使用 mock-backed 实现，但 Runtime persistence 和 scheduler 走真实路径。覆盖 local RAG/memory 且公网调用为 0、模型驱动 Leader 规划 DAG 与可并行节点、Browser read/write 经 scheduler 并带 approval context、Codex 风格 coding evidence、Claude 风格 review evidence、MCP call evidence、public escalation 经 Privacy Gateway、side-effect workflow 的 restart/idempotency 行为，以及 false-to-true watcher notification 去重。

- 本地知识：导入文件、提问、确认本地 evidence 且公网调用为 0。
- Linux 诊断：使用 memory、RAG 和 verification 运行只读诊断任务。
- Coding：仅在显式启用且隔离后运行 Codex/Claude 风格 backend。
- Browser approval：读取页面、提出写操作、要求审批、再执行。
- MCP：发现 metadata、请求权限、记录 evidence；不可用时 fail closed。
- Workflow restart：杀掉并重启服务，确认非终态 workflow 恢复且不重复副作用。
- Proactive watcher：条件从 false 变为 true 时只发一条 notification，重启后不重复。
