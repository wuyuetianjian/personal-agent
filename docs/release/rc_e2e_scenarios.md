# RC End-To-End Scenarios

## English

- Local knowledge: ingest a file, ask a question, verify local evidence and zero public calls.
- Linux diagnosis: run a read-only diagnostic task using memory, RAG, and verification.
- Coding: run governed Codex/Claude-style backends only when explicitly enabled and isolated.
- Browser approval: read a page, propose a write, require approval, then execute.
- MCP: discover metadata, request permission, record evidence, and fail closed if unavailable.
- Workflow restart: kill and restart the service; verify non-terminal workflows resume without duplicate side effects.
- Proactive watcher: false-to-true condition emits one notification and does not duplicate after restart.

## 中文

- 本地知识：导入文件、提问、确认本地 evidence 且公网调用为 0。
- Linux 诊断：使用 memory、RAG 和 verification 运行只读诊断任务。
- Coding：仅在显式启用且隔离后运行 Codex/Claude 风格 backend。
- Browser approval：读取页面、提出写操作、要求审批、再执行。
- MCP：发现 metadata、请求权限、记录 evidence；不可用时 fail closed。
- Workflow restart：杀掉并重启服务，确认非终态 workflow 恢复且不重复副作用。
- Proactive watcher：条件从 false 变为 true 时只发一条 notification，重启后不重复。
