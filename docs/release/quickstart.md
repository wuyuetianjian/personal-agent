# Pachat v1.0 Quickstart

## English

1. Download the archive for your platform and verify it against `SHA256SUMS`.
2. Extract `pachat` and run `scripts/install.sh /path/to/pachat`.
3. Export required secrets through environment variables, for example `PERSONAL_AGENT_PRIVACY_HMAC_SECRET` and `PACHAT_API_TOKEN`.
4. Run `pachat config validate --config ~/.config/pachat/config.yaml`.
5. Add local knowledge with `pachat knowledge add --config ~/.config/pachat/config.yaml ./notes.txt`.
6. Ask the first local question with `pachat run --config ~/.config/pachat/config.yaml --task "summarize my local notes"`.
7. Start the local server with `pachat serve --config ~/.config/pachat/config.yaml`.
8. Check `http://127.0.0.1:8787/healthz`, `/readyz`, `/metrics`, and `/dashboard`.

## 中文

1. 下载对应平台的发布包，并使用 `SHA256SUMS` 校验。
2. 解压 `pachat`，执行 `scripts/install.sh /path/to/pachat`。
3. 通过环境变量提供密钥，例如 `PERSONAL_AGENT_PRIVACY_HMAC_SECRET` 与 `PACHAT_API_TOKEN`。
4. 执行 `pachat config validate --config ~/.config/pachat/config.yaml`。
5. 使用 `pachat knowledge add --config ~/.config/pachat/config.yaml ./notes.txt` 添加本地知识。
6. 使用 `pachat run --config ~/.config/pachat/config.yaml --task "summarize my local notes"` 运行第一个问题。
7. 使用 `pachat serve --config ~/.config/pachat/config.yaml` 启动本地服务。
8. 检查 `http://127.0.0.1:8787/healthz`、`/readyz`、`/metrics` 与 `/dashboard`。
