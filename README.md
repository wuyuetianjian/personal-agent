# personal-agent

## English

AI agent for local user workflows.

This repository is a planning and contract package for adding governed Browser Automation to a local-first parallel Agent system.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, and Evidence Bus records. It does not include a concrete browser driver, Playwright/CDP adapter, cookie reader, browser profile reader, password reader, or remote model caller.

### Contents

- `docs/DESIGN.md`: Architecture and design overview.
- `docs/docs/browser_automation_requirements.md`: Browser Automation requirements.
- `docs/docs/security_model.md`: Browser security and privacy model.
- `docs/docs/implementation_plan.md`: Future implementation phases.
- `docs/docs/client_handoff.md`: Client implementation checklist.
- `docs/docs/package_completion.md`: Completion scope and validation notes.
- `docs/configs/browser.yaml`: Planning-level browser configuration structure.
- `internal/browser`: Go contracts, policy checks, redaction, evidence builders, and tests.

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

本仓库是一个规划与契约包，用于为“本地优先 + 并行 Agent”系统增加受治理的 Browser Automation 浏览器自动化能力。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏和 Evidence Bus 记录的 Go 契约与安全骨架。仓库不包含具体浏览器驱动、Playwright/CDP 适配器、cookie 读取器、浏览器 profile 读取器、密码读取器或远程模型调用器。

### 内容

- `docs/DESIGN.md`：架构与设计概览。
- `docs/docs/browser_automation_requirements.md`：浏览器自动化需求。
- `docs/docs/security_model.md`：浏览器安全与隐私模型。
- `docs/docs/implementation_plan.md`：后续实施阶段。
- `docs/docs/client_handoff.md`：client 实现交接清单。
- `docs/docs/package_completion.md`：完成范围与验证说明。
- `docs/configs/browser.yaml`：规划级浏览器配置结构。
- `internal/browser`：Go 契约、策略校验、脱敏、evidence builder 和测试。

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
