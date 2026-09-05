# personal-agent

## English

AI agent for local user workflows.

This repository is a Go implementation of a local-first parallel personal agent. The current implementation is the P0 foundation: configuration loading, SQLite migrations, core agent/browser contracts, and a no-op CLI task runner.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, and Evidence Bus records. It does not yet include a concrete browser driver, Playwright/CDP adapter, cookie reader, browser profile reader, password reader, or remote model caller.

### Contents

- `cmd/personal-agent`: CLI entrypoint.
- `configs/config.example.yaml`: Local-first example configuration.
- `internal/config`: YAML loader and validator.
- `internal/storage`: SQLite storage and migrations.
- `internal/agent`: Leader/Sub-Agent contracts.
- `internal/browser`: Go contracts, policy checks, redaction, evidence builders, and tests.

### Run

```sh
go run ./cmd/personal-agent run --config configs/config.example.yaml --task "smoke test"
```

### Validation

Run:

```sh
go test ./...
```

### Safety Defaults

- Browser sessions, cookies, tokens, passwords, and profile data remain local.
- Public LLM browser planning can receive only Privacy Gateway redacted summaries.
- Browser actions pass through the Permission Layer before execution.
- High-risk actions such as login submission, payment, deletion, sending messages, uploads, OAuth grants, and legal acceptance require confirmation or elevated policy.
- Evidence Bus records must be redacted before storage.

## 中文

面向本地用户工作流的 AI Agent。

本仓库是一个 Go 版本本地优先并行个人 Agent。当前实现为 P0 基础层：配置加载、SQLite 迁移、核心 agent/browser 契约，以及 no-op CLI task runner。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏和 Evidence Bus 记录的 Go 契约与安全骨架。仓库暂不包含具体浏览器驱动、Playwright/CDP 适配器、cookie 读取器、浏览器 profile 读取器、密码读取器或远程模型调用器。

### 内容

- `cmd/personal-agent`：CLI 入口。
- `configs/config.example.yaml`：本地优先示例配置。
- `internal/config`：YAML 加载和校验。
- `internal/storage`：SQLite 存储与迁移。
- `internal/agent`：Leader/Sub-Agent 契约。
- `internal/browser`：Go 契约、策略校验、脱敏、evidence builder 和测试。

### 运行

```sh
go run ./cmd/personal-agent run --config configs/config.example.yaml --task "smoke test"
```

### 验证

运行：

```sh
go test ./...
```

### 默认安全策略

- 浏览器 session、cookie、token、密码和 profile 数据仅保留在本地。
- 公网 LLM 参与浏览器规划时，只能接收 Privacy Gateway 脱敏后的摘要。
- 所有浏览器动作执行前都必须经过 Permission Layer。
- 登录提交、支付、删除、发送消息、上传、OAuth 授权、接受法律条款等高风险动作需要用户确认或更高权限策略。
- Evidence Bus 写入前必须完成敏感信息脱敏。
