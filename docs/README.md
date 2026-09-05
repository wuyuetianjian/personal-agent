# Local-First Parallel Agent Browser Automation Planning Pack

## English

This package extends a local-first parallel Agent design with Browser Automation planning and code-facing contracts.

It includes Go interfaces, types, policy checks, redaction helpers, evidence builders, and unit tests. It intentionally does not include a concrete browser driver, Playwright/CDP adapter, cookie reader, browser profile reader, password reader, or remote model caller.

### Included Files

- `DESIGN.md`: Architecture and design overview.
- `docs/browser_automation_requirements.md`: Browser Automation requirements.
- `docs/security_model.md`: Browser security and privacy model.
- `docs/implementation_plan.md`: Future implementation phases.
- `docs/client_handoff.md`: Client implementation handoff checklist.
- `configs/browser.yaml`: Planning-level browser configuration structure.
- `../internal/browser`: Go contracts and safety skeleton.

### Key Capabilities Planned

- Browser Tool as a controlled Agent tool.
- Reuse of local browser session/profile/cookies.
- Local-only session and cookie storage.
- Mouse, keyboard, navigation, screenshot, DOM, and accessibility support.
- Preferred semantic automation with coordinate fallback.
- Browser Sub-Agent delegation.
- Permission Layer for read/write/high-risk actions.
- Browser Session Manager for session ID, profile directory, allowlist, and expiry.
- Evidence Bus records for browser operations.
- Early stop and context cancellation.
- Privacy Gateway before public LLM browser planning.

### Safety Defaults

- Public LLMs never receive raw cookie, session, token, password, or sensitive raw DOM data.
- Read/navigation may be automatic on allowed domains.
- Login submission, payment, delete, send-message, upload, OAuth grant, and legal acceptance require stronger permission or user confirmation.
- Sensitive evidence is redacted before storage.

## 中文

这个包在“本地优先 + 并行 Agent”的设计基础上，新增 Browser Automation 浏览器自动化能力规划与代码契约。

当前包包含 Go 接口、类型、策略校验、脱敏 helper、evidence builder 和单元测试。它不包含具体浏览器驱动、Playwright/CDP 适配器、cookie 读取器、浏览器 profile 读取器、密码读取器或远程模型调用器。

### 文件内容

- `DESIGN.md`：整体架构与设计说明。
- `docs/browser_automation_requirements.md`：浏览器自动化需求。
- `docs/security_model.md`：浏览器安全与隐私模型。
- `docs/implementation_plan.md`：后续实现阶段规划。
- `docs/client_handoff.md`：client 实现交接清单。
- `configs/browser.yaml`：规划级浏览器配置结构。
- `../internal/browser`：Go 契约与安全骨架。

### 规划能力

- Browser Tool 作为受控工具接入 Agent。
- 支持复用本地浏览器 session/profile/cookie。
- session 与 cookie 仅本地保存。
- 支持鼠标、键盘、导航、截图、DOM、可访问性树。
- 优先使用语义化浏览器自动化，必要时 fallback 到坐标鼠标控制。
- 支持 Browser Sub-Agent 接收浏览器任务切片。
- 所有浏览器操作经过 Permission Layer。
- Browser Session Manager 管理 session ID、profile 目录、域名 allowlist 和过期时间。
- 浏览器操作写入 Evidence Bus。
- 支持 Early Stop 与 context cancellation。
- 公网模型参与浏览器规划前必须经过 Privacy Gateway 脱敏。

### 默认安全策略

- 公网模型不得接收原始 cookie、session、token、密码或敏感原始 DOM。
- read/navigation 在 allowlist 域名下可自动执行。
- 登录提交、支付、删除、发送消息、上传、OAuth 授权、接受法律条款等动作需要更高权限或用户确认。
- Evidence Bus 存储前必须脱敏。
