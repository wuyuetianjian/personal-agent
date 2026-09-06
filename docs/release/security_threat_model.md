# Security Guide And Threat Model

## English

Trust zones are local private runtime, trusted remote providers, public remote providers, browser sessions, external coding agents, MCP tools, and untrusted event inputs. Public model calls require the Privacy Gateway and fail closed when redaction or pseudonymization cannot run. Credential reads and direct browser writes require explicit permission. Confidential projects must not use public remote inference for repository content.

Prompt injection is treated as untrusted content. The agent preserves evidence boundaries, uses permission checks for side effects, and records approvals. Service examples use local loopback HTTP by default and keep writable paths narrow.

## 中文

信任边界包括本地私有运行时、可信远程 provider、公网远程 provider、浏览器会话、外部 Coding Agent、MCP 工具和不可信事件输入。公网模型调用必须经过 Privacy Gateway；无法执行脱敏或伪名化时 fail closed。凭据读取和浏览器写操作需要显式权限。Confidential project 禁止用公网远程推理处理仓库内容。

Prompt injection 被视为不可信输入。Agent 保留 evidence 边界，对副作用执行权限检查，并记录审批。服务示例默认只监听本地 loopback，并限制可写路径。
