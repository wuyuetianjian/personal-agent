# P14 前闭环需求

本文档定义 P14 打包前一次性补齐 P7-P12 剩余需求的实现边界。参考来源为 `docs/projdocs/PERSONAL_AGENT_REMAINING_WORK_UPDATED.md`。

## 范围

本轮目标是把 P7-P12 的运行时、持久化、CLI/API 和安全治理路径补成可运行闭环：

- `Runtime.Run()` 必须通过持久化 workflow 和 scheduler 执行主链，现有本地 memory、RAG、verification、synthesis 逻辑作为 workflow capability 执行。
- Leader planning 必须输出有界 DAG，且只能引用已注册 capability。没有可用模型或隐私网关不允许时必须 fail closed 或退回本地只读默认 DAG。
- Sub-Agent 返回必须统一包含状态、证据、置信度、usage 和错误分类。Browser、Coding、MCP 等外部能力在未启用时必须返回治理后的 unavailable/denied 结果，而不是静默成功。
- Workflow 必须持久化 node dependency、DAG 版本、ready/recovery 状态，并提供启动恢复和后台 worker 基础循环。
- RAG Runtime 必须支持 BM25、可选向量检索、RRF、可选 reranker、compressor，并在向量不可用时降级到 BM25。
- API task cancellation 必须取消正在运行的 task context，而不只是更新数据库状态。
- Chat 每轮必须走 Runtime，同时保留本地 episodic memory。
- Trigger/Event 必须提供 CLI/API create、list、show、enable、disable、run-now、history 和 `POST /events` 基础入口。
- Proactive 必须提供 daemon 基础循环、condition evaluator、dead letter queue、startup recovery 和受限 goal planner。
- P12 必须提供持久化 approval inbox、dashboard/SSE 基础 endpoint、模型发现和 watcher daemon 基础路径。
- 示例配置必须遵守 fail-closed/default-safe：外部 coding backend 默认禁用，direct writes 默认禁用。

## 非目标

以下能力仍属于 P14 或 post-v1 加固，不作为本轮阻塞项：

- 生产级 OS/kernel/browser 隔离。
- 外部分布式 tracing/export 服务。
- 外部漏洞扫描服务。
- 完整视觉产品级 Web Dashboard。
- 真实第三方通知通道。

## 验收

- `go test ./...` 通过。
- `make build` 通过。
- `make smoke` 通过或明确记录环境型失败。
- README 同步更新中文和英文能力边界。
- 每个实现段落用 git 单独提交。

## 完成记录

本轮已补齐以下 P14 前基础闭环：

- Runtime task 主链通过持久化 workflow 和 scheduler 执行，默认 DAG 覆盖 memory、RAG、verification、synthesis。
- Workflow node 依赖和 DAG 版本已持久化，恢复时保留依赖关系。
- API long task 使用可取消后台 runner，cancel endpoint 会触发 context cancellation。
- Trigger/Event 提供 CLI/API create、list、show、enable、disable、run-now、history 和 push event ingestion。
- Proactive daemon 提供 tick、run-now、startup recovery、condition evaluator 和 dead-letter 基础。
- Approval inbox、notification inbox、dashboard/SSE、model discovery、filesystem watcher 基础入口已接入 SQLite 和 CLI/API。
- Bounded model planner、public escalation fail-closed 接口、hybrid RAG 降级路径、capability runtime metrics、skill eval activation gate、draft skill candidate generator 和 bounded goal planner 已补齐库层闭环。
- 示例配置已改为 external coding backend 默认禁用且 direct writes 默认禁用。

## 后续 P14 发布闭环

P14 GA 发布打包已作为独立需求记录在 `docs/p14_ga_release_requirements.md`，完成状态归档在 `docs/projdocs/task/P14.md`。发布闭环包括版本模型、构建矩阵、checksum、安装/升级脚本、systemd/launchd 示例、production config 示例、发布文档、迁移兼容测试和 release checklist 自动化。
