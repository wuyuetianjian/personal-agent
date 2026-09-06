# Upgrade Procedure

## English

1. Stop the service.
2. Run `pachat backup create --config <config> --output <backup.zip>`.
3. Run `pachat config validate --config <config>`.
4. Install the new binary or run `scripts/upgrade.sh /path/to/new/pachat`.
5. Run `pachat storage migrate --config <config>`.
6. Start the service.
7. Check `/healthz`, `/readyz`, and representative `pachat run` output.

Rollback is binary-only when the database schema is still compatible with the previous version. If migrations changed the schema, restore the backup before starting the older binary.

## 中文

1. 停止服务。
2. 执行 `pachat backup create --config <config> --output <backup.zip>`。
3. 执行 `pachat config validate --config <config>`。
4. 安装新二进制，或执行 `scripts/upgrade.sh /path/to/new/pachat`。
5. 执行 `pachat storage migrate --config <config>`。
6. 启动服务。
7. 检查 `/healthz`、`/readyz` 和代表性的 `pachat run` 输出。

如果数据库 schema 仍兼容旧版本，可以只回滚二进制。如果迁移改变了 schema，启动旧版本前必须先恢复备份。
