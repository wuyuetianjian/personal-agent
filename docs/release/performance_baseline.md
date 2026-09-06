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
