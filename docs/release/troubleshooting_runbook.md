# Troubleshooting Runbook

## English

- Local model unreachable: run `pachat doctor`, check model base URL environment variables, and keep public fallback disabled unless privacy requirements are met.
- Qdrant unavailable: hybrid retrieval falls back to BM25; check vector service configuration only if vector search is required.
- DB locked: stop duplicate services, run `pachat storage checkpoint`, and inspect filesystem permissions.
- Browser cannot attach: verify Chrome/Chromium availability, profile env vars, and domain allowlists.
- Codex or Claude unavailable: confirm backend is enabled, binary path env vars are set, and project privacy allows the backend.
- MCP unavailable: confirm connector configuration and permissions; unavailable MCP calls should fail closed.
- Workflow stuck: inspect `pachat workflow show` and `pachat workflow events`; restart service to trigger recovery.
- Budget blocked: inspect cost policy and model usage.
- Permission waiting: run `pachat approval list`.
- Index stale: run `pachat knowledge status` and `pachat knowledge reindex`.

## 中文

- 本地模型不可达：执行 `pachat doctor`，检查模型 base URL 环境变量；除非满足隐私要求，否则保持 public fallback 禁用。
- Qdrant 不可用：hybrid retrieval 会降级到 BM25；仅在需要向量检索时检查向量服务配置。
- DB locked：停止重复服务，执行 `pachat storage checkpoint`，检查文件系统权限。
- Browser 无法 attach：检查 Chrome/Chromium、profile 环境变量和域名 allowlist。
- Codex 或 Claude 不可用：确认 backend 已启用、binary path 环境变量已设置，并且 project privacy 允许该 backend。
- MCP 不可用：检查 connector 配置和权限；不可用 MCP 调用应 fail closed。
- Workflow 卡住：查看 `pachat workflow show` 与 `pachat workflow events`；重启服务触发恢复。
- Budget blocked：检查 cost policy 和 model usage。
- Permission waiting：执行 `pachat approval list`。
- Index stale：执行 `pachat knowledge status` 与 `pachat knowledge reindex`。
