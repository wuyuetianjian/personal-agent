# Local-first Parallel Personal Agent 工程需求包

## 中文

本需求包用于指导 Codex 在 `wuyuetianjian/personal-agent` 仓库中实现一个 Go 版本 Local-first Parallel Personal Agent。

目标系统包含：

- Leader Agent 与 Sub-Agent 分离配置和独立模型策略
- OpenAI-compatible Provider、Model Registry、Model Policy、TrustLevel、Capability
- Local-first RAG：Qdrant、BM25、RRF、Reranker、Context Compressor
- Working / Episodic / Semantic Memory
- Task DAG、并行 Sub-Agent、Evidence Bus、Early Stop、context cancellation
- Browser Agent：复用 session/profile，CDP/chromedp RuntimeAdapter，DOM/A11y/截图/鼠标/键盘/坐标 fallback
- Permission Layer、高风险浏览器动作确认
- Privacy Gateway：HMAC-SHA256 伪匿名、敏感凭证 redact、私钥/凭证 dump block
- Public LLM 强制经过 Privacy Gateway
- Claim-level verification、Evidence coverage、conflict detection、confidence policy
- Token/Cost Accountant
- YAML 配置、SQLite/PostgreSQL/Qdrant 数据模型、REST/API 或 CLI 最小入口
- P0-P8 分阶段实施计划和可独立提交的 Codex 任务清单
- External Coding Agent：Codex/Claude Code CLI adapter、worktree isolation、safe process cancellation、repository privacy policy、coding evidence validation

建议执行顺序：

1. 先阅读 `docs/REQUIREMENTS.md`
2. 再阅读 `docs/ARCHITECTURE.md`
3. 使用 `docs/PROJECT_STRUCTURE.md` 创建目录和包边界
4. 按 `docs/IMPLEMENTATION_PLAN.md` 的 commit/PR 清单实施
5. 用 `docs/ACCEPTANCE_CRITERIA.md` 做阶段验收
6. 以 `configs/config.example.yaml` 作为配置规范基线
7. 参考 `templates/` 下的 Go 接口模板实现核心边界

## English

This package is an executable engineering specification for implementing a Go-based Local-first Parallel Personal Agent in the `wuyuetianjian/personal-agent` repository.

The system includes separated Leader/Sub-Agent model configuration, an OpenAI-compatible model layer, local-first RAG, memory, parallel task orchestration, browser automation with session reuse, privacy enforcement, claim verification, token/cost accounting, and governed External Coding Agent adapters for Codex/Claude-style CLI backends.

Recommended execution order:

1. Read `docs/REQUIREMENTS.md`
2. Read `docs/ARCHITECTURE.md`
3. Use `docs/PROJECT_STRUCTURE.md` to create package boundaries
4. Implement by the commit/PR tasks in `docs/IMPLEMENTATION_PLAN.md`
5. Validate each phase with `docs/ACCEPTANCE_CRITERIA.md`
6. Use `configs/config.example.yaml` as the configuration baseline
7. Use Go interface templates in `templates/` as implementation boundaries
