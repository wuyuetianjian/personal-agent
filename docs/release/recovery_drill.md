# Data-Loss Recovery Drill

## English

1. Create a project, add knowledge, run a task, create a trigger, and store a notification.
2. Run `pachat backup create --config <config> --output <backup.zip>`.
3. Move the test data directory aside.
4. Run `pachat backup restore --config <config> --input <backup.zip>` as a dry-run validation.
5. Restore the data copy in a disposable environment.
6. Verify projects, knowledge documents, memory, workflows, triggers, approvals, and notifications.

GA is blocked if restored data cannot pass `pachat storage integrity` or representative workflow reads.

`pachat release recovery-drill --config <path>` executes the drill against disposable fixture state. It uses the supplied config as a template, creates an isolated SQLite database under `--work-dir` or a temporary directory, seeds Projects, Skills, Memory, Workflow, Trigger, Notification, and Approval records, creates a backup archive, deletes the fixture database files, restores the archive into a fresh database, and verifies every seeded object class plus SQLite integrity. The command writes a JSON report when `--output <path>` is set and never deletes the database referenced by the user's original config.

## 中文

1. 创建 project、添加 knowledge、运行 task、创建 trigger，并写入 notification。
2. 执行 `pachat backup create --config <config> --output <backup.zip>`。
3. 将测试 data 目录移走。
4. 执行 `pachat backup restore --config <config> --input <backup.zip>` 做 dry-run 验证。
5. 在一次性环境中恢复数据副本。
6. 验证 projects、knowledge documents、memory、workflows、triggers、approvals 和 notifications。

如果恢复后的数据不能通过 `pachat storage integrity` 或代表性 workflow 读取，则阻塞 GA。

`pachat release recovery-drill --config <path>` 会针对一次性 fixture 状态实际执行恢复演练。它使用传入配置作为模板，在 `--work-dir` 或临时目录下创建隔离 SQLite 数据库，写入 Projects、Skills、Memory、Workflow、Trigger、Notification 和 Approval 记录，创建 backup archive，删除 fixture 数据库文件，恢复到新的数据库，并验证每一类种子对象以及 SQLite integrity。设置 `--output <path>` 时会写入 JSON 报告；命令不会删除用户原始配置指向的数据库。
