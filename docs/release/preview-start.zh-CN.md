# personal-agent 测试预览包（不是 v1.0 正式版）

这是未签名、未公证的 CI 测试产物，不是安装器或自动更新渠道。
先核对下载文件的 `.sha256`，解压至新目录。`MANIFEST.json` 记录目标、源码
提交和逐文件 SHA256；构建成功不等于在该平台运行验收成功，以配套 CI 结果为准。
Windows ARM64 若仅交叉构建，应视为未运行验证。Windows 普通非管理员账户
支持仍未验收。不要关闭系统安全保护来运行它。

## 运行环境

`pachat`（Windows 为 `pachat.exe`）内嵌已构建的 TypeScript Web。
终端入口是 `node terminal/cli.mjs`，需要另外安装官方 Node.js 24。
终端依赖已随包提供，无需安装 pnpm、tsx 或重新下载依赖；保留完整 terminal
目录，包含 Yoga WASM 与第三方许可证。不要单独拷贝 cli.mjs。

## 启动自己的服务

1. 复制 config.example.yaml 到自己的配置位置，填写实际 provider、模型注册 ID。
   示例名称并不保证该模型已安装；API key 只放相应环境变量，不写源码/共享包。
2. 准备一个全新的专用运行数据父目录。不要指向旧生产数据库。
3. 在当前终端设置私有 `PA_AGENT_TOKEN`，然后运行（Windows 使用 .\pachat.exe）：

```sh
./pachat version
./pachat serve-agent --config /absolute/config.yaml --model REGISTERED_MODEL_ID --runtime-parent /absolute/private-runtime --addr 127.0.0.1:7319
```

访问 http://127.0.0.1:7319 并输入 token；不要把 token 放 URL。
另一个终端设置同一 PA_AGENT_TOKEN：

```sh
node terminal/cli.mjs --url http://127.0.0.1:7319 --mode chat_only
node terminal/cli.mjs --url http://127.0.0.1:7319 --mode chat_only --print '你好' --format stream-json
```

使用 PowerShell 时通过 `$env:PA_AGENT_TOKEN = '你自己的随机令牌'` 设置环境变量，
路径用实际绝对 Windows 路径并加引号。以上交互会调用你自己配置的模型，可能收费。
CI 的离线合成测试不证明真实模型质量、兼容性或费用统计。

## Mac / Windows 文件能力边界

默认聊天不授予文件工具。只在你明确选择可信工作区时，服务增加
`--workspace /absolute/project --trusted-workspace`，新会话也须明确选择受信工作区
（机器创建命令如下；交互终端使用 `/new create confirm plan trusted-workspace`）。旧会话不会自动升级。

```sh
node terminal/cli.mjs --url http://127.0.0.1:7319 --new-session --format json --mode default --trusted-workspace
```

机器创建返回会话 ID 后，用 `--session SESSION_ID` 恢复该会话，不再次追加受信 opt-in。
仅 Read/Search；Apply/Undo/Exec/MCP 保持不可用。不得将数据目录放进工作区。
这不是抵御其他程序恶意替换文件的隔离，也不保证识别全部历史数据库副本。
切换/撤销 host 或工作区后，旧历史仍可读，但不能继续据旧绑定读文件。

真实 TTY 才支持交互 Ink。机器输出/SSE smoke 不等于 Windows ConPTY、所有输入法
或所有终端兼容验证。使用 `/help` 查看已实现命令，不承诺完整 Claude Code 兼容。

输入验收采用完整草稿回显后单独按 Enter。终端将文字与回车合并为同一输入块、
多行粘贴等输入边界尚未完成资格验证；粘贴后先核对草稿，再单独按 Enter。
本轮没有为了自动提交这些歧义输入而改变审批、粘贴或命令确认语义。

## 第三方材料

Node 依赖保留各自原始许可；Go 实际依赖模块与工具链许可见 THIRD-PARTY。
这些材料是构建来源清单，不是法律审查或许可兼容性保证。项目没有顶层LICENSE，
本包不替项目所有者选择许可，也不授予未明确授予的再分发权。
