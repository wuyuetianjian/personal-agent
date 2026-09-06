# Administrator Guide

## English

Configuration is loaded from YAML plus environment indirection. Store secrets only in environment variables or an external secret manager. SQLite is the default local database; keep `app.data_dir` and `storage.sqlite.path` on persistent storage. Use `pachat doctor`, `pachat storage integrity`, `pachat storage vacuum`, and `pachat storage checkpoint` for routine checks. Use systemd or launchd examples under `packaging/` for service operation.

Before upgrades, run `pachat backup create`, `pachat config validate`, then install the new binary and run `pachat storage migrate`. Confirm `/healthz` and `/readyz` before declaring the upgrade complete.

## 中文

配置由 YAML 与环境变量间接引用组成。密钥只放在环境变量或外部密钥系统中。SQLite 是默认本地数据库；`app.data_dir` 与 `storage.sqlite.path` 必须放在持久化存储上。日常运维使用 `pachat doctor`、`pachat storage integrity`、`pachat storage vacuum` 和 `pachat storage checkpoint`。服务部署参考 `packaging/` 下的 systemd 与 launchd 示例。

升级前先执行 `pachat backup create` 和 `pachat config validate`，再安装新二进制并执行 `pachat storage migrate`。确认 `/healthz` 与 `/readyz` 后再完成升级。
