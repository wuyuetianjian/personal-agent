# Browser Automation Code Skeleton Design

## Scope

Implement the code-facing contracts and safety skeleton described by the Browser Automation planning documents.

This task must not implement a concrete browser driver, Playwright/CDP adapter, browser profile reader, cookie reader, password reader, or remote model caller.

## Architecture

Add a Go module with an `internal/browser` package. The package exposes browser automation as governed contracts:

- Action, target, observation, result, and error types.
- Browser tool interface for future runtime adapters.
- Session manager interface and in-memory policy implementation.
- Permission classifier for browser action risk levels.
- Privacy gateway redactor for model/evidence-safe summaries.
- Evidence event builder for redacted audit records.

## Behavior

- Read and allowlisted navigation actions can be auto-allowed.
- Low-risk interactions are policy-dependent.
- Write interactions require confirmation by default.
- High-risk transactions require explicit confirmation by default.
- Sessions must enforce domain allowlists, expiry, reuse policy, and local-only storage metadata.
- Redaction must remove or mask common secret, token, cookie, password, authorization header, email, IP address, and local profile path values.
- Evidence events must store only redacted target and observation data.

## Validation

Unit tests cover:

- Permission classification.
- Domain allowlist enforcement.
- Expired session rejection.
- Local-only storage policy metadata.
- Privacy redaction.
- Evidence redaction.

## 中文

## 范围

按照 Browser Automation 规划文档实现代码层面的接口与安全骨架。

本任务不实现具体浏览器驱动、Playwright/CDP 适配器、浏览器 profile 读取器、cookie 读取器、密码读取器或远程模型调用器。

## 架构

新增 Go module 和 `internal/browser` 包。该包将浏览器自动化暴露为受治理的契约：

- Action、target、observation、result 和 error 类型。
- 面向后续运行时适配器的 Browser Tool 接口。
- Session Manager 接口与内存策略实现。
- 浏览器动作风险等级分类器。
- 面向模型和 evidence 的 Privacy Gateway 脱敏器。
- 写入脱敏审计记录的 Evidence Event builder。

## 行为

- 读取动作和 allowlist 内导航动作可以自动允许。
- 低风险交互由策略决定。
- 写交互默认需要确认。
- 高风险交易默认需要明确确认。
- Session 必须执行域名 allowlist、过期时间、复用策略和本地存储策略元数据约束。
- 脱敏必须移除或遮蔽常见 secret、token、cookie、password、authorization header、email、IP 地址和本地 profile path。
- Evidence event 只能保存脱敏后的 target 与 observation 数据。

## 验证

单元测试覆盖：

- 权限分类。
- 域名 allowlist。
- 过期 session 拒绝。
- local-only 存储策略元数据。
- Privacy Gateway 脱敏。
- Evidence 脱敏。
