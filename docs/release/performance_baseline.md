# Performance Baseline

## English

Expected local development ranges:

- Startup: under 2 seconds with SQLite on local SSD.
- Local routing: under 100 ms without model calls.
- BM25 retrieval: under 250 ms for small local corpora.
- Hybrid retrieval: BM25 result available when vector retrieval is unavailable.
- Workflow dispatch: under 250 ms for the default local DAG.
- API health/readiness: under 50 ms on loopback.
- Idle memory footprint: under 150 MB for CLI/server baseline excluding browser and model processes.

These are regression baselines, not hardware-independent guarantees.

Run `pachat release perf-baseline --config <path>` to produce an executable baseline from the real local Runtime. The default offline mode disables external model/browser/CLI dependencies, seeds local memory and RAG fixture data, and measures startup, local memory query latency, BM25-backed hybrid fallback latency, workflow dispatch latency, heap allocation, and concurrent workflow completion behavior. Use `--output <path>` to save the JSON report and `--offline=false` only in release-candidate environments where configured external backends are intentionally available.

## 中文

本地开发环境预期范围：

- 启动：本地 SSD + SQLite 下小于 2 秒。
- Local routing：无模型调用时小于 100 ms。
- BM25 retrieval：小型本地语料下小于 250 ms。
- Hybrid retrieval：向量检索不可用时仍返回 BM25 结果。
- Workflow dispatch：默认本地 DAG 小于 250 ms。
- API health/readiness：loopback 下小于 50 ms。
- 空闲内存：不含浏览器和模型进程时 CLI/server 基线小于 150 MB。

这些是回归检测基线，不是跨硬件承诺。

运行 `pachat release perf-baseline --config <path>` 可从真实本地 Runtime 产出可执行 baseline。默认 offline 模式会关闭外部模型、browser 和 CLI 依赖，写入本地 memory 与 RAG fixture 数据，并测量启动、本地 memory 查询、BM25-backed hybrid fallback、workflow dispatch、heap allocation 和并发 workflow 完成行为。可使用 `--output <path>` 保存 JSON 报告；只有在 release-candidate 环境中明确准备好外部 backend 时才使用 `--offline=false`。
