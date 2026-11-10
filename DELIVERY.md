# personal-agent 重建源码预览 / Source preview

2026-11-10。Go 核心 + TypeScript CLI/Web。基于恢复的 `c61f3f7`，保留完整源码和 Git 历史；不推送、不发布、不迁移旧活动数据库。

## 当前能用什么

- Go 服务是会话、执行、权限、审批、事件和恢复状态的唯一权威；SQLite 保存可恢复状态，HTTP/SSE 向双端发送真实事件。
- TypeScript React/Ink 终端与 React/Vite Web 共用 SDK、严格协议校验和状态控制器。支持流式对话、会话/历史分页、停止、精确审批、断线重连及原请求只读核对。
- Linux 工作区文件读取、搜索与审批后修改；会话分支、压缩/回退、显式 FIFO 队列、有限后台 Jobs。命令与工作区能力由服务端决定，界面不自行授权。
- 已配置且明确选择的 stdio MCP；知识库本地检索及确认后入库；项目元数据创建、重命名、归档/恢复和新会话描述标签。
- 不可变 Skill 目录的 Go 内核、HTTP API、TS SDK/控制器。**已接入 Skill 目录 Web/Ink/机器 CLI 元数据生命周期；选中版本本身不授权执行；确定性只读执行内核、独立运行 API 与 Web/Ink/机器 CLI 已接入；语义评估保持 not_run。**

这是一份有明确边界的 rebuilt preview，不是全量 Claude Code 兼容实现。Hooks、MCP HTTP/Tasks，以及重建界面的若干管理写入能力仍未完成。不同项目标签如果使用同一工作区，会共享该工作区的知识范围；标签不提供隔离或权限。

## 先准备工具链

本次验证使用 Go 1.26.4、Node 24.22.1、pnpm 11.25.0、Linux amd64。需要 Python 3 才能运行真实 PTY 测试。首次依赖安装需要访问相应官方包源。

```sh
go version
node --version
pnpm --version
pnpm install --frozen-lockfile
make web-assets
mkdir -p bin
go build -tags webui \
  -ldflags "-X agent/internal/version.Version=0.0.0-rebuilt-preview -X agent/internal/version.Commit=$(git rev-parse --short HEAD 2>/dev/null || printf source-preview)" \
  -o bin/pachat ./cmd/pachat
```

`make web-assets` 编译并复制 Web 资源，`-tags webui` 才会嵌入页面。只运行 `go build` 会得到 API-only 版本。旧 `make release-build`、安装/升级脚本和 `docs/release/quickstart.md` 属于 legacy 发布流程，不应当作本次 preview 的发布或迁移入口。

## 不接模型也能先体验双端

以下只启动本机确定性测试服务，不是真实模型。临时工作区/数据库退出即清理，服务最长运行五分钟。不要把用户数据放入此演示工作区。

```sh
export PA_AGENT_TOKEN="$(node -e "process.stdout.write(require('crypto').randomBytes(32).toString('hex'))")"
go run -tags webui ./cmd/agent-fixture --addr 127.0.0.1:7319
```

在浏览器打开 `http://127.0.0.1:7319`，手动输入该环境变量的值，不要把令牌放到 URL。**先将新会话权限选为 `default`，再新建会话**，才能体验文件审批；网页默认的 `chat_only` 不提供文件工具。也可以选择下方终端创建的 `default` 会话。另一个终端设置相同 `PA_AGENT_TOKEN` 后运行：

```sh
pnpm --filter @personal-agent/terminal start --url http://127.0.0.1:7319 --mode default
```

可发送 `你好` 查看增量回复；发送 `edit` 查看临时 `note.txt` 的精确修改审批；发送 `wait` 后停止以体验取消。使用 `/help` 查看当前真实可用命令。两端选择相同会话 ID 可观察同一状态，不要把 fixture 回复当成模型推理结果。

## 接入你自己的模型

1. 复制 `configs/config.example.yaml`，设置 provider 的地址、环境变量名与注册模型 ID。示例模型名不是已安装模型的保证。实际 API key 只放环境变量或合适的私密存储，不放源码。
2. 准备两个互不包含的绝对目录：专用运行数据父目录和允许访问的代码工作区。不要指向旧生产数据。没有工作区时只能用 `chat_only`。
3. 设置私有 `PA_AGENT_TOKEN`，明确启动新服务：

```sh
bin/pachat serve-agent --config /absolute/path/config.yaml \
  --model REGISTERED_MODEL_ID \
  --runtime-parent /absolute/private/agent-runtime \
  --workspace /absolute/project \
  --addr 127.0.0.1:7319
```

服务仅监听 loopback。Web 使用同源页面；终端使用上面的 `start` 命令，或 `--session SESSION_ID` 恢复已有会话。非交互模式为：

```sh
pnpm --filter @personal-agent/terminal start --url http://127.0.0.1:7319 \
  --mode chat_only --print '你好' --format json
```

生产服务不会仅凭配置宣称模型支持工具。需要明确的合成探测证据才会启用相应能力；该探测会向你配置的模型发送一次合成请求，可能产生费用，具体命令及含义见 README 的 “Explicit provider tool probe”。本次验证没有向真实模型、用户的搜索服务或外部 MCP 服务发送请求。

## 不能省略的安全与兼容边界

- 任意命令 exec 默认关闭。Linux 的 `--non-sandbox-exec` 是明确选择的**非沙箱**能力，拥有当前 OS 用户权限；cwd 不限制它的访问范围。取消不保证后代进程终止。不要把它当作安全沙箱。
- MCP 启动同样是非沙箱的本地代码执行，只应用于可信、明确配置的服务器。发现工具不证明调用过工具。
- Windows/macOS 原生文件与 exec 能力未验收且保持禁用；交叉编译不等于这些系统的运行验证。Firefox/WebKit 和系统 IME 也未验收。
- 真实模型服务兼容性、真实推理质量、费用/usage 完整性均未验收。本地 fixture 只证明协议和流程，不证明模型质量。
- 未知写入/审批结果必须按原请求 ID 只读核对，不自动重放。停止、超时或连接断开不代表副作用没有发生。
- 新服务使用独立 `runtime-rebuild-v1` 命名空间，拒绝未知 schema；旧 preview 数据迁移未验收，不要用旧二进制写新库。
- 本次是源码交付，不含生产部署、自动更新、平台签名或公开发布。

## 复验

```sh
go test ./...
go vet ./...
go test -race ./internal/api ./internal/interaction ./internal/storage ./internal/model ./internal/mcp
pnpm test
pnpm typecheck
pnpm build
go run ./cmd/protocolgen
git diff --exit-code -- packages/protocol/src/contracts.ts protocol/v1/schema.json protocol/v1/skills.golden.json
make web-assets
CHROMIUM_PATH=/absolute/path/to/chromium node --test --test-concurrency=1 apps/web/test/*.test.ts
for test in apps/terminal/test/*pty*.py; do python3 "$test" || exit; done
```

浏览器测试使用实际 Chromium，PTY 测试使用实际 Ink 进程与临时 Go 服务；都不应针对用户的真实数据运行。归档外层的 `VERIFICATION.md` 和 `evidence/` 记录本次实际执行结果，不以历史报告替代新验证。

## English summary

This is a bounded, reproducible **source preview**, not a complete Claude Code replacement or a production release. Go owns durable state, permissions, effects and recovery; TypeScript React/Ink and React/Vite share the strict SDK and controllers. The archive contains source, full Git history, checksums and fresh verification evidence. No remote publication or old-data migration was performed.

Use the build commands above and `serve-agent`, not the legacy release/install path. The local `agent-fixture` provides deterministic, disposable five-minute demos; it is not a real model. Real usage requires your own configured model, private token, separate runtime directory and explicit workspace. A synthetic provider-tool probe is a separate intentional outbound request, never automatic.

Skill catalog metadata is available in Web, Ink and machine CLI through the strict shared controller. Separate deterministic read-only Skill-run endpoints and actual Web/Ink/machine clients are now available. Enabled remains only an exact metadata selection; every run needs its own preview and confirmation. Hooks, MCP HTTP/Tasks and some management mutations are unavailable. Linux exec and MCP startup are explicitly non-sandboxed and opt-in. Native Windows/macOS file/exec behavior, real providers, Firefox/WebKit and system IME remain unqualified. Project labels grant neither permissions nor knowledge isolation. Unknown effects require read-only reconciliation, never blind retries.


### Read-only Skill runs / 只读 Skill 运行

This is a bounded deterministic `readonly-workspace-v1` profile, not a legacy workflow/model bridge. Catalog structural review, enabled revision selection, run eligibility/admission, actual node execution and evaluation are separate. `completed` means the admitted native reads/searches succeeded; evaluation remains `not_run`, verification `not_evaluated`. Only Linux native fixtures are qualified, with no OS-sandbox claim. Hooks and other profiles are unavailable.

Web: open Skill catalog in an existing plan/default/acceptEdits session with supported native workspace binding. Under Read-only Skill execution, enter the exact catalog/revision IDs and a JSON object keyed by the manifest's same-named object inputs. Read accepts `{path:"note.txt"}`; search accepts `{paths:["note.txt"],query:"literal"}`. Preview is content-free; inspect scope/root/policy, compiled native calls, hashes, fixed budgets and expiry, then explicitly confirm. Read the admitted run, or use full-evidence run history/detail and exact stop/resume. No provider probe or hidden session is needed. chat_only and unsupported profiles remain blocked.

Ink: `/skill-run preview CATALOG REVISION INPUTS_FILE`, then `y`/`n` or `/skill-run confirm|cancel`. The file is a bounded regular UTF-8 file; its entire literal path remainder is used, with no shell parsing or server-path authority. Use `/skill-run list [CURSOR]`, `/skill-run show RUN`, `/skill-run stop RUN`, `/skill-run resume RUN`. Dismiss known results with Enter/Esc or `/skill-run dismiss`. Unknown responses permit `r` or `/skill-run recover [ORIGINAL_SESSION ORIGINAL_REQUEST]` only. Repeated confirmation keys do not approve an underlying tool, release its draft, or resend a request.

Machine: `--skill-run preview|confirm|cancel|lookup|list|show|stop|resume --session ID --format text|json`. Preview needs `--catalog ID --revision EXACT_ID --inputs-file PATH --request-id ID`. Confirm/cancel need `--operation-id ORIGINAL_PREVIEW --digest SHA256 --request-id DISTINCT_ID`. Show uses `--run-id ID`; stop/resume also need their own stable request ID. Original operation recovery is `--skill-run lookup --request-id ORIGINAL_ID`. Original control recovery additionally requires `--run-id ORIGINAL_RUN --control cancel|resume`; emitted `recovery_args` include these identities and the original daemon URL. One JSON document is stdout; unknown/unresolved exits 2, local/refused exits 1, known results exit 0 (stale admission exits 1). No automatic POST retry or hidden session.

The authenticated API is `/api/v1/sessions/{session}/skill-runs`: POST `/preview`, POST `/decision`, GET `/operations/{original_request}`, GET collection (`limit` 1..100, exact retained-run cursor), GET `/{run}`. Control recovery is GET `/controls/{run}/{cancel|resume}/{original_request}` over the existing control reservation and exact retained acknowledgment. Missing acknowledgment stays `needs_reconciliation`; GET never reconstructs an outcome or schedules work. Native controls remain `/api/v1/runs/{run}/cancel|resume`. SDK/UI history requests one full-evidence run per page and follows `next_cursor`.

Only recovery identity is persisted: Web sessionStorage `agent-skill-run-operation`, Ink `$XDG_STATE_HOME/personal-agent/skill-run-<daemon-url-hash>.json` (default `~/.local/state`). Run inputs, results, pins, manifests and tokens are not copied into recovery storage. Unknown prepare/confirm/cancel/control restores global session/navigation/approval quarantine before admission. If persistence fails, keep the displayed original IDs. Existing session events carry execution progress; full per-node observations and immutable pin provenance are available in run detail. A read that won the durable claim before revocation may finish; its actual result is retained. Disable/selection-away permanently blocks future nodes, even after re-enable/selection-back. Already observed reads cannot be undone.

Fixed limits: 16 attempts/nodes, 120-second admission deadline, 32 KiB canonical input, 64 KiB serialized result envelope per node, 1 MiB aggregate outputs/artifacts, 512 ordinary plus 64 control/recovery events. Lifetime reservations/retained copies are also bounded; capacity refusals do not silently truncate outputs, create another run or bypass confirmation. Logical payload limits do not promise SQLite/WAL physical size. No purge, real-provider qualification, MCP/network execution, platform widening or semantic evaluation is included.

中文：独立运行入口现已接入 Web、Ink 与机器 CLI，仅支持固定工作区的确定性只读读取/搜索。目录历史结构审核、选中版本、运行资格、准入、节点实际结果与语义评估严格分离。每次运行均需精确版本、JSON 输入、独立预览与明确确认；不调用模型，不创建隐藏会话。完成只代表原生读取/搜索成功，evaluation 始终为 `not_run`，verification 为 `not_evaluated`。未知响应及重载/重启后保持全局隔离，仅按原始会话/请求/运行/动作 GET 读取恢复，绝不重发 POST。只保存恢复标识，不保存输入、令牌、清单或结果。停用或切换版本会永久撤销后续节点；已完成的读取不能撤回，已有证据保留。限额、保留容量、支持平台及 Hooks 边界如上，不能将只读运行误认为沙箱或通用 Skill 执行器。
