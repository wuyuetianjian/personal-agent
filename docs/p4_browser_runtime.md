# P4 Browser Runtime

Archived stage record: `docs/projdocs/task/P4.md`.

## Scope

P4 connects the existing browser safety contracts to a concrete local runtime boundary. The implementation remains library-level: the CLI still does not execute browser tasks until later API and E2E phases wire task execution together.

## Requirements

- Add a runtime adapter that maps governed browser actions to a local browser runtime.
- Add a chromedp-backed runtime implementation with caller-supplied options only.
- Keep profile/session reuse local and hide profile paths from metadata and evidence.
- Add semantic element location using selector, text, and accessibility-style role/name hints.
- Add screenshot capture to a caller-configured local directory.
- Add accessibility tree reading and short visible text/DOM summaries.
- Add coordinate fallback only when semantic targeting cannot resolve an element and the action includes coordinates.
- Add a confirmation gate so write and high-risk actions cannot execute until an approval is supplied.
- Publish redacted browser events to a browser evidence bus.
- Keep local browser E2E tests opt-in so normal test runs do not require an installed browser.

## Design

`internal/browser/runtime_adapter.go` owns the action-to-runtime mapping. It validates action/session context, delegates domain checks to the existing session manager, asks the permission classifier for a decision, requires explicit confirmation for guarded actions, executes allowed actions, and publishes redacted evidence events.

`internal/browser/chromedp_adapter.go` implements the concrete runtime through chromedp. It accepts allocator options, an optional browser context, and a screenshot directory from callers. No browser path, profile directory, URL, token, or credential is hardcoded. Screenshot IDs are local file paths generated under the configured screenshot directory.

`profile_store.go` keeps profile reuse as local metadata. It resolves profile directories from environment variable names supplied by configuration or callers and returns redacted metadata only.

`locator.go`, `a11y.go`, `screenshot.go`, and `coordinate_fallback.go` keep browser understanding as small, testable helpers. Semantic locators prefer selector, then text, then role/name matching. Coordinate fallback records the fallback reason and is denied when coordinates are missing.

`confirmation.go` provides an in-memory confirmation store for P4 tests and later API wiring. P5 can replace it with the global permission layer without changing the browser runtime interface.

## Validation

- Runtime adapter mock tests cover allowed execution, confirmation gating, evidence publishing, and cancellation.
- Session/profile tests cover local-only metadata and environment-based profile lookup.
- Locator and coordinate fallback tests cover selector/text/role matching and fallback denial.
- Domain allowlist denial remains covered by session/tool tests.
- Local chromedp E2E tests under `testdata/browser` skip unless `PACHAT_BROWSER_E2E=1` is set.

## 中文

## 范围

P4 将已有浏览器安全契约接到具体本地运行时边界。实现仍停留在库层；CLI 要到后续 API 和 E2E 阶段才会把浏览器任务接入实际 task execution。

## 需求

- 新增 runtime adapter，将受治理浏览器动作映射到本地浏览器 runtime。
- 新增 chromedp runtime 实现，所有选项由调用方传入。
- profile/session 复用保持本地化，并且 metadata 与 evidence 不暴露 profile path。
- 支持基于 selector、text、role/name hint 的语义元素定位。
- 将截图保存到调用方配置的本地目录。
- 支持 accessibility tree 读取以及短 visible text/DOM 摘要。
- 仅当语义定位失败且 action 提供坐标时使用坐标兜底。
- 增加确认 gate，write 和 high-risk action 在 approval 缺失时不能执行。
- 向 browser evidence bus 发布脱敏事件。
- 本地浏览器 E2E 测试默认跳过，避免普通测试依赖已安装浏览器。

## 设计

`internal/browser/runtime_adapter.go` 负责 action 到 runtime 的映射。它校验 action/session 上下文，复用现有 session manager 做 domain 检查，通过 permission classifier 得出决策，为受保护动作要求明确确认，执行允许的动作，并发布脱敏 evidence event。

`internal/browser/chromedp_adapter.go` 通过 chromedp 实现具体 runtime。allocator options、可选 browser context 和截图目录都由调用方提供。实现不硬编码浏览器路径、profile 目录、URL、token 或 credential。截图 ID 是配置截图目录下生成的本地文件路径。

`profile_store.go` 将 profile reuse 限定为本地 metadata。它只从配置或调用方提供的环境变量名解析 profile 目录，并只返回脱敏 metadata。

`locator.go`、`a11y.go`、`screenshot.go` 和 `coordinate_fallback.go` 将页面理解能力拆成可测试 helper。语义定位优先 selector，其次 text，最后 role/name 匹配。坐标兜底会记录 fallback reason，缺少坐标时拒绝执行。

`confirmation.go` 提供 P4 测试和后续 API 接线可用的内存 confirmation store。P5 可以用全局 permission layer 替换它，而不需要改变浏览器 runtime interface。

## 验证

- Runtime adapter mock tests 覆盖允许执行、确认 gate、evidence 发布和取消。
- Session/profile tests 覆盖 local-only metadata 和基于环境变量的 profile lookup。
- Locator 与 coordinate fallback tests 覆盖 selector/text/role matching 和 fallback denial。
- Domain allowlist denial 继续由 session/tool tests 覆盖。
- `testdata/browser` 下的本地 chromedp E2E 仅在设置 `PACHAT_BROWSER_E2E=1` 时运行。
