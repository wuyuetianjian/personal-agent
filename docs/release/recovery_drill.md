# Data-Loss Recovery Drill

## English

1. Create a project, add knowledge, run a task, create a trigger, and store a notification.
2. Run `pachat backup create --config <config> --output <backup.zip>`.
3. Move the test data directory aside.
4. Run `pachat backup restore --config <config> --input <backup.zip>` as a dry-run validation.
5. Restore the data copy in a disposable environment.
6. Verify projects, knowledge documents, memory, workflows, triggers, approvals, and notifications.

GA is blocked if restored data cannot pass `pachat storage integrity` or representative workflow reads.

## 中文

1. 创建 project、添加 knowledge、运行 task、创建 trigger，并写入 notification。
2. 执行 `pachat backup create --config <config> --output <backup.zip>`。
3. 将测试 data 目录移走。
4. 执行 `pachat backup restore --config <config> --input <backup.zip>` 做 dry-run 验证。
5. 在一次性环境中恢复数据副本。
6. 验证 projects、knowledge documents、memory、workflows、triggers、approvals 和 notifications。

如果恢复后的数据不能通过 `pachat storage integrity` 或代表性 workflow 读取，则阻塞 GA。
