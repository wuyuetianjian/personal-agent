# personal-agent 重建源码预览 / Source preview

2026-11-15 更新。Go 核心 + TypeScript CLI/Web。基于恢复的 `c61f3f7`，保留完整源码和 Git 历史；后续预览已推送公开测试分支 `test/rebuild-20261110`，没有合并 main、正式 Release 或迁移旧活动数据库。

## 当前能用什么

- Go 服务是会话、执行、权限、审批、事件和恢复状态的唯一权威；SQLite 保存可恢复状态，HTTP/SSE 向双端发送真实事件。
- TypeScript React/Ink 终端与 React/Vite Web 共用 SDK、严格协议校验和状态控制器。支持流式对话、会话/历史分页、停止、精确审批、断线重连及原请求只读核对。
- Linux 工作区文件读取、搜索与审批后修改；会话分支、压缩/回退、显式 FIFO 队列、有限后台 Jobs。命令与工作区能力由服务端决定，界面不自行授权。
- 已配置且明确选择的 stdio MCP；知识库本地检索及确认后入库；项目元数据创建、重命名、归档/恢复和新会话描述标签。
- 不可变 Skill 目录的 Go 内核、HTTP API、TS SDK/控制器。**已接入 Skill 目录 Web/Ink/机器 CLI 元数据生命周期；选中版本本身不授权执行；确定性只读执行内核、独立运行 API 与 Web/Ink/机器 CLI 已接入；语义评估保持 not_run。**

这是一份有明确边界的 rebuilt preview，不是全量 Claude Code 兼容实现。现已提供仅用于只读 Skill 运行的有界、会话拥有的声明式拒绝/审计 Hooks；可执行/通用 Hooks、MCP HTTP/Tasks，以及重建界面的若干管理写入能力仍未完成。不同项目标签如果使用同一工作区，会共享该工作区的知识范围；标签不提供隔离或权限。

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

本节 `default` 文件修改审批演示适用于已通过完整 Files 能力的 Linux。Mac/Windows 可改用 `--chat-only-fixture` 启动此测试服务，并以 `chat_only` 新建会话体验流式回复/停止；不要执行文件审批演示或把该 fixture 当作受信读搜验收。下文的显式受信入口使用真正的 `serve-agent`。

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
- Windows/macOS 完整 Files（含修改/撤销）、exec、MCP 能力保持禁用。另有下文显式受信工作区 Read/Search 预览；不可把这一窄范围验收视为完整平台支持。Windows 普通非管理员用户、Firefox/WebKit 和系统 IME 仍未验收。
- 真实模型服务兼容性、真实推理质量、费用/usage 完整性均未验收。本地 fixture 只证明协议和流程，不证明模型质量。
- 未知写入/审批结果必须按原请求 ID 只读核对，不自动重放。停止、超时或连接断开不代表副作用没有发生。
- 新服务使用独立 `runtime-rebuild-v1` 命名空间，拒绝未知 schema；旧 preview 数据迁移未验收，不要用旧二进制写新库。
- 当前是测试分支源码预览，不含生产部署、自动更新、平台签名或正式 Release；后续测试归档不改变这些限制。

Native readiness is tracked internally for Read, Search, Replace, Undo, Exec and MCP stdio; the public Files/Exec shape and Linux permissions are unchanged. Files still requires all four file operations. MCP adds its own native-readiness requirement to the existing Files, workspace, configuration and explicit session opt-in gates, independently of Exec. The original full Files/Exec gates on macOS, Windows and other non-Linux targets remain disabled; the separate explicit trusted Read/Search preview below is not that full bundle. Compile-only evidence is not native execution or support acceptance. Later activation requires real per-target filesystem/identity, reparse/symlink, ACL/DACL, locking/recovery, replacement/undo, exec and separate MCP qualification.

原生实现就绪状态在内部区分 Read、Search、Replace、Undo、Exec 与 MCP stdio；公开 Files/Exec 结构及 Linux 权限保持不变，Files 仍要求全部四种文件操作。MCP 在现有 Files、工作区、主机配置及会话明确启用条件上增加独立原生就绪门槛，不依赖 Exec。macOS、Windows 及其他非 Linux 目标原有完整 Files/Exec 门槛保持禁用；下文独立显式受信 Read/Search 预览不属于完整 Files。仅交叉编译不能证明原生执行或支持验收。后续启用必须完成各真实目标的文件系统/身份、reparse/symlink、ACL/DACL、锁定与恢复、替换/撤销、exec 及独立 MCP 验收。

## macOS / Windows 显式受信读搜预览

这是对用户明确选择的受信目录进行 Read/Search，不是恶意并发程序隔离沙箱。
不保证识别所有历史数据库副本，不开放 Apply、Undo、Exec 或 MCP。
默认不开启，也不会给历史会话自动升级权限；运行数据目录与工作区仍须分开。

在上述 `serve-agent` 命令中同时明确指定 `--workspace` 和 `--trusted-workspace`。
然后在 Web 新会话中选择 `plan` 或 `default`，明确勾选受信工作区选项；默认未勾选。
终端交互入口为 `/new create confirm plan trusted-workspace`。
机器模式新会话使用 `--new-session --format json --mode plan --trusted-workspace`，并显式指定服务 URL。
不允许与 `chat_only`、`acceptEdits`、MCP 或恢复既有会话混用来升级权限。
主机取消该选项或换工作区后，历史仍可读，但原会话不再有实时文件读取权限。

真实原生 CI 已验证 Mac/Windows 的适配器、明确会话准入、读取、撤权、换根及恢复。
Windows 验证使用管理员 runner，不能替代普通用户验收；`f107329` 的[过滤 token 原生诊断](https://github.com/wuyuetianjian/personal-agent/actions/runs/43668953680)证实派生候选仍为 High integrity，在启动子进程前被 exactly Medium 门槛拒绝；未降低安全断言。
详见[资格证据与剩余限制](docs/release/trusted-workspace-qualification.md)及[客户端说明](docs/release/trusted-workspace-clients.md)。
现有完整平台回归仍有失败，不能称 CI 全绿、正式 v1.0 或 Claude Code 全功能替代。

## 独立测试包构建（不是 v1.0 Release）

不要使用旧 `scripts/build-release.sh` 生成这一预览。新构建入口为：

```sh
node scripts/build-preview.mjs darwin-arm64 /absolute/new-preview-output
```

目标可选 `darwin-arm64`、`darwin-amd64`、`windows-amd64`、`windows-arm64`；输出目录必须尚不存在。
先安装上文锁定工具链和依赖。版本强制 `0.0.0-preview.<commit>`，包内含嵌入 Web 的服务、编译后的 TS 终端及其 JS 生产依赖、配置样例、中文启动说明、原始第三方许可和逐文件 SHA256 清单。
TS 终端仍需另外安装 Node 24，不是免运行时的单文件可执行程序，也不是安装器。

`Artifact-only preview packages` 工作流仅向 Actions Artifacts 保存通过本轮解压验证的候选，不创建 tag 或 GitHub Release。
请阅读 artifact 名称与日志：`native` 表示在匹配目标上运行对应 smoke；`verify-only` 只验证归档与编译，不能当作原生运行通过。
Windows ARM64 目前仅列为交叉构建候选；Windows ConPTY、普通非管理员环境、签名/notarization 和真实模型兼容性仍未验收。
测试包成功也不消除上文完整平台回归与安全边界。详见[打包范围](docs/release/preview-packages-design.md)和[包内启动说明](docs/release/preview-start.zh-CN.md)。

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

Skill catalog metadata is available in Web, Ink and machine CLI through the strict shared controller. Separate deterministic read-only Skill-run endpoints and actual Web/Ink/machine clients are now available. Enabled remains only an exact metadata selection; every run needs its own preview and confirmation. Bounded session-owned declarative deny/audit Hooks are available for read-only Skill runs. Executable/general Hooks, MCP HTTP/Tasks and some management mutations remain unavailable. Linux exec and MCP startup are explicitly non-sandboxed and opt-in. The separate explicit trusted Read/Search preview has native evidence; complete Windows/macOS Files/Exec, ordinary non-administrator Windows, real providers, Firefox/WebKit and system IME remain unqualified. Project labels grant neither permissions nor knowledge isolation. Unknown effects require read-only reconciliation, never blind retries.


### Read-only Skill runs / 只读 Skill 运行

This is a bounded deterministic `readonly-workspace-v1` profile, not a legacy workflow/model bridge. Catalog structural review, enabled revision selection, run eligibility/admission, actual node execution and evaluation are separate. `completed` means the admitted native reads/searches succeeded; evaluation remains `not_run`, verification `not_evaluated`. Only Linux native fixtures are qualified, with no OS-sandbox claim. Session-owned declarative deny/audit Hooks for this profile are available as described below; executable/general Hooks and other execution profiles remain unavailable.

Web: open Skill catalog in an existing plan/default/acceptEdits session with supported native workspace binding. Under Read-only Skill execution, enter the exact catalog/revision IDs and a JSON object keyed by the manifest's same-named object inputs. Read accepts `{path:"note.txt"}`; search accepts `{paths:["note.txt"],query:"literal"}`. Preview is content-free; inspect scope/root/policy, compiled native calls, hashes, fixed budgets and expiry, then explicitly confirm. Read the admitted run, or use full-evidence run history/detail and exact stop/resume. No provider probe or hidden session is needed. chat_only and unsupported profiles remain blocked.

Ink: `/skill-run preview CATALOG REVISION INPUTS_FILE`, then `y`/`n` or `/skill-run confirm|cancel`. The file is a bounded regular UTF-8 file; its entire literal path remainder is used, with no shell parsing or server-path authority. Use `/skill-run list [CURSOR]`, `/skill-run show RUN`, `/skill-run stop RUN`, `/skill-run resume RUN`. Dismiss known results with Enter/Esc or `/skill-run dismiss`. Unknown responses permit `r` or `/skill-run recover [ORIGINAL_SESSION ORIGINAL_REQUEST]` only. Repeated confirmation keys do not approve an underlying tool, release its draft, or resend a request.

Machine: `--skill-run preview|confirm|cancel|lookup|list|show|stop|resume --session ID --format text|json`. Preview needs `--catalog ID --revision EXACT_ID --inputs-file PATH --request-id ID`. Confirm/cancel need `--operation-id ORIGINAL_PREVIEW --digest SHA256 --request-id DISTINCT_ID`. Show uses `--run-id ID`; stop/resume also need their own stable request ID. Original operation recovery is `--skill-run lookup --request-id ORIGINAL_ID`. Original control recovery additionally requires `--run-id ORIGINAL_RUN --control cancel|resume`; emitted `recovery_args` include these identities and the original daemon URL. One JSON document is stdout; unknown/unresolved exits 2, local/refused exits 1, known results exit 0 (stale admission exits 1). No automatic POST retry or hidden session.

The authenticated API is `/api/v1/sessions/{session}/skill-runs`: POST `/preview`, POST `/decision`, GET `/operations/{original_request}`, GET collection (`limit` 1..100, exact retained-run cursor), GET `/{run}`. Control recovery is GET `/controls/{run}/{cancel|resume}/{original_request}` over the existing control reservation and exact retained acknowledgment. Missing acknowledgment stays `needs_reconciliation`; GET never reconstructs an outcome or schedules work. Native controls remain `/api/v1/runs/{run}/cancel|resume`. SDK/UI history requests one full-evidence run per page and follows `next_cursor`.

Only recovery identity is persisted: Web sessionStorage `agent-skill-run-operation`, Ink `$XDG_STATE_HOME/personal-agent/skill-run-<daemon-url-hash>.json` (default `~/.local/state`). Run inputs, results, pins, manifests and tokens are not copied into recovery storage. Unknown prepare/confirm/cancel/control restores global session/navigation/approval quarantine before admission. If persistence fails, keep the displayed original IDs. Existing session events carry execution progress; full per-node observations and immutable pin provenance are available in run detail. A read that won the durable claim before revocation may finish; its actual result is retained. Disable/selection-away permanently blocks future nodes, even after re-enable/selection-back. Already observed reads cannot be undone.

Fixed limits: 16 attempts/nodes, 120-second admission deadline, 32 KiB canonical input, 64 KiB serialized result envelope per node, 1 MiB aggregate outputs/artifacts, 512 ordinary plus 64 control/recovery events. Lifetime reservations/retained copies are also bounded; capacity refusals do not silently truncate outputs, create another run or bypass confirmation. Logical payload limits do not promise SQLite/WAL physical size. No purge, real-provider qualification, MCP/network execution, platform widening or semantic evaluation is included.

中文：独立运行入口现已接入 Web、Ink 与机器 CLI，仅支持固定工作区的确定性只读读取/搜索。目录历史结构审核、选中版本、运行资格、准入、节点实际结果与语义评估严格分离。每次运行均需精确版本、JSON 输入、独立预览与明确确认；不调用模型，不创建隐藏会话。完成只代表原生读取/搜索成功，evaluation 始终为 `not_run`，verification 为 `not_evaluated`。未知响应及重载/重启后保持全局隔离，仅按原始会话/请求/运行/动作 GET 读取恢复，绝不重发 POST。只保存恢复标识，不保存输入、令牌、清单或结果。停用或切换版本会永久撤销后续节点；已完成的读取不能撤回，已有证据保留。限额、保留容量、支持平台及 Hooks 边界如上，不能将只读运行误认为沙箱或通用 Skill 执行器。

Declarative Hook kernel and clients: Go/SQLite schema-2 read-only Skill snapshots, reviewed session policy selection, before-claim deny and causal audit receipts are implemented through the public Hook API and TypeScript Web/Ink/machine controls. New run previews/admissions include the complete schema-2 policy snapshot; clients strictly read retained schema-1 and schema-2 evidence without dropping fields. Executable/general Hooks, model/Jobs hooks, writes, provider/MCP execution, new native platforms and a release remain outside scope; this is not an OS sandbox or semantic verification. Logical bounds are 16 MiB of policy management reservation and a 160 KiB Hook slab inside each existing 8 MiB Skill execution reservation; these are not SQLite/WAL physical-size promises.

中文：声明式 Hook 内核及客户端现通过公开 Hook API 与 TypeScript Web/Ink/机器控件提供 Go/SQLite schema-2 只读 Skill 快照、经审核的会话策略选择、调用声明前拒绝以及与实际结果同事务的审计回执。新运行预览/准入包含完整 schema-2 策略快照；客户端严格读取保留的 schema-1 和 schema-2 证据，不丢弃字段。可执行/通用 Hooks、模型/Jobs Hooks、写入、provider/MCP 执行、新原生平台及发布仍不在范围内，也不提供操作系统沙箱或语义验证。策略管理逻辑预留上限为 16 MiB，每个 Skill 的现有 8 MiB 预算内固定预留 160 KiB；这些不是 SQLite/WAL 物理文件大小保证。

## Declarative Hooks client stage / 声明式策略客户端阶段

Added bounded session-owned policy management to the existing API, generated strict v1/v2 contracts, shared SDK quarantine, Web, Ink and machine entry points. New run admission always pins the reviewed policy snapshot; legacy reads remain exact. This is the same execution owner, native allowlist, original control receipt route and immutable context-kind separation, with no extra scheduler, callback runtime, model/Jobs hook, platform qualification or publication.

交付边界：仅声明式 `before_node` 拒绝及 `after_node`/`run_terminal` 审计；分别确认导入、启用/禁用和运行。丢失响应仅按原 ID GET 核对，恢复不自动确认、不重发 POST，客户端仅保存恢复 ID。选择改变永久撤销旧的未结束运行，禁用后重新启用也不会恢复。容量是逻辑保留上限，不提供清理/退款或 SQLite/WAL 物理大小承诺。执行观察不代表评估或验证通过（`not_run` / `not_evaluated`）。

Verification commands include `go test ./...`, `go vet ./...`, affected-package `go test -race`, `pnpm test`, `pnpm typecheck`, `pnpm build`, protocol generation/parity (including both Skill goldens and `hooks.golden.json`), actual Chromium `apps/web/test/hooks.test.ts`, compiled real machine entry tests, and `hooks_pty.py`/`hooks_recovery_pty.py`. Command availability is not a claim that a particular checkout passed; inspect the actual command results for the exact revision under review. Owned offline fixtures are the only permitted runtime evidence. No real provider/MCP/service, non-test run, user-data migration, merge, push or release is part of this stage.
