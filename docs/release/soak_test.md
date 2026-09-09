# Soak Test

## English

Run `pachat serve` continuously with representative chat, run, knowledge, workflow, trigger, watcher, and approval workloads. Record memory growth, goroutine count, DB size, workflow terminal rate, browser/process cleanup, watcher event volume, and notification duplication. Any unbounded growth, stuck workflow, leaked browser process, or duplicate side effect blocks GA.

`pachat release soak --config <path>` provides a repeatable local soak gate. By default it runs a short offline Runtime workload that avoids external model/browser/CLI binaries while still exercising SQLite, task creation, workflow dispatch, memory/RAG fallback, verification, synthesis, and notification storage. Release candidates can set `--duration 24h --interval 1m --offline=false` when the configured model/browser/external backends are intentionally available.

The command writes a JSON report when `--output <path>` is set. The report records start/end time, iterations, samples, goroutine growth, heap growth, SQLite growth, terminal workflow counts, non-terminal workflow counts, child-process/worktree/browser artifact counts, and duplicate notification counts. The gate fails when workflows remain stuck, duplicate notifications are observed, child process/worktree/browser artifacts grow in the offline gate, or memory/goroutine/SQLite growth exceeds the configured thresholds.

## 中文

持续运行 `pachat serve`，覆盖 chat、run、knowledge、workflow、trigger、watcher 与 approval 等代表性负载。记录内存增长、goroutine 数量、DB 大小、workflow 终态比例、browser/process 清理、watcher 事件量和 notification 重复情况。任何无界增长、workflow 卡死、browser 进程泄漏或重复副作用都会阻塞 GA。

`pachat release soak --config <path>` 提供可重复执行的本地 soak gate。默认运行短时 offline Runtime workload，避免依赖外部模型、browser 或 CLI 二进制，同时仍覆盖 SQLite、task 创建、workflow dispatch、memory/RAG fallback、verification、synthesis 和 notification storage。Release candidate 环境可在明确准备好模型、browser 和外部 backend 后使用 `--duration 24h --interval 1m --offline=false`。

设置 `--output <path>` 时命令会写入 JSON 报告。报告记录开始/结束时间、迭代次数、采样、goroutine 增长、heap 增长、SQLite 增长、终态 workflow 数、非终态 workflow 数、child process/worktree/browser artifact 数和重复 notification 数。若存在卡住的 workflow、重复 notification、offline gate 中 child process/worktree/browser artifact 增长，或 memory/goroutine/SQLite 增长超过配置阈值，则 gate 失败。
