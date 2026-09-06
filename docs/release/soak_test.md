# Soak Test

## English

Run `pachat serve` continuously with representative chat, run, knowledge, workflow, trigger, watcher, and approval workloads. Record memory growth, goroutine count, DB size, workflow terminal rate, browser/process cleanup, watcher event volume, and notification duplication. Any unbounded growth, stuck workflow, leaked browser process, or duplicate side effect blocks GA.

## 中文

持续运行 `pachat serve`，覆盖 chat、run、knowledge、workflow、trigger、watcher 与 approval 等代表性负载。记录内存增长、goroutine 数量、DB 大小、workflow 终态比例、browser/process 清理、watcher 事件量和 notification 重复情况。任何无界增长、workflow 卡死、browser 进程泄漏或重复副作用都会阻塞 GA。
