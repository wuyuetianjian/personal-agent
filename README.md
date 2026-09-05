# personal-agent

## English

AI agent for local user workflows.

This repository is a Go implementation of a local-first parallel personal agent. The current implementation is the P0 foundation: `pachat` CLI packaging, configuration loading, SQLite migrations, core agent/browser contracts, interactive local memory, and long-task state tracking.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, and Evidence Bus records. It does not yet include a concrete browser driver, Playwright/CDP adapter, cookie reader, browser profile reader, password reader, or remote model caller.

### Contents

- `cmd/pachat`: CLI entrypoint.
- `configs/config.example.yaml`: Local-first example configuration.
- `internal/config`: YAML loader and validator.
- `internal/storage`: SQLite storage and migrations.
- `internal/memory`: Local episodic memory store.
- `internal/agent`: Leader/Sub-Agent contracts.
- `internal/browser`: Go contracts, policy checks, redaction, evidence builders, and tests.

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

Current P0 limitation: the command does not yet call models, run RAG, automate the browser, verify claims, or execute Sub-Agents. Those capabilities are planned for later phases.

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

## 中文

面向本地用户工作流的 AI Agent。

本仓库是一个 Go 版本本地优先并行个人 Agent。当前实现为 P0 基础层：`pachat` CLI 打包、配置加载、SQLite 迁移、核心 agent/browser 契约、交互式本地记忆和长任务状态跟踪。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏和 Evidence Bus 记录的 Go 契约与安全骨架。仓库暂不包含具体浏览器驱动、Playwright/CDP 适配器、cookie 读取器、浏览器 profile 读取器、密码读取器或远程模型调用器。

### 内容

- `cmd/pachat`：CLI 入口。
- `configs/config.example.yaml`：本地优先示例配置。
- `internal/config`：YAML 加载和校验。
- `internal/storage`：SQLite 存储与迁移。
- `internal/memory`：本地 episodic memory 存储。
- `internal/agent`：Leader/Sub-Agent 契约。
- `internal/browser`：Go 契约、策略校验、脱敏、evidence builder 和测试。

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

当前 P0 限制：该命令还不会调用模型、运行 RAG、自动化浏览器、验证 claims 或执行 Sub-Agent。这些能力会在后续阶段实现。

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
