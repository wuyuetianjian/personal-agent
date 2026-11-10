# personal-agent

**Start here / 从这里开始：** [交付范围、启动步骤与限制 / Delivery scope, startup and limits](DELIVERY.md)。本次交付是可验证的源码预览，不是 Claude Code 全功能替代品。后文保留阶段历史；当前范围以该交付说明为准。

## Rebuilt preview: Go service + shared TypeScript foundation / 重建预览

This is a **rebuilt preview**, not recovery of the unavailable `b48da400` source or a formal release. The legacy CLI command surface is retained; MCP transport safety updates are documented below. The new isolated service, shared SDK, React/Ink terminal, React/Vite Web and noninteractive print modes are implemented as local preview clients. Local browser/PTY acceptance is described below; this is not a production release or real-provider compatibility claim.

这是**重建预览**，不是恢复 `b48da400` 源码，也不是正式发布。保留旧 CLI 命令界面；MCP 传输安全更新见下文。现已提供独立 Go 服务、共享 TS SDK、React/Ink 终端、React/Vite Web 和 print 模式的本地预览，浏览器/PTY 本地验收见下文；不代表正式发布或真实模型服务兼容性已验证。

### Skill catalog clients / Skill 目录客户端

The rebuilt immutable skill catalog now exposes authenticated session-scoped HTTP validation, staged import, exact revision enable/disable, original-request recovery and bounded catalog/version reads, plus a generated strict TypeScript SDK and durable shared controller. **Web, Ink and machine CLI now expose this metadata-only lifecycle, sharing the strict SDK and quarantine controller.** Web offers the separate “Skill catalog / 技能目录” panel; Ink offers `/skills help`; machine CLI offers `--skill` with an existing `--session`. The legacy `pachat skill` commands and legacy runtime/registry are not adapters for this catalog. See [the kernel contract](SKILL_CATALOG.md).

A preview stores private manifest bytes but creates no catalog/revision before confirmation. Input `status: active` never activates anything: a new catalog is disabled. Adding a version never changes its selection. Enable pins one exact immutable revision; **enabled ≠ evaluated ≠ executable**. Every target is reported `unsupported`, evaluation is `not_run`, and execution is `unavailable` with `bridge_not_implemented`. No evaluation pass can be supplied by a client. Nothing here invokes workflows, Jobs, models, tools, MCP, processes or retrieval.

The strict `strict-manifest-v1` profile accepts only explicit UTF-8 YAML text, at most 64 KiB, one document, with fixed fields and bounds. It rejects unknown/duplicate keys, anchors/aliases/explicit tags, type coercion, malformed Unicode, cycles and ambiguous targets. Catalog visibility is service-local metadata across bound sessions; it grants no workspace/project/runtime authority. Metadata operations are allowed in `plan` and `chat_only` as well as `default` and `acceptEdits`, with exact confirmation and policy/scope rechecks. They do not inherit knowledge-indexing permissions. Retained artifacts and operations have hard capacity limits and no automatic purge.

重建后的不可变 Skill 目录已提供会话绑定的 HTTP 校验、导入预览、指定版本启用/停用、原始请求恢复和有界读取，以及生成的严格 TypeScript SDK 与可恢复控制器。**Web、Ink 与机器 CLI 现已接入同一严格 SDK 和隔离控制器，仅处理目录元数据。** Web 使用独立“Skill catalog / 技能目录”面板，Ink 使用 `/skills help`，机器 CLI 使用 `--skill` 并指定已有 `--session`。旧 `pachat skill` 命令、runtime 和 registry 不连接这个新目录。详见[内核契约](SKILL_CATALOG.md)。

预览只保留私有 manifest 原文，确认前不创建目录或版本。导入 `status: active` 仍然得到停用目录；新增版本不切换当前选择。启用只选择一个明确的不可变版本，**已启用 ≠ 已评估 ≠ 可执行**：所有目标均标记 `unsupported`，评估为 `not_run`，执行为 `unavailable / bridge_not_implemented`，不接受客户端提供的评估通过状态。不会调用 workflow、Jobs、模型、工具、MCP、进程或检索。

`strict-manifest-v1` 只接收不超过 64 KiB、单文档、字段明确且有界的 UTF-8 YAML 文本；拒绝未知/重复键、锚点/别名/显式标签、隐式类型转换、无效 Unicode、依赖环和歧义目标。目录属于服务本地元数据，可由绑定会话查看，不授予工作区、项目或执行权限。`plan`、`chat_only`、`default`、`acceptEdits` 均可进行这些明确确认的元数据操作，确认时重核 scope/policy，不沿用知识索引权限。保留内容与操作数量/字节有硬上限，不自动清理。

### Start the separate service / 启动独立服务

```sh
go build -o bin/pachat ./cmd/pachat
# Supply a nonempty token via PA_AGENT_TOKEN or a private 0600 --auth-file.
# 通过 PA_AGENT_TOKEN 或权限 0600 的 --auth-file 提供非空令牌；不要放入 URL。
bin/pachat serve-agent --config configs/config.example.yaml --model <registered-model-id> \
  --runtime-parent /absolute/private/agent-rebuild --workspace /absolute/project \
  --auth-file /absolute/private/agent-token --addr 127.0.0.1:7319
```

`--runtime-parent` is mandatory. Only its new `runtime-rebuild-v1/agent.db` is opened. Unknown existing namespaces/databases are refused before migration; old preview data is never adopted. A kernel process lock excludes a second service and is acquired before offline recovery. Do not point legacy tools at this namespace. Migration/import from the previous preview remains unverified. The workspace must be separate from the runtime directory. Omitting `--workspace` allows explicit `chat_only` sessions only; it never authorizes the current directory.

必须指定 `--runtime-parent`，只使用其中独立的 `runtime-rebuild-v1/agent.db`。未知命名空间或旧数据库拒绝迁移，不接管旧预览数据；启动先获取内核进程锁，再做离线恢复。不要让旧工具写此命名空间。旧预览迁移尚未验证。工作区与运行数据必须分离；省略工作区时只能显式创建 `chat_only` 会话，绝不会默认授权 cwd。

Schema recognition is stricter than the namespace marker: under the process lock, existing databases receive a read-only, committed-WAL-aware check **before** writable open, migration or application recovery. The migration ledger must be an exact contiguous prefix of this build's embedded `0001`–`0029` filenames, and user tables/indexes/triggers must match the catalog produced by those migrations (with the known optional provider-evidence table). Unknown, renamed, gapped or inconsistent histories are refused without schema repair. Equivalent but differently authored schemas can be refused too. This checks catalog shape, not business-record validity, database provenance or old-preview/import compatibility. SQLite may rebuild WAL shared-memory bookkeeping; the preflight does not checkpoint or rewrite the database/committed WAL. Keep noncooperating SQLite writers out of this namespace. Each migration and its ledger entry remain atomic; the whole upgrade batch is not one transaction.

Schema 识别不只看命名空间标记：取得进程锁后，先以只读方式检查现有数据库及已提交 WAL，再进行可写打开、迁移和应用恢复。迁移记录必须是本构建内嵌 `0001`–`0029` **完整文件名**的连续前缀，用户表、索引、触发器定义须匹配该前缀生成的 schema（仅额外允许已知 provider-evidence 表）。未知、改名、缺号或不一致状态直接拒绝，不自动修复；另行编写但语义相同的 schema 也可能拒绝。检查不证明业务记录有效、来源可信或旧预览/导入兼容。SQLite 可能重建 WAL 共享内存记录，但只读预检不会 checkpoint 或改写数据库及已提交 WAL。不要让不遵守进程锁的工具写入此目录。每个迁移及其记录原子提交，但整个升级批次不是单一事务。


API requests require `Authorization: Bearer <token>`. Every POST additionally requires `X-Agent-Protocol: personal-agent-rebuild-v1`; missing/wrong flavor returns HTTP 426 before mutation. `/status` returns that exact `protocol` identifier. The SDK verifies this handshake before every write, so an old preview sharing API major 1 is not treated as compatible. Host must match the bound loopback IP/port; browser Origin must be same-origin. Cross-site requests, URL credentials, unrecognized JSON fields and duplicate fields are rejected. Tokens belong only in the CLI environment/private file and browser memory, never URLs, logs or localStorage. The dedicated server exposes no legacy routes. CSP has no unsafe-inline/unsafe-eval; Web assets are bundled only by the explicit Web build below.

每个 POST 还必须携带 `X-Agent-Protocol: personal-agent-rebuild-v1`，否则在写入前返回 426；SDK 每次写前验证 `/status` 的相同标识，不因旧预览也使用 major 1 就假定兼容。API 强制 Bearer 认证、loopback、精确 Host 和浏览器同源 Origin；拒绝跨站、URL 令牌、未知与重复 JSON 字段。令牌仅放 CLI 环境/私有文件和浏览器内存，禁止 URL、日志和 localStorage。新服务不暴露旧路由，CSP 不允许 unsafe-inline/unsafe-eval，仅下文明确的 Web 构建会打包 UI。

### API v1 and SDK / API 与 SDK

- `GET /api/v1/status`, `GET /api/v1/sessions[?before=<session-id>]` (100/session page).
- `POST /api/v1/sessions`: `{request_id, mode, model_id?, mcp?: boolean}`. Mode is explicitly `chat_only`, `default`, `plan`, or `acceptEdits`. No client-supplied root/project ID is accepted.
- `POST /api/v1/sessions/:id/messages`: `{request_id, content}`.
- `GET /api/v1/sessions/:id/snapshot`, `/context`, `/mcp` (read-only), `/history[?before=<next_before>]`.
- `GET /api/v1/sessions/:id/events?after=<sequence>`: fetch-based authenticated SSE. `after` is exclusive; schemas use `schema_version: 1`, separate from aggregate run version. Up to 256 events/read, 100 ms live polling, 16 streams, 5-second write deadlines. A cursor ahead of the head or >512 behind receives `event: snapshot`. Snapshot/cursor share one DB read transaction. History returns 50 runs/page; old pages are not replacements for current live state.
- `POST /api/v1/runs/:id/cancel` and `/resume`: `{request_id}`. Cancel acknowledges intent; `cancelling` remains active until execution stops. Approval-paused cancellation is deliberately drained.
- `GET /api/v1/approvals/:id/review`: authenticated tagged review `{kind:"edit",edit:{...}}` or `{kind:"exec",exec:{...}}` or `{kind:"mcp",mcp:{...}}`. MCP binds server/tool/validated arguments/schema/configuration/process generation and non-sandbox risk. Edit keeps immutable exact old/new text, hashes, path and scope; exec exposes exact host-prepared command, environment, bounds and non-sandbox warning. `POST .../:id/decision`: `{request_id, digest, decision: "approved" | "denied"}`.

At executor saturation, a new message is rejected with `503 service_capacity` before run admission. Already acknowledged approval/cancel drains retain bounded service-owned slots (20 total workers, including up to four active executors). Only the distinct pre-ownership capacity outcome is retried; live/unknown ownership and reconciliation are never blindly retried. Pending capacity is observable as `run.waiting_capacity`; cancellation remains visibly `cancelling` until stopped. Completed journal effects recovered after a crash emit one identity-bound `tool.recovered` observation atomically with checkpoint/terminal state, so cancelled runs still show effects that actually completed.

执行容量满时，新消息在创建 run 前以 `503 service_capacity` 拒绝。已确认的审批/取消保留有界排队槽位（最多 20 个工作项，其中最多 4 个正在执行）。只重试明确的“尚未获得执行权、容量已满”结果，不盲重试未知执行权或副作用。`run.waiting_capacity` 表示容量等待；取消始终保持 `cancelling`，直到执行停止。崩溃后已完成 journal 的工具结果通过身份绑定的 `tool.recovered` 与 checkpoint/终态原子保存，取消也不掩盖已经完成的实际效果。

Every write needs a distinct caller-chosen request ID. Repeating an already recorded identical operation returns its saved acknowledgement; reusing the ID for different input conflicts. There is **no automatic write retry**. A lost/unknown response must be checked via snapshot/history before any deliberate retry. Provider errors are projected as stable safe error codes, not raw credentials/config/checkpoints. On resume, saved model identity and effective non-secret endpoint, trust, privacy and capability policy must match; `provider_changed` requires restoring the old binding or explicitly starting a new session. Same-endpoint credential rotation is allowed without storing keys.

每次写操作需独立 request ID；已记录的相同操作返回原确认，复用 ID 改参数则冲突。**不会自动重试写操作**，未知结果先检查快照/历史。错误、事件与快照只投影可显示状态，不返回原始 checkpoint、凭据或配置。恢复会比较保存的模型及有效端点、信任、隐私和能力绑定；变化返回 `provider_changed`，不会把历史自动发往新地址。同端点换密钥无需持久化密钥。

```ts
import { AgentClient } from '@personal-agent/client';
import { emptyState, reduceSession } from '@personal-agent/protocol';
const client = new AgentClient({ baseURL: 'http://127.0.0.1:7319', token: () => tokenInMemory });
const session = await client.createSession('chat_only', crypto.randomUUID());
let view = emptyState(session.id);
const abort = new AbortController();
// Run the watcher independently of the submit handler.
const watching = (async () => {
  for await (const frame of client.watchSession(session.id, { signal: abort.signal })) {
    view = reduceSession(view, frame); // Presentation only; never sends an action.
  }
})();
await client.submitMessage(session.id, '你好', crypto.randomUUID());
// Later: abort.abort(); await watching;
```

`watchSession` snapshots on mount, checks contiguous cursors and refreshes gaps. Read-only SSE reconnects are bounded (default 5; capped exponential backoff); socket/body failures and incomplete frames preserve the last accepted cursor. Auth/schema failures stop immediately. All writes are one-shot. `cancelRun`, `resumeRun`, `decideApproval`, `approvalReview`, `history`, `context`, `listSessions` and `status` are available. Shared `commands` has no standalone exec shortcut; exec availability comes from the session capability, not a UI toggle. Compact/plugins remain unavailable. `safeTerminalText` removes control/ANSI escape sequences; future Markdown renderers must disable raw HTML.

`watchSession` 挂载先读快照，按序去重并检测缺口；只读 SSE 最多重连 5 次并指数退避，断流保留最后游标，认证/协议错误立即停止。写操作仅发一次。reducer 只处理展示，刷新不会重发消息/审批。共享命令表明确标记未实现能力；终端使用 `safeTerminalText`，未来 Markdown 渲染必须禁用原始 HTML。

### Verification and boundaries / 验证与边界

```sh
# Go 1.26.4+, Node 24.22.1, pnpm 11.25.0
pnpm install --frozen-lockfile
pnpm test && pnpm typecheck && pnpm build
go test -race ./internal/api ./internal/interaction ./internal/cli ./internal/apifixture
go test ./...
go run ./cmd/protocolgen # regenerate public DTOs + protocol/v1/schema.json
# Offline real-Go fixture, token still required; emits URL/workspace, never token:
PA_AGENT_TOKEN=<test-token> go run ./cmd/agent-fixture
```

`pnpm test` includes the real Go fixture server, genuine emitted deltas, exact edit approval, native temp-workspace edit, answer, reload and cancellation. Go and TS share `protocol/v1/replay.golden.json`. This is **not real vLLM/NewAPI verification**, browser testing, or a UI completion claim. Linux file capabilities remain bounded native operations, not an OS sandbox. Windows/macOS native file tools remain disabled; explicit chat-only sessions retain durable protected history and budgets on every platform. Exec is off by default; Linux has the separately approved non-sandbox opt-in described below. Windows/macOS exec remains unavailable. Production CLI advertises no model tools without matching explicit synthetic probe evidence; configuration flags alone never attest support. Existing privacy gateway remains fail-closed. No automatic reconciliation of unknown tool outcomes, old-data migration, remote publishing, or billing attestation is provided; displayed context is a conservative estimate and cost is unknown.

测试包含真实 Go 本地服务、真实发出的增量、精确审批、临时目录原生修改、回答、刷新与取消，Go/TS 共用 golden。这**不是 vLLM/NewAPI 实测、浏览器测试或 UI 完成证明**。Linux 文件操作不是 OS 沙箱；Windows/macOS 文件工具禁用，但显式 chat-only 保留持久受保护历史与预算。exec 默认关闭；Linux 可通过下述独立非沙箱选项启用，Windows/macOS exec 仍不可用；正式 CLI 在模型工具支持未验证时不广告工具，可通过下述显式合成探测保存匹配证据。未知副作用不自动重试/和解，旧数据不迁移，无远程发布，费用无账单证明。

### Explicit provider tool probe / 显式模型工具探测

Stop `serve-agent`, then deliberately run against your configured model:

```sh
bin/pachat probe-agent-provider --config configs/config.example.yaml \
  --model <registered-model-id> --runtime-parent /absolute/separate/agent-runtime \
  --send-synthetic
```

`--send-synthetic` explicitly authorizes one synthetic request to the configured endpoint and ordinary API usage charges. It sends a random nonce and one harmless tool schema, never workspace files or conversation history, and **never executes any tool**. There are no automatic probes at startup, selection, session creation or retry. The real streaming adapter must receive exactly the expected tool call/nonce, strict arguments, one `tool_calls` finish and a fully framed `[DONE]`. Limits: 20 seconds, 256 requested output tokens, 64 KiB wire response, 4 KiB text, 1 KiB arguments. Text-only claims, invalid/incomplete responses and timeout stay unverified. Redirects are refused. Bounded, allowlisted diagnostics distinguish provider auto-tool-choice configuration rejection, system-message ordering rejection, invalid arguments and other protocol failures; raw provider errors are never stored or shown. This narrow evidence says nothing about incremental text, usage/billing reporting, other schemas or broad provider compatibility.

Versioned, nonsecret evidence is stored atomically in the private, exclusively locked `runtime-rebuild-v1/agent.db` (`interaction_provider_evidence`), not legacy config or data paths. The service and probe use the **same** explicit runtime parent and cannot run concurrently. Evidence pins effective endpoint, registered/provider/model identity and nonsecret execution policy, not API key values or environment-variable names. A successful probe only permits configured `tool_calling` capability; config flags alone cannot verify it. Missing/corrupt/incompatible evidence or changed configuration fails closed; key rotation alone does not invalidate it. Once an attempt starts, prior evidence is invalidated before sending, including on failure/interruption. Invalid configuration/consent/lock failures send nothing. Evidence records store neither request/output nor credentials.

Every successful attempt has a fresh generation. **Create a new workspace session after probing**: existing workspace sessions, approvals and checkpoints cannot silently gain or regain authority and return `provider_changed` when their saved binding differs. History/review and cancellation remain available. Chat-only sessions ignore tool-evidence changes but still pin endpoint/model/policy and never acquire tools. `/api/v1/status` exposes safe `tool_capability` and `tool_capability_reason`; session DTOs expose `tool_capability` plus `reason`, with tool/exec flags disabled when their binding is no longer current. `verified_synthetic` is not an OS security boundary: native file platform gates, separate non-sandbox exec host/session opt-in and fresh exact approval still apply. Evidence has no expiry or remote model-revision attestation; intentionally re-probe after endpoint-side model changes. Only local fixtures have been tested here, **not a real provider**.

先停止 `serve-agent`，再使用同一配置、注册模型 ID 和 `--runtime-parent` 主动执行上述命令。`--send-synthetic` 明确同意向配置的端点发送一次合成请求，可能产生正常 API 费用。仅发送随机 nonce 与无害工具 schema，不发送工作区文件或聊天历史，**不执行任何工具**，也不会在启动、选模型、创建会话或重试时自动探测。实际流适配器须验证唯一预期工具调用、严格参数与 nonce、一次 `tool_calls` 结束及完整 `[DONE]` 帧。限时 20 秒，请求最多 256 输出 token，响应总量 64 KiB、文本 4 KiB、参数 1 KiB；仅文本声称支持、无效/不完整响应、超时均不算验证，拒绝重定向。错误体有界读取并仅保留白名单分类，区分 provider 的 auto-tool-choice 配置拒绝、system-message 顺序拒绝、无效参数和其他协议错误，不保存或展示原始错误。这不是增量文本、usage/账单、其他工具 schema 或普遍兼容性的证明。

证据以版本化非秘密记录原子保存在独立私有且独占加锁的 `runtime-rebuild-v1/agent.db` 中；探测与服务不可同时运行。绑定实际端点、模型/provider 身份与非秘密执行策略，不保存凭据、请求或输出。配置中的 `tool_calling` 只是前提，不能代替证据。证据缺失、损坏、版本/配置不匹配均关闭工具；仅轮换 API key 不影响身份。每次有效探测开始前先撤销旧证据，失败或中断也不会沿用旧成功结果；配置、授权参数或锁检查失败时不发送请求。

每次成功探测都有新代次，**探测后创建新的工作区会话**。旧会话、审批和检查点不能自动获得或恢复权限，绑定不同会返回 `provider_changed`；仍可查看历史/审阅内容或取消。chat-only 不受无关工具证据变化影响，但继续绑定端点/模型/策略且永远不获得工具。状态与会话提供安全的能力状态/原因；绑定失效会关闭展示中的工具/exec 标记。合成证据不是 OS 沙箱，原生文件平台门禁、非沙箱 exec 独立主机/会话开关与逐次精确审批仍全部必需。证据没有自动有效期或远程模型版本证明，端点背后的模型改变后需主动重新探测。目前仅有本地 fixture 验证，**没有真实 provider 兼容性实测**。


### Optional NON-SANDBOXED exec / 可选非沙箱命令

Linux only: add `--non-sandbox-exec` to `serve-agent` **only if you intend current OS-user access**. It requires an explicit workspace, copies opt-in into each new workspace session, and cannot be enabled by HTTP/model arguments or `acceptEdits`. Existing sessions keep their saved policy; `plan` and `chat_only` never execute commands. The provider-evidence gate is independent and requires matching explicit probe evidence: this flag alone does **not** enable model tools there.

Every call, including `acceptEdits`, requires a fresh approval bound to its executable path/identity/SHA256, exact argv, cwd/workspace identity, fixed environment policy, explicit environment changes and effective environment, timeout and risk warning. No implicit shell expansion occurs; requesting `/bin/sh -c ...` shows that exact script. The default environment is only `PATH=/usr/bin:/bin` and `LANG=C.UTF-8` plus explicit changes; no host/model/API/auth variables are inherited. The process can nevertheless read files, contact the network and access credentials available to the OS user. **Cwd is not confinement; this is not a secret-isolation boundary and does not weaken or replace the native file-tool boundary.**

Only the executable file is fingerprinted and pinned, not its interpreter/shared-library dependencies or mutable OS-user resources. Linux launches via `/proc/self/fd/3`; shebang scripts see that pinned script name (shown in review). Required kernel/filesystem/proc primitives are checked at operation time. Stdout/stderr retain at most 4 KiB each, flag truncation, and preserve non-UTF-8 retained bytes in optional base64 fields. The direct child's exit code/signal is recorded; clients must still sanitize terminal/HTML display.

Timeout is explicit, 1–60,000 ms. Cancellation uses PID-safe **direct-child-only** kill and synchronous wait; it may leave descendants running. Pipe drains are capped at 150 ms, including descendant-inherited pipes. An OS child stuck in uninterruptible kernel wait can delay shutdown; the service does not abandon it or claim stopped. Timeout, cancellation or ambiguous output/wait results enter `needs_reconciliation`, publish `tool.uncertain` facts and never auto-rerun. A normal direct-child exit is **not** proof every descendant exited: results always report `descendants_stopped:false`. Windows/macOS exec stays unavailable; cross-compilation is not native qualification.

仅 Linux 可加 `--non-sandbox-exec`，明确授权**当前 OS 用户权限下的非沙箱执行**。必须指定工作区；开关只复制到新工作区会话，不可通过 HTTP、模型参数或 `acceptEdits` 打开。旧会话策略不变，`plan`/`chat_only` 禁止命令。真实 provider 工具证据门禁独立存在，正式 CLI 不会仅因该开关就开始调用工具。

每次执行（含 `acceptEdits`）都要重新审批，绑定绝对可执行文件身份/哈希、完整 argv、cwd/工作区身份、固定环境策略、明确环境改动及最终环境、超时与风险。没有隐式 shell 展开；显式 shell 的脚本全文可审阅。仅传固定 PATH/LANG 和明确改动，不继承模型/API/认证环境变量，但进程仍能访问当前用户可用的文件、网络和凭据。**cwd 不构成隔离，最小环境不隔离秘密；原文件工具边界保持不变。**

可执行文件本身使用指纹和句柄绑定，解释器/动态库及同用户可写资源不冻结；脚本会看到 review 中声明的 `/proc/self/fd/3` 名称。每路输出最多保留 4 KiB，标明截断，非 UTF-8 字节另存 base64。超时 1–60,000 ms；取消只终止并等待直接子进程，后代可能继续运行，继承管道最多等待 150 ms。内核不可中断等待可能延长关闭，绝不提前声称已停止。超时/取消/结果不明进入 `needs_reconciliation` 并展示 `tool.uncertain`，不自动重跑；直接子进程正常退出也不代表后代全部退出。Windows/macOS exec 仍禁用，交叉编译不冒充原生验证。

### Configured MCP in rebuilt sessions / 重建会话中的显式 MCP

**Default off, Linux workspace sessions only.** Neither the old config's MCP `enabled` flag nor merely starting/viewing `serve-agent` launches a server. Supply a separate private (0600, at most 64 KiB) JSON file with `--mcp-config /absolute/private/mcp.json`. Example shape below uses a placeholder, not an installed helper:

```json
{
  "enabled": true,
  "servers": [{
    "id": "my-trusted-server",
    "command": "/absolute/trusted/server",
    "args": [],
    "environment": [],
    "directory": "/absolute/trusted/cwd",
    "timeout_ms": 10000,
    "read_only_tools": []
  }]
}
```

The operator trusts the **entire executable**, including startup code. This is **NON-SANDBOXED current-user execution**, not the native file boundary or exec containment: the server can read files, use network/credentials and spawn descendants before any tool approval. The environment is exactly the declared `NAME=value` list, empty by default; there is no host/model/auth inheritance or shell expansion. Minimal environment is not secret isolation. Do not put secrets in argv. The configuration fingerprint privately binds argv, environment values, cwd identity, exact trusted read-only classification and selected executable hash. Raw environment values are not sent to the model or status DTOs. Dependencies, scripts passed in argv, interpreter/shared libraries and same-user concurrent replacements are **not** frozen or sandboxed. An observed selected-executable or directory-identity change revokes calls.

Creating an MCP session is a **second explicit opt-in**: check “启动已配置 MCP（非沙箱）” in Web, use terminal `--mode default --mcp`, or call `client.createSession('default', requestID, modelID, true)` / POST `{..., "mcp":true}`. The MCP checkbox is off by default and unavailable in chat-only. Creation launches the configured processes and discovers/validates tools; ordinary sessions and `/mcp`, status, history and approval review never launch or reconnect. Existing provider verification/privacy gates still apply. There is no auto-install, HTTP endpoint, remote discovery or arbitrary command/env from a model.

Session policy pins server/config identity, negotiated protocol, normalized input+output schema digest and process generation. Tool names are collision-resistant per-server aliases, not native `exec`/`read` names. Peer annotations, read-only hints, descriptions and self-reported identity do not grant permission. Only exact names in **operator-authored** `read_only_tools` can run in plan mode or without per-call approval; the default is unknown/writable, requiring fresh exact single-use approval even in `acceptEdits`. JSON and supported schema validation happen before approval, then discovery/schema validation repeats before dispatch. Config/schema/generation drift refuses the old call rather than changing its meaning.

Prepared reviews, approvals, artifacts and the authoritative side-effect journal remain in `runtime-rebuild-v1/agent.db`, outside transport. Restart never reconnects/replays. Historical reviews remain readable but show **approval revoked**; approval is refused with `mcp_approval_revoked`. Deny or cancel a still-unexecuted pending run, then deliberately create a new MCP session/call. `not_sent` is a known refusal; tool `isError` is a known tool error, not success. Possible sends, cancellation, EOF, unsupported Tasks/`input_required`, malformed post-send results and conservative RPC failures enter **needs_reconciliation**, retain `tool.uncertain` and block subsequent turns/replay. A cancellation notification is intent, not proof of stopped effects. No automatic or interactive reconciliation/continuation UI is implemented.

Web/Ink show configured versus discovered state separately from actual tool call cards and result/empty/error/unknown observations. Reviews show server, tool, exact arguments, schema/config fingerprint, protocol/generation and current-user risk. `/mcp` is read-only inspection, not an enable command. MCP data is untrusted plain text, not evidence links or executable HTML. Complete raw outputs are session-scoped artifacts when the serialized observation exceeds 4 KiB; frames cap at 1 MiB. Limits: 8 configured servers, 16 retained process slots per service, 64 tools/server, 64 KiB arguments, 1–30,000 ms per transport request. Discovery is paginated/bounded; this is not a total wall-clock startup guarantee. Direct children are closed and synchronously reaped; descendants are not tracked/attested stopped, and an uninterruptible OS wait can delay shutdown. A service restart releases process slots but revokes all old MCP process generations.

**默认关闭，仅支持 Linux 工作区会话。** 旧配置的 MCP enabled 不会启用重建服务。通过独立 `--mcp-config` 指定权限 0600、最大 64 KiB 的 JSON，再在新建会话时勾选“启动已配置 MCP（非沙箱）”、终端添加 `--mode default --mcp`，或 API 明确传 `mcp:true`。普通会话、`/mcp`、状态、历史和审阅不会启动或重连服务器。chat-only 禁止启用；现有模型工具证据与隐私门禁不变。

配置意味着信任**整个服务端可执行程序及启动代码**：以当前用户权限访问文件、网络、凭据及创建后代，绝不是原生文件工具的边界或 OS 沙箱，启动本身也可能产生副作用。只传声明的环境变量（默认空），不继承主机/模型/认证环境、不做 shell 展开；最小环境不等于隔离秘密，不要将密钥放 argv。私有指纹绑定配置参数、环境值、cwd 身份、可信只读分类与选定可执行文件哈希；原始环境值不进入模型或状态 DTO。argv 引用的脚本、解释器、动态库及同用户并发修改不冻结。发现可执行文件或目录身份变化会撤销后续调用。

会话固定 server/config、协议、规范化输入输出 schema 摘要与进程代次；不同服务器的同名工具不共享权限。服务端 annotations/只读提示/描述不授予权限；只有主机明确列入 `read_only_tools` 的具体名称可在 plan 执行或免逐次审批。其余默认未知/可写，即使 `acceptEdits` 也逐次精确审批。审批前校验参数和 schema，发送前重新发现校验；变化只会拒绝旧操作，不沿用旧审批执行新语义。

持久提案、审批、观察 artifact 和副作用 journal 与传输分离，仍保存在独立 runtime 数据库。重启不重连/重放，旧审批显示失效；未执行的等待 run 可拒绝或取消，再新建会话。`not_sent` 为已知拒绝；`isError` 为工具错误而非成功；可能发送、取消、EOF、Tasks/input_required、不完整响应等进入 `needs_reconciliation`，保留未知观察并阻止新回合/重放。取消通知不证明停止。暂无自动核对或交互续传界面。

Web/Ink 区分已配置、已发现和真实调用/结果/空结果/错误/未知，展示精确 server/tool/args/schema/config 与非沙箱风险，不制造证据链接。序列化观察超过 4 KiB 时保存会话隔离 artifact，帧最大 1 MiB；最多 8 个配置服务器、服务进程保留 16 个进程槽位、每服务器 64 个工具、参数 64 KiB、每请求 1–30,000 ms。分页发现有界，但不是总启动时间保证。只关闭并等待直接子进程，不证明后代停止；不可中断 OS 等待可能延迟关闭。重启释放槽位同时撤销旧 MCP 代次。

| Capability / 能力 | Rebuilt support / 重建支持 | Evidence / 证据 |
| --- | --- | --- |
| Linux/amd64 configured stdio 2026-07-28 + 2024-11-05 | Explicit host + new-session opt-in; exact durable approval / 主机与新会话双重授权、持久精确审批 | Owned real Go helpers, SDK/API, Web Chromium, Ink PTY / 仅自有本地测试程序 |
| Windows/macOS rebuilt MCP | Unavailable: workspace adapter gate / 工作区适配器门禁，明确不可用 | Compile checks only, not native acceptance / 仅交叉编译 |
| HTTP/SSE MCP, Tasks, input_required continuation | Unsupported, never advertised or continued / 不支持、不声明、不续传 | Explicit refusal/unknown tests; UI SSE unrelated / UI SSE 无关 |
| Real external MCP/provider interoperability | Not tested / 未验证 | No real endpoints, credentials or user files used / 未使用真实端点、凭据或用户文件 |

Local acceptance: `go test ./...`, focused Go race tests, `pnpm test`, `pnpm typecheck`, `pnpm build`; run `make web-assets` **before** real browser tests (`node --import ./apps/terminal/node_modules/tsx/dist/loader.mjs --test apps/web/test/mcp.test.ts`), with `CHROMIUM_PATH` set to the installed headless shell. `python3 apps/terminal/test/mcp_pty.py` exercises a real PTY. These fixtures launch only their owned executable/temp data; they are not installed user MCP servers.

## English

AI agent for local user workflows.

This repository is a Go implementation of a local-first parallel personal agent. The current implementation includes the P0 foundation, P1 model/privacy foundation, P2 RAG/memory foundation, P3 orchestration/Sub-Agent foundation, P4 browser runtime foundation, P5 permissions/verification/cost foundation, P6 API/E2E/hardening foundation, P7 Runtime MVP closure, P8 External Coding Agent library foundation, P9 Capability/Skill/Workflow core closure, P10 proactive trigger/event core closure, P11 Runtime Integration closure, the P12 Operator Experience core closure, the P13 Reliability/Security/Observability core slice, and the P14 v1.0 GA release packaging slice: `pachat` CLI packaging, configuration loading, SQLite migrations, core agent/browser contracts, Runtime-backed chat, long-task state tracking with cancellable API background execution, model registry/policy contracts, OpenAI-compatible provider boundaries, a fail-closed Privacy Gateway for public remote model calls, local BM25 retrieval, optional vector/RRF/reranker hybrid retrieval with BM25 fallback, context compression, working/episodic/semantic memory stores, cancellable DAG orchestration, dependency-aware parallel scheduling, Sub-Agent retry handling, early stop, ordered node lifecycle evidence events, a governed chromedp browser runtime boundary, global permission policy evaluation, persistent confirmation inbox records, confirmation approve/deny APIs, claim/evidence verification, conflict detection, registry-backed cost accounting, local REST task APIs, full mock-based local E2E coverage, secret-leak regression fixtures, a unified workflow-backed Runtime used by CLI/API task execution, bounded model-backed Leader planning interfaces, governed External Coding Agent boundaries for Codex/Claude-style CLI backends, deterministic capability and skill registries, capability runtime metrics, skill eval activation gates, draft skill candidate generation, persistent workflow checkpoints with node dependencies, read-only MCP metadata adaptation plus governed MCP execution placeholders, persistent proactive triggers, untrusted event envelopes, event ingestion APIs, proactive daemon tick/recovery helpers, local notification inboxes, bounded goal planning, filesystem watcher scans, strict versioned config validation, local doctor diagnostics, knowledge/project/operator commands, packaged local HTTP serving, dashboard/SSE/model-discovery endpoints, backup/restore dry-run, retention cleanup, SQLite maintenance commands, structured log redaction, local runtime metrics, JSON health/readiness responses, release version metadata, cross-platform release builds, checksums, install/upgrade scripts, service templates, sample production configs, release runbooks, and context-cancellation conformance across model HTTP, browser, MCP, tool, and coding process boundaries.

It includes Go contracts and safety skeletons for the Browser Tool, Browser Session Manager, Permission Layer integration, Privacy Gateway filtering, Evidence Bus records, model selection, provider enforcement, hybrid local retrieval/memory indexing, orchestration, semantic browser location, accessibility reads, screenshot capture, coordinate fallback, confirmation-gated browser execution, global permission decisions, verification gates, budget enforcement, local HTTP handler wiring, workflow-backed Runtime execution, coding agent adapter selection, safe process execution, environment sanitization, Git worktree isolation, CLI adapter request mapping, coding evidence collection, repository privacy checks, trigger scheduling helpers, trigger state persistence, event deduplication, local operator tooling, persistent approvals, notifications, dashboard event stubs, watcher scans, and bounded goal planning. It still does not include cookie reading, password reading, production-grade OS/kernel/browser isolation, external tracing export, external vulnerability scanning, or third-party notification delivery.

MCP session startup itself is an effect boundary. Before launching, the API durably reserves the request fingerprint and a stable session ID. Identical retries return the original acknowledgement or `mcp_admission_unresolved`; changed requests using that ID conflict. Failed, interrupted or restarted admissions never automatically relaunch. Authenticated `GET /api/v1/admissions/{request_id}` inspects the saved identity/state without starting a server. Inspect a bound session, or explicitly create a new request after reviewing possible startup effects; do not delete the claim to retry. Startup/discovery do not hold global API/host locks: unrelated cancel/status/review remain available, and shutdown joins pending startup cleanup.

MCP 会话启动本身也是副作用边界：启动前先持久保存请求指纹和固定会话 ID。同请求重试只返回原确认或 `mcp_admission_unresolved`；同 ID 改内容会冲突。失败、中断或重启后的 admission 不会自动重新启动。经认证的 `GET /api/v1/admissions/{request_id}` 只读查看固定身份及状态，不启动服务端。可查看已绑定会话，或核对可能发生的启动副作用后明确使用新请求；不要删除 claim 来重试。启动与发现不占 API/host 全局锁，其他会话的取消、状态和审批审阅仍可进行；关闭会等待未完成启动的清理。

### Contents

- `cmd/pachat`: CLI entrypoint.
- `configs/config.example.yaml`: Local-first example configuration.
- `internal/config`: YAML loader and validator.
- `internal/storage`: SQLite storage and migrations.
- `internal/memory`: Working, episodic, and semantic memory stores.
- `internal/rag`: Document/chunk storage, chunking, BM25 retrieval, RRF fusion, Qdrant wrapper, reranker boundary, and context compression.
- `internal/agent`: Leader/Sub-Agent contracts, structured results, claims, evidence IDs, usage, and error categories.
- `internal/orchestrator`: DAG validation, ready-node calculation, parallel scheduling, retry handling, cancellation propagation, early stop, and lifecycle evidence events.
- `internal/browser`: Go contracts, chromedp runtime adapter, session/profile policy checks, semantic locator, accessibility/screenshot helpers, confirmation gate, coordinate fallback, redaction, evidence builders, and tests.
- `internal/permission`: Global permission policies, evaluator, confirmation workflow, and audit records.
- `internal/verification`: Claim extraction, claim/evidence coverage, conflict detection, and confidence policy.
- `internal/cost`: Registry-backed pricing lookup, usage estimation, and budget limit enforcement.
- `internal/model`: Model trust levels, capabilities, registry, policy selection, provider interfaces, and OpenAI-compatible HTTP boundary.
- `internal/privacy`: HMAC-SHA256 pseudonymization, secret redaction, credential dump blocking, and audit metadata.
- `internal/api`: Local REST handler for task create/list/status/cancel/events, trigger/event ingestion, dashboard/SSE/model discovery, notification inbox, and confirmation inspect/approve/deny.
- `internal/runtime`: Local-first Personal Agent Runtime that routes task execution through persisted workflows, bounded planning, memory, hybrid RAG, evidence, verification, synthesis, guarded external capability dispatch, and public escalation fail-closed boundaries.
- `internal/codingagent`: External Coding Agent contracts, registry, safe process executor, worktree isolation, Codex/Claude CLI adapters, environment sanitizer, and evidence validation.
- `internal/capability`: Capability descriptions, health states, deterministic policy resolver, runtime metrics, and Runtime capability registration.
- `internal/skill`: Versioned Skill manifests, dependency/permission validation, registry, skill-first matching, eval-gated activation, and draft candidate generation.
- `internal/workflow`: Persistent workflow/node state, persisted dependencies, checkpoints, recovery, and pause/resume/cancel transitions.
- `internal/project`: Project context and allowlist policy persisted through SQLite.
- `internal/mcp`: Read-only MCP server/tool metadata and capability adaptation boundary.
- `internal/trigger`: Proactive trigger model, validation, persistence, schedule calculation, daemon tick/recovery, condition evaluation, watcher scan, deduplication, debounce, cooldown, jitter, backoff helpers, history, and dead-letter storage.
- `internal/event`: Untrusted event envelope, in-memory bus, SQLite event store, and event deduplication.
- `internal/notification`: SQLite-backed local notification inbox.
- `internal/goal`: Bounded local goal planner.
- `internal/observability`: Structured log redaction, in-process metric snapshots, and health/readiness reports.
- `internal/e2e`: Mock-based local integration tests that combine API, SQLite, RAG, memory, model mocks, browser denial, verification, cost, and secret-leak regression coverage.

### P0 Usage

The completed P0 tool is `pachat`. Use it to verify local configuration, SQLite migrations, task persistence, interactive memory, and long-task state tracking.

Build:

```sh
make build
```

`make build` also copies the safe local-first example config to `bin/config.example.yaml`, so running from `bin/` can use `./pachat chat --config config.example.yaml`. Public remote providers are disabled in the copied example; enable them only after setting the required environment variables, including `PERSONAL_AGENT_PRIVACY_HMAC_SECRET`.

Run a no-op task:

```sh
bin/pachat run --config configs/config.example.yaml --task "smoke test"
```

Run with your own task text:

```sh
bin/pachat run --config configs/config.example.yaml --task "summarize my local notes"
bin/pachat run --config configs/config.example.yaml --project <project_id> --task "summarize project notes"
bin/pachat run --config configs/config.example.yaml --task "summarize my local notes" --stream
```

Expected output shape:

```text
task_id=task_<generated_id>
status=completed
confidence=0.90
remote_tokens=0

answer:
Local-first answer for: smoke test
```

What happens:

- The CLI loads and validates `configs/config.example.yaml`.
- SQLite opens at `./data/personal-agent.db`.
- Embedded migrations under `internal/storage/migrations` are applied.
- One row is inserted into the `tasks` table as `running`.
- The Runtime queries local memory and local RAG, stores evidence, verifies the answer, and marks the task `completed`.

Interactive local memory with slash-command completion:

```sh
bin/pachat chat --config configs/config.example.yaml
```

Inside chat, type a message to store it in local episodic memory. Type `/` and press `Tab` to complete built-in commands. Use `/help` to list commands, `/memory` to view recent memory, and `/exit` or `/quit` to leave.

Long-task state:

```sh
bin/pachat run --config configs/config.example.yaml --task "long local task" --long
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task usage --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```

Operator commands:

```sh
bin/pachat init --config configs/config.yaml --data-dir ./data
bin/pachat config validate --config configs/config.example.yaml
bin/pachat doctor --config configs/config.example.yaml
bin/pachat knowledge add --config configs/config.example.yaml ./notes.txt
bin/pachat knowledge list --config configs/config.example.yaml --json
bin/pachat project create --config configs/config.example.yaml --name "Local Project"
bin/pachat skill import --config configs/config.example.yaml --path ./skill.yaml
bin/pachat skill list --config configs/config.example.yaml
bin/pachat skill run --config configs/config.example.yaml --id <skill_id> --input "local task"
bin/pachat mcp list --config configs/config.example.yaml
bin/pachat mcp health --config configs/config.example.yaml
bin/pachat mcp tools --config configs/config.example.yaml
bin/pachat workflow events --config configs/config.example.yaml --id <workflow_id>
bin/pachat trigger create --config configs/config.example.yaml --type manual --id local-trigger
bin/pachat trigger run-now --config configs/config.example.yaml --id local-trigger
bin/pachat approval list --config configs/config.example.yaml
bin/pachat notification list --config configs/config.example.yaml
bin/pachat model discover --config configs/config.example.yaml
bin/pachat watcher scan --config configs/config.example.yaml --path ./docs
bin/pachat serve --config configs/config.example.yaml
curl http://127.0.0.1:8787/healthz
curl http://127.0.0.1:8787/readyz
curl http://127.0.0.1:8787/metrics
curl http://127.0.0.1:8787/dashboard
curl http://127.0.0.1:8787/models/discover
curl "http://127.0.0.1:8787/tasks?limit=20&offset=0&status=running"
curl "http://127.0.0.1:8787/workflows?limit=20&offset=0&project_id=default"
curl "http://127.0.0.1:8787/events?limit=20&offset=0&type=push"
curl "http://127.0.0.1:8787/projects?limit=20&offset=0&q=local"
curl "http://127.0.0.1:8787/skills?limit=20&offset=0&status=active"
bin/pachat backup create --config configs/config.example.yaml
bin/pachat export --config configs/config.example.yaml --output portable.json
bin/pachat import --config configs/config.example.yaml --input portable.json
bin/pachat storage integrity --config configs/config.example.yaml
```

Current CLI boundary: default task execution is workflow-backed and local-first. Browser, Coding, MCP, and public model paths are governed and fail closed unless explicit executors/providers are configured.

Cancellation boundary: API/CLI task cancellation propagates through Runtime workflows, the scheduler, Sub-Agent execution, configured model HTTP calls, vector HTTP calls, browser executors, MCP child processes, allowlisted local tool child processes, and Codex/Claude-style coding process runners.

Skill workflow boundary: active high-confidence read-only Skills compile directly into persistent workflows with `skill_id` and `skill_version`, then run through the existing scheduler without calling the Leader planner. Planner-backed and default Runtime workflows remain the fallback when no eligible Skill matches.

Skill operator CLI: `pachat skill validate/import/list/show/versions/enable/disable/run` manages Skill manifests persisted in SQLite and executes enabled Skills through the existing Runtime workflow path.

Background workflow worker: `pachat serve` starts a Runtime worker that resumes unfinished runnable workflows on startup and polls for more work. `paused` and `waiting_approval` workflows are left for operator or approval actions.

Proactive daemon: `pachat serve` starts the proactive trigger loop when `proactive.enabled` is true. It recovers trigger/watch state, processes due schedules and condition-watch transitions at `proactive.poll_interval`, dispatches trigger work as Runtime workflows, and shuts down through the server context.

Side-effect idempotency: non-read-only workflow capabilities claim a persisted idempotency key before execution. Replays from duplicate events, retries, or crash recovery do not re-run already claimed browser writes, MCP/tool mutations, coding pushes, PR creation, deploy, or message-send style effects.

Project policy boundary: Runtime workflow execution applies project policy to Skill matching, Leader model selection, memory/RAG capabilities, public escalation, browser/MCP/tool dispatch, and Codex/Claude backend allowlists. CLI task runs can select a project with `--project`; API task creation accepts `project_id`.

Coding Cross Review: set `coding_agents.cross_review.enabled: true` and configure `large_diff_bytes` to require a second enabled coding backend for security-sensitive, critical-project, verification-failed, or large-diff coding tasks. Ordinary coding tasks still use one backend.

Real coding E2E: run `PACHAT_CODING_AGENT_E2E=1 PACHAT_CODEX_CLI_PATH=/path/to/codex go test ./internal/codingagent` or set `PACHAT_CLAUDE_CODE_CLI_PATH` for Claude to exercise real CLI backends against a temporary repository. These tests skip unless explicitly enabled.

Condition watcher state: proactive condition-watch triggers persist previous/current state, last transition, last check, last notification, and cooldown state so daemon restarts keep watcher context.

Notification policy: local notifications deduplicate by `dedup_key`, carry severity and delivery state, and support quiet-hours suppression through notification policy.

Goal persistence: bounded goals and milestones persist status, dependencies, workflow linkage, completion criteria, budget policy, pause/resume state, and re-evaluation timestamps.

### P1 Model And Privacy Foundation

P1 adds library-level model and privacy controls:

- Model trust levels: `local_private`, `local_sandboxed`, `trusted_remote`, and `public_remote`.
- Model capabilities: `chat`, `tool_calling`, `json_schema`, `vision`, `embedding`, `rerank`, `long_context`, and `browser_reasoning`.
- Registry validation for duplicate model IDs, provider references, trust levels, and capabilities.
- Role-specific model policy selection so Leader and Sub-Agent settings remain independent.
- Mockable chat, embedding, and rerank provider interfaces.
- OpenAI-compatible chat and embedding HTTP boundary with injectable transport.
- Privacy Gateway with stable HMAC-SHA256 pseudonymization, redaction, blocking, and audit metadata.
- Public remote provider calls fail closed when the Privacy Gateway is missing or blocks the payload.

### P2 RAG And Memory Foundation

P2 adds library-level retrieval and memory controls:

- Deterministic document chunking with content hashes.
- SQLite-backed document and chunk persistence using the existing local schema.
- Local BM25 retrieval over stored chunks.
- Reciprocal Rank Fusion for combining ranked retrieval lists with deterministic tie-breaking.
- Qdrant HTTP wrapper with caller-supplied base URL, collection, API key, and HTTP client.
- Reranker interface boundary for later model-backed reranking.
- Context compression that preserves evidence IDs, source URIs, titles, metadata, scores, and privacy class.
- Working memory with expiration cleanup.
- Episodic memory retained for chronological local events.
- Semantic memory with optional indexing into RAG for durable facts and preferences.

### P3 Orchestration And Parallel Sub-Agents

P3 adds library-level orchestration controls:

- Task DAG validation for duplicate node IDs, missing dependencies, self-dependencies, and cycles.
- Dependency-aware ready-node calculation.
- Parallel scheduling through role-specific Sub-Agent contracts.
- Retry handling for retryable failures using each node's max-attempt policy.
- Caller-provided early stop policy.
- Context cancellation propagation to running Sub-Agents.
- Structured node results with claims, evidence IDs, usage, and typed error categories.
- Ordered Evidence Bus lifecycle events for ready, started, retrying, completed, failed, cancelled, and early stop records.

### P4 Browser Runtime Foundation

P4 adds a governed local browser runtime boundary:

- Runtime action adapter for navigation, reads, screenshots, accessibility reads, semantic targeting, input, and coordinate fallback.
- chromedp-backed runtime with caller-supplied allocator options and screenshot directory.
- Local profile store that resolves profile directories from configured environment variables without exposing profile paths in metadata.
- Confirmation gate for write and high-risk browser actions.
- Redacted browser evidence publishing through an in-memory browser Evidence Bus boundary.
- Local browser E2E fixture under `internal/browser/testdata/browser`; run it with `PACHAT_BROWSER_E2E=1 go test ./internal/browser`.

### P5 Permissions, Verification, And Cost Foundation

P5 adds library-level safety and accounting controls:

- Global permission action categories for file system, network, browser read/write, high-risk browser actions, memory writes, public model calls, credential access, and external side effects.
- Configurable permission policy defaults and per-action rules.
- Confirmation requests that include action, target, risk, evidence IDs, and exact proposed effect.
- Auditable permission decision records for later evidence persistence.
- Deterministic claim extraction with claim-to-evidence coverage checks.
- Conflict detection between claims and evidence.
- Confidence policy gates for final synthesis.
- Registry/config-backed pricing lookup and model usage cost estimation.
- Budget soft and hard limit enforcement.

### P6 API, E2E, And Hardening Foundation

P6 adds local API and integration validation:

- Standard-library REST handler for task create, list, status, cancel, and event listing.
- Confirmation inspect, approve, and deny endpoints backed by the confirmation store boundary.
- JSON responses with stable status codes and secret redaction on API-facing text.
- Full local E2E test using provider mocks, SQLite, RAG, episodic/semantic memory, browser high-risk denial, verification, cost accounting, and final synthesis.
- Secret-leak regression fixtures for API responses, Privacy Gateway output, and browser evidence.
- PostgreSQL integration test placeholder that skips cleanly unless a test DSN is configured.

### P7 Runtime Integration / Personal Agent MVP

P7 Runtime MVP is documented in `docs/projdocs/P0_P7_EXECUTION_PLAN.md`, `docs/p7_runtime_integration.md`, `docs/p7_runtime_mvp_implementation.md`, and `docs/projdocs/task/P7.md`. The first vertical slice connects storage, memory, local RAG, evidence, verification, synthesis, CLI execution, and API task creation into real local-first task execution.

P7 MVP acceptance covers the removal of the no-op task path, local-first evidence, zero public model usage when local evidence is enough, workflow-backed CLI/API task execution, Runtime-backed chat turns, verification reports, persisted final answers, API background cancellation, bounded Leader planning interfaces, guarded BrowserAgent execution boundaries, public model escalation fail-closed interfaces, provider usage propagation when real providers return usage, and secret-leak regression coverage.

### P8 External Coding Agents

P8 is documented in `docs/projdocs/P0_P8_EXECUTION_PLAN.md`, `docs/p8_external_coding_agents.md`, and `docs/projdocs/task/P8.md`. The first library slice adds a governed External Coding Agent boundary for Codex CLI and Claude Code CLI style backends.

P8 includes configurable backend enablement, independent execution location and inference trust, capability-based backend selection with fallback, safe subprocess execution with timeout/cancellation, minimal environment sanitization, Git worktree isolation, Codex/Claude adapter request mapping, repository privacy denial for confidential repositories using public remote inference, and evidence validation requiring actual diff/files/tests instead of self-reported completion.

Current P8 boundary: coding capability nodes are recognized by Runtime workflow dispatch and return governed blocked/unavailable results unless a backend is explicitly enabled. Example configuration defaults keep Codex/Claude disabled and direct writes disabled.

### P9 Core Release Slice

P9 is documented in `docs/p9_core_release_slice.md`, `docs/projdocs/P0_P9_EXECUTION_PLAN.md`, and `docs/projdocs/task/P9.md`. This slice adds deterministic capability discovery and policy resolution, versioned Skill validation and matching, Project-scoped policy data, persistent Workflow checkpoints and recovery, and a read-only MCP metadata adapter.

Operator commands:

```sh
bin/pachat capability list --config configs/config.example.yaml
bin/pachat capability health --config configs/config.example.yaml
bin/pachat workflow list --config configs/config.example.yaml
bin/pachat workflow show --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow pause --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow resume --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow cancel --config configs/config.example.yaml --id <workflow_id>
```

The current P9 boundary persists workflow state, node dependencies, and checkpoints; exposes safe lifecycle transitions; provides capability metrics-aware routing; adds eval-gated Skill activation and draft candidate generation; and exposes governed MCP execution placeholders. External MCP transport remains fail-closed unless an executor is explicitly provided.

### P10 Proactive Trigger/Event Core Slice

P10 is documented in `docs/p10_core_release_slice.md`, `docs/projdocs/P0_P10_EXECUTION_PLAN.md`, and `docs/projdocs/task/P10.md`. This first proactive slice adds persistent trigger definitions and state, one-shot/interval/simple-cron schedule calculation, deterministic execution keys, debounce, cooldown, jitter, exponential backoff helpers, untrusted event envelopes, event deduplication, and an in-memory event bus with SQLite persistence.

Current P10 boundary: the package provides trigger CLI/API commands, push event ingestion, trigger history, daemon tick/recovery helpers, condition evaluation, dead-letter persistence, local notification inboxes, bounded goal planning, and filesystem watcher scans. Continuous always-on daemon operation and third-party notification delivery remain deployment choices.

### P11 Runtime Integration Closure Slice

P11 is documented in `docs/p11_runtime_integration_closure.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P11.md`. The first closure slice adds a Runtime-owned workflow engine that loads persisted workflow runs, recovers non-terminal nodes, checks project capability policy, checks registered capability availability, runs local read-only nodes through the existing scheduler, persists per-node checkpoints, and marks workflows completed, failed, or cancelled.

Current P11 boundary: `Runtime.Run()` now creates and executes persisted workflows through the scheduler. Persisted workflows can execute `memory.search`, `rag.search`, `verification.verify`, and `synthesis.local`; Browser/Coding/MCP nodes are routed to guarded SubAgents and fail closed when executors are unavailable.

### P12 Operator Experience Core Slice

P12 is documented in `docs/p12_operator_experience.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P12.md`. This core slice adds versioned strict config validation, `pachat init`, `pachat config validate`, `pachat doctor`, local knowledge indexing/list/status/reindex/remove commands, project create/list/show/update/archive/use commands, task/workflow JSON output, workflow checkpoint event inspection, a packaged local `pachat serve` command with health/readiness endpoints, backup create/restore dry-run, event retention cleanup, and SQLite integrity/vacuum/checkpoint maintenance commands.

Current P12 boundary: the operator CLI and REST API include persistent approval inbox storage, local notification inboxes, dashboard and SSE-ready endpoints, model discovery from configuration, filesystem watcher scans, and packaged local serving. The dashboard endpoint is intentionally minimal rather than a full visual web application.

### P13 Reliability / Security / Observability Release Gate

P13 is documented in `docs/p13_reliability_security_observability.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P13.md`. This core slice adds centralized redaction for structured logs and API-facing text, in-process local runtime metrics, JSON `/healthz` and `/readyz` reports for `pachat serve`, a JSON `/metrics` endpoint, request/error/activity counters, and secret-leak regression coverage for observability output.

The remaining P13 release-gate foundations add local tracing spans, a global resource governor, disk pressure checks in readiness, graceful shutdown coordination primitives, API auth/CORS/body-limit/rate-limit/security-header middleware, command/path/network policy helpers, prompt-injection and secret-leak fixtures, coding-agent sandbox checks, browser security regression helpers, a durable `security_audit_log` migration/store, golden eval cases, and measurable quality-gate threshold evaluation.

Current P13 boundary: P13 now provides local policy boundaries and regression gates. It does not claim production-grade OS-level sandboxing, distributed tracing export, external vulnerability services, or kernel/browser isolation.

### P14 v1.0 GA Release Packaging

P14 is documented in `docs/p14_ga_release_requirements.md`, `docs/projdocs/P0_P14_EXECUTION_PLAN.md`, and `docs/projdocs/task/P14.md`. This release slice adds a version model (`pachat version`), reproducible release metadata, a required build matrix for `darwin/arm64`, `linux/amd64`, and `linux/arm64`, SHA256 checksum generation, bootstrap install and upgrade scripts, systemd and launchd service examples, safe production config examples, migration compatibility coverage, and release checklist automation through `pachat release check` and `make release-check`.

The default `configs/config.example.yaml` uses the locally available Ollama model `qwen3.8:27b-mlx`. Change `models.registry[].model` in the mountable config file when using a different local model.

`configs/config.example.yaml` is now self-documenting: each example key has Chinese and English comments explaining the setting, expected value shape where useful, and privacy or safety impact for sensitive provider, browser, network, storage, and external-tool options. `models.registry` includes complete representative entries for local chat/planning, local embedding, local reranking, and a disabled public remote chat model. The comments and extra disabled/optional registry examples do not change the default local-first behavior.

OpenAI-compatible chat messages are serialized with the required lowercase `role` and `content` fields, so configured providers can accept planner, reasoning, synthesis, and interactive chat requests.

Interactive `pachat chat` now dispatches every ordinary turn through the shared task workflow. Local memory/vector/BM25 retrieval runs first; sufficient local evidence invokes only the configured synthesis Sub-Agent, while insufficient evidence invokes the Leader planner and delegated Sub-Agents. The Leader provider remains selected by `agent.leader.model_id`; planner output is bounded by `agent.planner.max_nodes`, validated against the Runtime capability registry, and rejected before workflow creation when it references unknown capabilities or invalid DAG dependencies. Provider connectivity or response errors are returned explicitly.

Planner configuration:

```yaml
agent:
  planner:
    enabled: true
    max_nodes: 8
```

Workflow reasoning and synthesis now reuse the configured private local model when `Runtime.ChatProvider` is available. `reasoning.local` emits bounded claims, evidence IDs, confidence, and provider usage without storing chain-of-thought. `synthesis.local` writes the final answer from verified evidence, and `Runtime.Run()` uses the completed synthesis checkpoint as the persisted task answer.

Role-specific Sub-Agent model IDs are honored for workflow-backed reasoning and synthesis. Set `agent.subagents.reasoning.model_id` or `agent.subagents.synthesis.model_id` to a model in `models.registry` to use a different configured provider for those nodes. `pachat chat` and `pachat run` both use the shared `Runtime.DispatchTask` workflow path; chat ordinary turns therefore invoke reasoning/synthesis when local retrieval is insufficient, while local evidence hits use only lightweight synthesis.

Hybrid RAG production wiring is controlled by the mountable `rag` config. `rag.vector` composes an embedding model with Qdrant vector search; `rag.reranker` composes a configured rerank model when available. If vector search or reranking is disabled or unavailable, retrieval keeps the local BM25 fallback.

Runtime capability executors are now registered through a local executor registry. When browser support is enabled, `browser.read`, `browser.navigate`, and `browser.write` route through the governed browser tool; read observations are persisted as Runtime evidence, while navigation and write actions still pass through session/domain policy and confirmation gates.

Enabled coding backends are registered as `coding.<backend>` executors, including Codex and Claude style adapters. Coding workflow nodes must provide structured JSON input with `repository_path`, `prompt`, write policy, privacy class, and optional test commands; execution delegates to the governed coding agent Runner and stores diff/test evidence.

Configured MCP servers are registered as `mcp.<server>.<tool>` executors and called through stdio JSON-RPC. MCP output is persisted as `UNTRUSTED OBSERVATION` evidence. Configured local tools are registered from `tools.allowlist`; only fixed allowlisted program/args execute, and workflow input is not interpolated into shell commands.

`OP-02` adds MCP operator commands: list configured servers, check command availability, enumerate MCP tool capabilities, and smoke-test a configured tool through Runtime workflow execution with `pachat mcp test`.

`OP-03` replaces the dashboard stub with a minimal operator console at `/dashboard`, including Chat/Run, Tasks, Workflows, Approvals, Notifications, Projects, Skills, Capabilities Health, and Triggers views.

`OP-04` productizes `/dashboard/events` as typed SSE for Task, Workflow, Approval, Notification, Trigger, and ready events with `Last-Event-ID` reconnect filtering and heartbeat keepalive.

`OP-05` adds `/evidence` and `/evidence/{id}` plus a dashboard Evidence view for claim, redacted preview, source type, trust, privacy, timestamp, and verification status.

`OP-06` completes full backup restore execution with temporary database validation, migration compatibility checks, SQLite integrity checks, existing database pre-restore copies, and rollback on failed final validation.

`OP-07` adds portable data export/import. `pachat export` writes Projects, Skills and versions, selected episodic/semantic memory, and knowledge document metadata to a versioned JSON artifact. `pachat import` validates that artifact and upserts those objects into the configured local database. It does not export credentials, browser cookies, browser passwords, browser profile data, coding CLI credential stores, or raw document chunk text.

`OP-08` adds foreground streaming for `pachat run --stream`. The stream reports planning, workflow progress, node state, tool-like node progress, approval wait state, and final answer deltas with secret-like text redacted. It does not expose chain-of-thought or raw reasoning checkpoint fields.

`OP-09` adds paginated and filtered REST list APIs for tasks, workflows, events, notifications, projects, triggers, and skills. List endpoints accept bounded `limit` and `offset` plus resource-specific filters such as `status`, `project_id`, `type`, `enabled`, `privacy_class`, `skill_id`, `source`, `severity`, and `q`.

The Functional RC E2E gate is executable with `go test ./internal/e2e -run FunctionalRC`. It covers local RAG/memory with zero public calls, model-backed DAG planning, Browser read/write evidence, Codex-style diff/test evidence, Claude-style review evidence, MCP evidence, Privacy Gateway public escalation, restart/idempotency behavior, and false-to-true watcher notification deduplication using mock-backed external executors and real Runtime persistence/scheduling.

`pachat release check --quick` now includes mandatory Functional GA gates for the Functional RC E2E suite, runtime Leader planning, external executor registry wiring, workflow restart recovery, privacy escalation, early-stop cancellation, side-effect idempotency, and notification edge semantics. Real external CLI/browser binary validation remains opt-in for release candidates.

`pachat release soak --config <path>` runs an executable soak gate. The default offline mode exercises the real local Runtime and SQLite workflow path without requiring external model/browser/CLI binaries, samples goroutine, heap, SQLite, workflow, notification, and external-artifact metrics, and can write a JSON report with `--output <path>`. Use `--duration 24h --interval 1m --offline=false` for release-candidate environments with configured external backends.

`pachat release recovery-drill --config <path>` runs the data-loss recovery drill against disposable fixture state. It seeds Projects, Skills, Memory, Workflow, Trigger, Notification, and Approval records, creates a backup through the normal backup command path, deletes the fixture SQLite files, restores into a fresh database, verifies SQLite integrity and every object class, and writes a JSON report when `--output <path>` is set.

`pachat release perf-baseline --config <path>` records a regression baseline from the real local Runtime. The default offline mode uses an isolated SQLite database, measures startup, local memory query latency, hybrid RAG fallback latency, workflow dispatch latency, heap allocation, and concurrent workflow completion behavior, and writes a JSON report when `--output <path>` is set.

Public escalation is wired from enabled `public_remote` chat models in the model registry. If a public provider requires Privacy Gateway, `privacy.hmac_secret_env` must resolve or Runtime build fails closed. Secret-bearing payloads are blocked before provider calls.

Task usage can be queried with `pachat task usage --config <path> --id <task_id>`. Usage is aggregated from workflow checkpoints at node, workflow, and task levels. Model providers with reliable usage use provider token counts; external CLI/tool usage remains marked as unknown when token counts are unavailable.

Verification-driven early stop is enabled in the workflow engine. Once completed node claims meet the verification policy with persisted evidence and no conflicts, the scheduler cancels remaining pending work and usage stops growing beyond completed checkpoints.

`HIGH-13` wires proactive triggers into the long-running service path. `pachat serve` uses `proactive.enabled` and `proactive.poll_interval` to recover trigger/watch state, process due work, enqueue Runtime workflows, and stop through context cancellation.

`OP-01` adds the persistent Skill operator CLI: validate/import/list/show/version manifests, enable or disable active versions, and run enabled Skills through Runtime workflows.

P14 documentation lives under `docs/release/` and covers quickstart, user operations, administration, security/threat model, troubleshooting, upgrade, RC E2E scenarios, soak testing, performance baselines, and data-loss recovery drills. The current release package is archive/script based; package manager publishing and Windows binaries are post-v1 work.

### Validation

Run:

```sh
go test ./...
go test ./internal/release
go test ./internal/e2e -run FunctionalRC
go run ./cmd/pachat release soak --config configs/config.example.yaml --duration 1s --interval 1s --output /tmp/pachat-soak.json
go run ./cmd/pachat release recovery-drill --config configs/config.example.yaml --work-dir /tmp/pachat-recovery-drill --output /tmp/pachat-recovery-drill.json
go run ./cmd/pachat release perf-baseline --config configs/config.example.yaml --work-dir /tmp/pachat-perf-baseline --output /tmp/pachat-perf-baseline.json
make build
make smoke
make release-build
make checksums
go run ./cmd/pachat release check --quick
```

### Safety Defaults

- Browser sessions, cookies, tokens, passwords, and profile data remain local.
- Portable exports include user-operable local data only and exclude credentials, browser cookie/password/profile data, coding CLI credential stores, and raw document chunk text.
- Public LLM browser planning can receive only Privacy Gateway redacted summaries.
- Browser actions pass through the Permission Layer before execution.
- High-risk actions such as login submission, payment, deletion, sending messages, uploads, OAuth grants, and legal acceptance require confirmation or elevated policy.
- Evidence Bus records must be redacted before storage.
- API responses do not echo raw task input and redact secret-like text in titles, final answers, events, and confirmation fields.

## 中文

面向本地用户工作流的 AI Agent。

本仓库是一个 Go 版本本地优先并行个人 Agent。当前实现包含 P0 基础层、P1 模型/隐私基础层、P2 RAG/记忆基础层、P3 编排/Sub-Agent 基础层、P4 浏览器运行时基础层、P5 权限/验证/成本基础层、P6 API/E2E/加固基础层、P7 Runtime MVP 闭环、P8 External Coding Agent 库层基础、P9 Capability/Skill/Workflow 核心闭环、P10 proactive trigger/event 核心闭环、P11 Runtime Integration 闭环、P12 Operator Experience 核心闭环、P13 Reliability/Security/Observability 核心 slice，以及 P14 v1.0 GA 发布打包 slice：`pachat` CLI 打包、配置加载、SQLite 迁移、核心 agent/browser 契约、Runtime-backed chat、可取消 API 后台长任务、模型注册/策略契约、OpenAI-compatible provider 边界、面向 public remote 模型调用的 fail-closed Privacy Gateway、本地 BM25 检索、可选 vector/RRF/reranker 的 hybrid RAG 降级路径、上下文压缩、working/episodic/semantic memory store、可取消 DAG 编排、依赖感知并行调度、Sub-Agent retry、early stop、有序节点生命周期 evidence event、受治理的 chromedp 浏览器运行时边界、全局权限策略评估、持久化 approval inbox、确认 approve/deny API、claim/evidence 验证、冲突检测、基于 registry 的成本核算、本地 REST 任务 API、基于 mock 的完整本地 E2E 覆盖、secret-leak 回归 fixture、CLI/API 任务执行使用的 workflow-backed Runtime、有界模型驱动 Leader planning 接口、面向 Codex/Claude 风格 CLI backend 的受治理 External Coding Agent 边界、确定性 capability/skill registry、capability runtime metrics、Skill eval activation gate、draft skill candidate generation、持久化 workflow checkpoint 与 node dependency、只读 MCP metadata adapter 与受治理 MCP 执行占位、持久化 proactive trigger、不可信 event envelope、event ingestion API、proactive daemon tick/recovery helper、本地 notification inbox、有界 goal planning、filesystem watcher scan、严格版本化配置校验、本地 doctor 诊断、knowledge/project/operator 命令、打包本地 HTTP server、dashboard/SSE/model-discovery endpoint、backup/restore dry-run、retention cleanup、SQLite maintenance 命令、structured log redaction、本地 runtime metrics、JSON health/readiness 响应、发布版本元数据、跨平台 release build、checksum、install/upgrade 脚本、service template、生产配置样例、release runbook，以及覆盖 model HTTP、browser、MCP、tool、coding 进程边界的 context cancellation 一致性。

仓库包含 Browser Tool、Browser Session Manager、Permission Layer 接入、Privacy Gateway 脱敏、Evidence Bus 记录、模型选择、provider enforcement、hybrid 本地检索/记忆索引、编排、语义浏览器定位、accessibility 读取、截图、坐标兜底、带确认 gate 的浏览器执行、全局权限决策、验证 gate、预算控制、本地 HTTP handler wiring、workflow-backed Runtime execution、coding agent adapter 选择、安全进程执行、环境脱敏、Git worktree 隔离、CLI adapter request mapping、coding evidence 收集、repository privacy 检查、trigger scheduling helper、trigger state persistence、event deduplication、本地运维工具、持久化 approval、notification、dashboard event stub、watcher scan 和有界 goal planning。仓库仍不包含 cookie 读取器、密码读取器、生产级 OS/kernel/browser 隔离、外部分布式 tracing export、外部漏洞扫描或第三方通知投递。

### 内容

- `cmd/pachat`：CLI 入口。
- `configs/config.example.yaml`：本地优先示例配置。
- `internal/config`：YAML 加载和校验。
- `internal/storage`：SQLite 存储与迁移。
- `internal/memory`：working、episodic 和 semantic memory 存储。
- `internal/rag`：document/chunk 存储、chunking、BM25 检索、RRF 融合、Qdrant wrapper、reranker 边界和上下文压缩。
- `internal/agent`：Leader/Sub-Agent 契约、结构化结果、claims、evidence IDs、usage 和错误分类。
- `internal/orchestrator`：DAG 校验、ready-node 计算、并行调度、retry、取消传播、early stop 和生命周期 evidence event。
- `internal/browser`：Go 契约、chromedp runtime adapter、session/profile 策略校验、semantic locator、accessibility/screenshot helper、confirmation gate、coordinate fallback、脱敏、evidence builder 和测试。
- `internal/permission`：全局权限策略、评估器、确认流程和审计记录。
- `internal/verification`：claim 抽取、claim/evidence 覆盖率、冲突检测和置信度策略。
- `internal/cost`：基于 registry 的价格查询、usage 估算和预算限制执行。
- `internal/model`：模型 trust level、capability、registry、policy selection、provider interface 和 OpenAI-compatible HTTP 边界。
- `internal/privacy`：HMAC-SHA256 pseudonymization、secret redaction、credential dump blocking 和 audit metadata。
- `internal/api`：本地 REST handler，支持 task create/list/status/cancel/events、trigger/event ingestion、dashboard/SSE/model discovery、notification inbox 和 confirmation inspect/approve/deny。
- `internal/runtime`：本地优先 Personal Agent Runtime，通过持久化 workflow 执行 task，连接有界 planning、memory、hybrid RAG、evidence、verification、synthesis、受治理外部 capability dispatch 和 public escalation fail-closed 边界。
- `internal/codingagent`：External Coding Agent 契约、registry、安全进程执行器、worktree 隔离、Codex/Claude CLI adapter、环境脱敏和 evidence 验证。
- `internal/capability`：Capability 描述、health 状态、确定性 policy resolver、runtime metrics 和 Runtime capability 注册。
- `internal/skill`：版本化 Skill manifest、依赖/权限校验、registry、skill-first matching、eval-gated activation 和 draft candidate generation。
- `internal/workflow`：持久化 workflow/node 状态、持久化 dependency、checkpoint、recovery 和 pause/resume/cancel 状态转换。
- `internal/project`：通过 SQLite 持久化的 Project context 与 allowlist policy。
- `internal/mcp`：只读 MCP server/tool metadata 和 capability adapter 边界。
- `internal/trigger`：proactive trigger 模型、校验、持久化、调度计算、daemon tick/recovery、condition evaluation、watcher scan、deduplication、debounce、cooldown、jitter、backoff helper、history 和 dead-letter storage。
- `internal/event`：不可信 event envelope、内存 event bus、SQLite event store 和 event deduplication。
- `internal/notification`：SQLite-backed 本地 notification inbox。
- `internal/goal`：有界本地 goal planner。
- `internal/observability`：structured log redaction、进程内 metrics snapshot 和 health/readiness report。
- `internal/e2e`：基于 mock 的本地集成测试，组合 API、SQLite、RAG、memory、model mock、浏览器 denial、verification、cost 和 secret-leak 回归覆盖。

### P0 使用方式

当前已完成的 P0 工具是 `pachat`，用于验证本地配置加载、SQLite 迁移、task 持久化、交互式记忆和长任务状态跟踪。

构建：

```sh
make build
```

`make build` 也会把安全的本地优先示例配置复制到 `bin/config.example.yaml`，因此在 `bin/` 目录中可以使用 `./pachat chat --config config.example.yaml`。复制后的示例默认关闭 public remote provider；只有在设置所需环境变量（包括 `PERSONAL_AGENT_PRIVACY_HMAC_SECRET`）后才启用。

运行 no-op 任务：

```sh
bin/pachat run --config configs/config.example.yaml --task "smoke test"
```

使用自己的任务文本：

```sh
bin/pachat run --config configs/config.example.yaml --task "帮我整理本地资料"
bin/pachat run --config configs/config.example.yaml --project <project_id> --task "整理项目资料"
bin/pachat run --config configs/config.example.yaml --task "帮我整理本地资料" --stream
```

预期输出形态：

```text
task_id=task_<generated_id>
status=completed
confidence=0.90
remote_tokens=0

answer:
Local-first answer for: 帮我整理本地资料
```

执行过程：

- CLI 加载并校验 `configs/config.example.yaml`。
- SQLite 打开位置为 `./data/personal-agent.db`。
- 执行 `internal/storage/migrations` 中的内置迁移。
- 以 `running` 状态向 `tasks` 表写入一条记录。
- Runtime 查询本地 memory 和本地 RAG、写入 evidence、验证答案，并将任务标记为 `completed`。

带 slash 命令补全的交互式本地记忆：

```sh
bin/pachat chat --config configs/config.example.yaml
```

进入 chat 后，输入普通消息会写入本地 episodic memory。输入 `/` 后按 `Tab` 可以补全内置命令。使用 `/help` 查看命令列表，使用 `/memory` 查看近期记忆，使用 `/exit` 或 `/quit` 退出。

长任务状态：

```sh
bin/pachat run --config configs/config.example.yaml --task "long local task" --long
bin/pachat task list --config configs/config.example.yaml
bin/pachat task show --config configs/config.example.yaml --id <task_id>
bin/pachat task usage --config configs/config.example.yaml --id <task_id>
bin/pachat task cancel --config configs/config.example.yaml --id <task_id>
```

运维命令：

```sh
bin/pachat init --config configs/config.yaml --data-dir ./data
bin/pachat config validate --config configs/config.example.yaml
bin/pachat doctor --config configs/config.example.yaml
bin/pachat knowledge add --config configs/config.example.yaml ./notes.txt
bin/pachat knowledge list --config configs/config.example.yaml --json
bin/pachat project create --config configs/config.example.yaml --name "Local Project"
bin/pachat skill import --config configs/config.example.yaml --path ./skill.yaml
bin/pachat skill list --config configs/config.example.yaml
bin/pachat skill run --config configs/config.example.yaml --id <skill_id> --input "local task"
bin/pachat mcp list --config configs/config.example.yaml
bin/pachat mcp health --config configs/config.example.yaml
bin/pachat mcp tools --config configs/config.example.yaml
bin/pachat workflow events --config configs/config.example.yaml --id <workflow_id>
bin/pachat trigger create --config configs/config.example.yaml --type manual --id local-trigger
bin/pachat trigger run-now --config configs/config.example.yaml --id local-trigger
bin/pachat approval list --config configs/config.example.yaml
bin/pachat notification list --config configs/config.example.yaml
bin/pachat model discover --config configs/config.example.yaml
bin/pachat watcher scan --config configs/config.example.yaml --path ./docs
bin/pachat serve --config configs/config.example.yaml
curl http://127.0.0.1:8787/healthz
curl http://127.0.0.1:8787/readyz
curl http://127.0.0.1:8787/metrics
curl http://127.0.0.1:8787/dashboard
curl http://127.0.0.1:8787/models/discover
curl "http://127.0.0.1:8787/tasks?limit=20&offset=0&status=running"
curl "http://127.0.0.1:8787/workflows?limit=20&offset=0&project_id=default"
curl "http://127.0.0.1:8787/events?limit=20&offset=0&type=push"
curl "http://127.0.0.1:8787/projects?limit=20&offset=0&q=local"
curl "http://127.0.0.1:8787/skills?limit=20&offset=0&status=active"
bin/pachat backup create --config configs/config.example.yaml
bin/pachat export --config configs/config.example.yaml --output portable.json
bin/pachat import --config configs/config.example.yaml --input portable.json
bin/pachat storage integrity --config configs/config.example.yaml
```

当前 CLI 边界：默认 task execution 已通过 workflow 和 scheduler 执行，并保持本地优先。Browser、Coding、MCP 和 public model 路径必须显式配置 executor/provider，否则按治理策略 fail closed。

取消边界：API/CLI task cancel 会向 Runtime workflow、scheduler、Sub-Agent execution、配置的 model HTTP 调用、vector HTTP 调用、browser executor、MCP 子进程、allowlisted 本地 tool 子进程，以及 Codex/Claude 风格 coding process runner 传播。

Skill workflow 边界：active、高置信、read-only Skill 会直接编译为带 `skill_id` 和 `skill_version` 的持久化 workflow，并通过现有 scheduler 执行，不调用 Leader planner。没有合格 Skill 命中时继续回退到 planner-backed 或默认 Runtime workflow。

Skill operator CLI：`pachat skill validate/import/list/show/versions/enable/disable/run` 会管理持久化在 SQLite 中的 Skill manifest，并通过现有 Runtime workflow 路径执行已启用 Skill。

后台 workflow worker：`pachat serve` 会启动 Runtime worker，在服务启动时恢复未完成且可运行的 workflow，并持续轮询后续工作。`paused` 和 `waiting_approval` workflow 会保留给 operator 或 approval 操作处理。

Proactive daemon：当 `proactive.enabled` 为 true 时，`pachat serve` 会启动 proactive trigger loop。它会恢复 trigger/watch state，按 `proactive.poll_interval` 处理到期 schedule 和 condition-watch transition，把 trigger work 派发为 Runtime workflow，并通过 server context 安全退出。

Side-effect idempotency：非 read-only workflow capability 在执行前会持久化认领 idempotency key。duplicate event、retry 或 crash recovery 触发的 replay 不会重复执行已认领的 browser write、MCP/tool mutation、coding push、PR create、deploy 或 message send 类 side effect。

Project policy 边界：Runtime workflow execution 会把 project policy 统一应用到 Skill matching、Leader model selection、memory/RAG capability、public escalation、browser/MCP/tool dispatch，以及 Codex/Claude backend allowlist。CLI task run 可用 `--project` 选择 project；API task create 接受 `project_id`。

Coding Cross Review：设置 `coding_agents.cross_review.enabled: true` 并配置 `large_diff_bytes` 后，security-sensitive、critical-project、verification-failed 或 large-diff coding task 会要求第二个已启用 coding backend 做审查。普通 coding task 仍只使用一个 backend。

真实 coding E2E：运行 `PACHAT_CODING_AGENT_E2E=1 PACHAT_CODEX_CLI_PATH=/path/to/codex go test ./internal/codingagent`，或设置 `PACHAT_CLAUDE_CODE_CLI_PATH` 验证 Claude，会在临时 repository 中调用真实 CLI backend。默认未显式启用时这些测试会跳过。

Condition watcher state：proactive condition-watch trigger 会持久化 previous/current state、last transition、last check、last notification 和 cooldown state，daemon 重启后会保留 watcher 上下文。

Notification policy：本地 notification 会按 `dedup_key` 去重，记录 severity 和 delivery state，并支持通过 notification policy 做 quiet-hours suppression。

Goal persistence：bounded goal 和 milestone 会持久化 status、dependencies、workflow linkage、completion criteria、budget policy、pause/resume state 和 re-evaluation timestamp。

### P1 模型与隐私基础

P1 新增库层模型和隐私控制：

- 模型 trust level：`local_private`、`local_sandboxed`、`trusted_remote`、`public_remote`。
- 模型 capability：`chat`、`tool_calling`、`json_schema`、`vision`、`embedding`、`rerank`、`long_context`、`browser_reasoning`。
- Registry 校验 duplicate model ID、provider reference、trust level 和 capability。
- 按 role 独立选择模型，确保 Leader 与 Sub-Agent 配置互不泄漏。
- 可 mock 的 chat、embedding、rerank provider interface。
- OpenAI-compatible chat 和 embedding HTTP 边界，支持注入 transport 进行无网络测试。
- Privacy Gateway 支持稳定 HMAC-SHA256 pseudonymization、redaction、blocking 和 audit metadata。
- public remote provider 在 Privacy Gateway 缺失或 payload 被阻断时 fail closed。

### P2 RAG 与记忆基础

P2 新增库层检索和记忆控制：

- 使用 content hash 的确定性 document chunking。
- 基于现有本地 schema 的 SQLite document/chunk 持久化。
- 针对已存储 chunk 的本地 BM25 检索。
- 使用 Reciprocal Rank Fusion 合并多路检索结果，并提供确定性 tie-breaking。
- Qdrant HTTP wrapper，base URL、collection、API key 和 HTTP client 均由调用方提供。
- Reranker interface，为后续模型 rerank 接线预留边界。
- 上下文压缩会保留 evidence ID、source URI、title、metadata、score 和 privacy class。
- 支持过期清理的 working memory。
- 保留用于按时间记录本地事件的 episodic memory。
- Semantic memory 支持把长期事实和偏好可选索引到 RAG。

### P3 编排与并行 Sub-Agent 基础

P3 新增库层编排控制：

- Task DAG 校验 duplicate node ID、missing dependency、self-dependency 和 cycle。
- 依赖感知 ready-node 计算。
- 通过按 role 注册的 Sub-Agent contract 做并行调度。
- 基于每个 node 的 max-attempt policy 处理 retryable failure。
- 支持调用方提供 early stop policy。
- 将 context cancellation 传播给正在运行的 Sub-Agent。
- 结构化 node result，包含 claims、evidence IDs、usage 和 typed error category。
- 有序 Evidence Bus 生命周期事件，覆盖 ready、started、retrying、completed、failed、cancelled 和 early stop。

### P4 浏览器运行时基础

P4 新增受治理的本地浏览器 runtime 边界：

- Runtime action adapter 支持 navigation、读取、截图、accessibility 读取、semantic targeting、输入和坐标兜底。
- 基于 chromedp 的 runtime，allocator options 和截图目录都由调用方配置。
- 本地 profile store 从配置的环境变量解析 profile 目录，不在 metadata 中暴露 profile path。
- write 和 high-risk 浏览器动作必须通过 confirmation gate。
- 通过内存 browser Evidence Bus 边界发布脱敏浏览器 evidence。
- 本地浏览器 E2E fixture 位于 `internal/browser/testdata/browser`；使用 `PACHAT_BROWSER_E2E=1 go test ./internal/browser` 运行。

### P5 权限、验证与成本基础

P5 新增库层安全和核算控制：

- 覆盖文件系统、网络、浏览器读写、高风险浏览器动作、memory write、public model call、credential access 和 external side effect 的全局权限动作分类。
- 可配置的权限默认策略和按 action 覆盖规则。
- 确认请求包含 action、target、risk、evidence IDs 和精确 proposed effect。
- 可审计的权限决策记录，供后续持久化为 evidence。
- 确定性 claim 抽取和 claim/evidence 覆盖率检查。
- 检测 claim 与 evidence 之间的冲突。
- 用于 final synthesis 的置信度策略 gate。
- 基于 registry/config 的价格查询和模型 usage 成本估算。
- 预算 soft limit 和 hard limit 执行。

### P6 API、E2E 与加固基础

P6 新增本地 API 和集成验证：

- 基于标准库的 REST handler，支持 task create、list、status、cancel 和 event listing。
- 基于 confirmation store 边界的 confirmation inspect、approve 和 deny endpoint。
- JSON 响应提供稳定状态码，并对 API 输出文本做 secret redaction。
- 完整本地 E2E 测试，覆盖 provider mock、SQLite、RAG、episodic/semantic memory、浏览器高风险拒绝、verification、cost accounting 和 final synthesis。
- 针对 API 响应、Privacy Gateway 输出和 browser evidence 的 secret-leak 回归 fixture。
- PostgreSQL integration test placeholder，在未配置 test DSN 时会干净跳过。

### P7 Runtime Integration / Personal Agent MVP

P7 Runtime MVP 已记录在 `docs/projdocs/P0_P7_EXECUTION_PLAN.md`、`docs/p7_runtime_integration.md`、`docs/p7_runtime_mvp_implementation.md` 和 `docs/projdocs/task/P7.md` 中。第一条 vertical slice 已将 storage、memory、本地 RAG、evidence、verification、synthesis、CLI execution 和 API task creation 连接成真正的本地优先任务执行路径。

P7 MVP 验收覆盖移除 no-op task path、local-first evidence、本地证据足够时 public model usage 为零、workflow-backed CLI/API task execution、Runtime-backed chat turn、verification report、持久化 final answer、API background cancellation、有界 Leader planning interface、受治理 BrowserAgent execution boundary、public model escalation fail-closed interface、真实 provider usage 传播和 secret-leak 回归覆盖。

### P8 External Coding Agents

P8 已记录在 `docs/projdocs/P0_P8_EXECUTION_PLAN.md`、`docs/p8_external_coding_agents.md` 和 `docs/projdocs/task/P8.md` 中。第一条库层 slice 增加了面向 Codex CLI 与 Claude Code CLI 风格 backend 的受治理 External Coding Agent 边界。

P8 包含可配置 backend enablement、独立的 execution location 与 inference trust、基于 capability 的 backend selection 与 fallback、安全 subprocess 执行及 timeout/cancellation、最小环境脱敏、Git worktree 隔离、Codex/Claude adapter request mapping、针对 confidential repository + public remote inference 的隐私拒绝，以及要求真实 diff/files/tests 而非 self-reported completion 的 evidence 验证。

当前 P8 边界：Runtime workflow dispatch 能识别 coding capability node；未显式启用 backend 时会返回受治理的 blocked/unavailable result。示例配置默认禁用 Codex/Claude，且禁用 direct writes。

### P9 核心 Release Slice

P9 已记录在 `docs/p9_core_release_slice.md`、`docs/projdocs/P0_P9_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P9.md` 中。本核心 slice 增加确定性的 Capability Registry/Resolver、版本化 Skill manifest 校验与匹配、Project-scoped policy 数据、带 checkpoint 和 crash recovery 查询的持久化 Workflow state，以及只读 MCP metadata adapter。

运维命令：

```sh
bin/pachat capability list --config configs/config.example.yaml
bin/pachat capability health --config configs/config.example.yaml
bin/pachat workflow list --config configs/config.example.yaml
bin/pachat workflow show --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow pause --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow resume --config configs/config.example.yaml --id <workflow_id>
bin/pachat workflow cancel --config configs/config.example.yaml --id <workflow_id>
```

当前 P9 边界持久化 Workflow 状态、node dependency 和 checkpoint，并提供安全 lifecycle transition、capability metrics-aware routing、eval-gated Skill activation、draft skill candidate generation 和受治理 MCP 执行占位。外部 MCP transport 未显式配置 executor 时 fail closed。

### P10 Proactive Trigger/Event 核心 Slice

P10 已记录在 `docs/p10_core_release_slice.md`、`docs/projdocs/P0_P10_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P10.md` 中。第一条 proactive slice 增加持久化 trigger definition/state、one-shot/interval/simple-cron 调度计算、确定性 execution key、debounce、cooldown、jitter、指数 backoff helper、不可信 event envelope、event deduplication，以及带 SQLite 持久化的内存 event bus。

当前 P10 边界：已提供 trigger CLI/API、push event ingestion、trigger history、daemon tick/recovery helper、condition evaluation、dead-letter persistence、本地 notification inbox、有界 goal planning 和 filesystem watcher scan。持续常驻 daemon 与第三方通知投递属于部署选择。

### P11 Runtime Integration Closure Slice

P11 已记录在 `docs/p11_runtime_integration_closure.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P11.md` 中。第一条 closure slice 增加 Runtime 持有的 workflow engine：它会加载持久化 workflow run，恢复非终态 node，检查 project capability policy，检查已注册 capability 可用性，通过现有 scheduler 执行本地只读 node，写入 per-node checkpoint，并将 workflow 标记为 completed、failed 或 cancelled。

当前 P11 边界：`Runtime.Run()` 会创建持久化 workflow 并通过 scheduler 执行。持久化 workflow 可以执行 `memory.search`、`rag.search`、`verification.verify` 和 `synthesis.local`；Browser/Coding/MCP node 会路由到 guarded SubAgent，并在 executor 不可用时 fail closed。

### P12 Operator Experience 核心 Slice

P12 已记录在 `docs/p12_operator_experience.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P12.md` 中。本核心 slice 增加版本化严格配置校验、`pachat init`、`pachat config validate`、`pachat doctor`、本地 knowledge index/list/status/reindex/remove 命令、project create/list/show/update/archive/use 命令、task/workflow JSON 输出、workflow checkpoint event 查看、带 health/readiness endpoint 的本地 `pachat serve` 命令、backup create/restore dry-run、event retention cleanup，以及 SQLite integrity/vacuum/checkpoint maintenance 命令。

当前 P12 边界：operator CLI 与 REST API 已包含持久化 approval inbox、本地 notification inbox、dashboard 和 SSE-ready endpoint、基于配置的 model discovery、filesystem watcher scan 和打包本地 serving。dashboard endpoint 是最小可运行状态面，不是完整视觉 Web 应用。

### P13 Reliability / Security / Observability Release Gate

P13 已记录在 `docs/p13_reliability_security_observability.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P13.md` 中。本核心 slice 增加 structured log 与 API-facing text 的集中脱敏、进程内本地 runtime metrics、`pachat serve` 的 JSON `/healthz` 和 `/readyz`、JSON `/metrics` endpoint、请求/错误/活动请求计数，以及 observability 输出的 secret-leak 回归覆盖。

P13 剩余 release-gate 基础现已补齐：本地 tracing span、全局 resource governor、readiness 中的 disk pressure check、graceful shutdown 协调原语、API auth/CORS/body limit/rate limit/security header middleware、command/path/network policy helper、prompt-injection 与 secret-leak fixture、coding-agent sandbox 检查、browser security regression helper、持久化 `security_audit_log` migration/store、golden eval case，以及可度量 quality-gate threshold evaluation。

当前 P13 边界：P13 现在提供本地 policy boundary 和 regression gate，但不声称已经具备生产级 OS sandbox、分布式 tracing export、外部漏洞扫描服务，或 kernel/browser 隔离能力。

### P14 v1.0 GA 发布打包

P14 已记录在 `docs/p14_ga_release_requirements.md`、`docs/projdocs/P0_P14_EXECUTION_PLAN.md` 和 `docs/projdocs/task/P14.md` 中。本发布 slice 增加版本模型（`pachat version`）、可复现发布元数据、`darwin/arm64`、`linux/amd64` 与 `linux/arm64` 的必需构建矩阵、SHA256 checksum 生成、bootstrap install 与 upgrade 脚本、systemd 与 launchd 服务示例、安全 production config 示例、迁移兼容测试，以及通过 `pachat release check` 和 `make release-check` 执行的发布检查自动化。

默认配置 `configs/config.example.yaml` 已使用本地 Ollama 模型 `qwen3.8:27b-mlx`；如本机模型名称不同，请在可挂载配置文件中修改 `models.registry[].model`。

`configs/config.example.yaml` 现在是自说明示例配置：每个示例 key 都带有中文和英文注释，说明该配置项、必要时说明取值形态，并对 provider、browser、network、storage 和 external-tool 等敏感配置标注隐私或安全影响。`models.registry` 包含本地 chat/planning、本地 embedding、本地 reranking，以及默认禁用的 public remote chat 模型完整示例。注释和额外的禁用/可选 registry 示例不改变默认的本地优先行为。

OpenAI-compatible chat message 现在会按协议要求序列化为小写的 `role` 和 `content` 字段，因此配置的 provider 可以正常接收 planner、reasoning、synthesis 和交互式 chat 请求。

交互式 `pachat chat` 现在会把每条普通输入送入统一 task workflow。系统先检索本地 memory/vector/BM25；本地 evidence 足够时只调用配置的 synthesis Sub-Agent，不足时才调用 Leader planner 和后续 Sub-Agent。Leader provider 仍由 `agent.leader.model_id` 选择；Planner 输出受 `agent.planner.max_nodes` 约束，并会先通过 Runtime capability registry 与 DAG 依赖校验；引用未知 capability 或无效依赖时，会在创建 workflow 前失败。provider 连接或响应错误会明确返回。

Planner 配置：

```yaml
agent:
  planner:
    enabled: true
    max_nodes: 8
```

Workflow 中的 reasoning 与 synthesis 现在会在 `Runtime.ChatProvider` 可用时复用配置的私有本地模型。`reasoning.local` 输出有界 claims、evidence IDs、confidence 和 provider usage，不保存 chain-of-thought。`synthesis.local` 基于已验证 evidence 写出最终答案，`Runtime.Run()` 会使用完成的 synthesis checkpoint 作为持久化 task answer。

Workflow-backed reasoning 和 synthesis 会读取各自的 Sub-Agent model ID。将 `agent.subagents.reasoning.model_id` 或 `agent.subagents.synthesis.model_id` 指向 `models.registry` 中的模型，即可让这些节点使用不同的已配置 provider。`pachat chat` 和 `pachat run` 都使用统一的 `Runtime.DispatchTask` workflow 路径；因此 chat 普通输入在本地检索不足时会调用 reasoning/synthesis，本地 evidence 命中时只调用轻量 synthesis。

Hybrid RAG 生产接线由可挂载的 `rag` 配置控制。`rag.vector` 会组合 embedding 模型与 Qdrant 向量检索；`rag.reranker` 会在可用时组合配置的 rerank 模型。vector search 或 reranking 禁用/不可用时，检索会继续使用本地 BM25 fallback。

Runtime capability executor 现在通过本地 executor registry 注册。启用 browser 后，`browser.read`、`browser.navigate` 和 `browser.write` 会进入受治理的 browser tool；read observation 会作为 Runtime evidence 持久化，navigation 与 write action 仍经过 session/domain policy 和 confirmation gate。

启用的 coding backend 会注册为 `coding.<backend>` executor，包括 Codex 与 Claude 风格 adapter。Coding workflow node 必须提供结构化 JSON 输入，包含 `repository_path`、`prompt`、写入策略、privacy class 和可选 test commands；执行会委托给受治理的 coding agent Runner，并持久化 diff/test evidence。

配置的 MCP server 会注册为 `mcp.<server>.<tool>` executor，并通过 stdio JSON-RPC 调用。MCP 输出会作为 `UNTRUSTED OBSERVATION` evidence 持久化。配置的本地工具来自 `tools.allowlist`；执行时只使用固定 allowlisted program/args，不会把 workflow input 拼接进 shell 命令。

`OP-02` 增加 MCP operator 命令：列出已配置 server、检查 command 可用性、枚举 MCP tool capability，并通过 `pachat mcp test` 走 Runtime workflow execution 对配置的 tool 做 smoke test。

`OP-03` 将 `/dashboard` 从 stub 替换为最小 operator console，包含 Chat/Run、Tasks、Workflows、Approvals、Notifications、Projects、Skills、Capabilities Health 和 Triggers 视图。

`OP-04` 将 `/dashboard/events` 产品化为 typed SSE，覆盖 Task、Workflow、Approval、Notification、Trigger 和 ready event，并支持 `Last-Event-ID` reconnect filtering 与 heartbeat keepalive。

`OP-05` 增加 `/evidence`、`/evidence/{id}` 和 dashboard Evidence 视图，用于查看 claim、脱敏 preview、source type、trust、privacy、timestamp 和 verification status。

`OP-06` 完成完整 backup restore 执行：临时数据库校验、migration compatibility check、SQLite integrity check、现有数据库 pre-restore copy，以及最终校验失败时 rollback。

`OP-07` 增加便携数据导出/导入。`pachat export` 会把 Project、Skill 及版本、选定 episodic/semantic memory 和 knowledge document metadata 写入版本化 JSON artifact。`pachat import` 会校验 artifact，并把这些对象 upsert 到配置指向的本地数据库。它不会导出 credential、浏览器 cookie、浏览器密码、浏览器 profile data、coding CLI credential store 或原始 document chunk text。

`OP-08` 为 `pachat run --stream` 增加前台 streaming。stream 会报告 planning、workflow progress、node state、tool-like node progress、approval wait state 和 final answer delta，并对 secret-like 文本脱敏。它不会暴露 chain-of-thought 或原始 reasoning checkpoint 字段。

`OP-09` 为 tasks、workflows、events、notifications、projects、triggers 和 skills 增加带分页与过滤的 REST list API。列表 endpoint 支持有界 `limit` 和 `offset`，以及 `status`、`project_id`、`type`、`enabled`、`privacy_class`、`skill_id`、`source`、`severity`、`q` 等资源相关过滤参数。

Functional RC E2E gate 可通过 `go test ./internal/e2e -run FunctionalRC` 执行。它使用 mock-backed external executor 和真实 Runtime persistence/scheduling，覆盖 local RAG/memory 且公网调用为 0、模型驱动 DAG planning、Browser read/write evidence、Codex 风格 diff/test evidence、Claude 风格 review evidence、MCP evidence、经 Privacy Gateway 的 public escalation、restart/idempotency 行为，以及 false-to-true watcher notification 去重。

`pachat release check --quick` 现在包含必过 Functional GA gate：Functional RC E2E、Runtime Leader planning、external executor registry 接线、workflow restart recovery、privacy escalation、early-stop cancellation、side-effect idempotency 和 notification edge semantics。真实外部 CLI/browser 二进制验证仍是 release candidate 的 opt-in 步骤。

`pachat release soak --config <path>` 会执行可运行的 soak gate。默认 offline 模式不要求外部模型、browser 或 CLI 二进制，但会覆盖真实本地 Runtime 与 SQLite workflow 路径，采样 goroutine、heap、SQLite、workflow、notification 和 external-artifact 指标，并可用 `--output <path>` 写入 JSON 报告。在已配置外部 backend 的 release-candidate 环境中，可使用 `--duration 24h --interval 1m --offline=false`。

`pachat release recovery-drill --config <path>` 会针对一次性 fixture 状态执行 data-loss recovery drill。它会写入 Projects、Skills、Memory、Workflow、Trigger、Notification 和 Approval 记录，通过正常 backup 命令路径创建备份，删除 fixture SQLite 文件，恢复到新的数据库，验证 SQLite integrity 与每类对象，并可用 `--output <path>` 写入 JSON 报告。

`pachat release perf-baseline --config <path>` 会从真实本地 Runtime 记录 regression baseline。默认 offline 模式使用隔离 SQLite 数据库，测量启动、本地 memory 查询、hybrid RAG fallback、workflow dispatch、heap allocation 和并发 workflow 完成行为，并可用 `--output <path>` 写入 JSON 报告。

Public escalation 会从 model registry 中启用的 `public_remote` chat model 接线。如果 public provider 要求 Privacy Gateway，`privacy.hmac_secret_env` 必须能解析，否则 Runtime build 会 fail closed。包含 secret 的 payload 会在调用 provider 前被阻断。

可以使用 `pachat task usage --config <path> --id <task_id>` 查询 task usage。Usage 会从 workflow checkpoint 聚合到 node、workflow 和 task 层级。具备可靠 usage 的模型 provider 会使用 provider token count；外部 CLI/tool 在没有 token count 时会标记为 unknown。

Workflow engine 已启用 verification-driven early stop。当已完成节点的 claims 通过持久化 evidence 验证、满足 verification policy 且没有冲突时，scheduler 会取消剩余 pending work，usage 不会继续增长到被取消的节点之后。

`HIGH-13` 将 proactive trigger 接入长运行服务路径。`pachat serve` 使用 `proactive.enabled` 与 `proactive.poll_interval` 恢复 trigger/watch state、处理到期工作、入队 Runtime workflow，并通过 context cancellation 停止。

`OP-01` 增加持久化 Skill operator CLI：validate/import/list/show/version manifest，enable 或 disable active version，并通过 Runtime workflow 运行已启用 Skill。

P14 文档位于 `docs/release/`，覆盖 quickstart、用户操作、管理员操作、安全/threat model、troubleshooting、upgrade、RC E2E scenario、soak test、performance baseline 和 data-loss recovery drill。当前发布包采用 archive/script 形式；package manager 发布和 Windows 二进制属于 post-v1 工作。

### 验证

运行：

```sh
go test ./...
go test ./internal/release
go test ./internal/e2e -run FunctionalRC
go run ./cmd/pachat release soak --config configs/config.example.yaml --duration 1s --interval 1s --output /tmp/pachat-soak.json
go run ./cmd/pachat release recovery-drill --config configs/config.example.yaml --work-dir /tmp/pachat-recovery-drill --output /tmp/pachat-recovery-drill.json
go run ./cmd/pachat release perf-baseline --config configs/config.example.yaml --work-dir /tmp/pachat-perf-baseline --output /tmp/pachat-perf-baseline.json
make build
make smoke
make release-build
make checksums
go run ./cmd/pachat release check --quick
```

### 默认安全策略

- 浏览器 session、cookie、token、密码和 profile 数据仅保留在本地。
- 便携导出只包含用户可操作的本地数据，排除 credential、浏览器 cookie/password/profile data、coding CLI credential store 和原始 document chunk text。
- 公网 LLM 参与浏览器规划时，只能接收 Privacy Gateway 脱敏后的摘要。
- 所有浏览器动作执行前都必须经过 Permission Layer。
- 登录提交、支付、删除、发送消息、上传、OAuth 授权、接受法律条款等高风险动作需要用户确认或更高权限策略。
- Evidence Bus 写入前必须完成敏感信息脱敏。
- API 响应不会回显原始 task input，并会对 title、final answer、event 和 confirmation 字段中的 secret-like 文本做脱敏。

### Rebuilt preview: TypeScript terminal and Web / 重建预览：TypeScript 终端与网页

These are local preview clients for the **new** `serve-agent` protocol, not the legacy service or a recovered release. Install Node 24 and pnpm 11, then run `pnpm install`. Start `serve-agent` using the separate runtime and workspace described above; set `PA_AGENT_TOKEN` in the environment or use its protected token file. The clients never accept a token in a URL or command argument. The terminal reads `PA_AGENT_TOKEN`:

```sh
cd apps/terminal
node --import tsx src/cli.tsx --url http://127.0.0.1:7319 --mode chat_only
# Open an existing session directly (within Ink, /history lists IDs and /resume ID switches):
node --import tsx src/cli.tsx --url http://127.0.0.1:7319 --session SESSION_ID
# Non-interactive; stdout contains only the requested format:
node --import tsx src/cli.tsx --url http://127.0.0.1:7319 -p '你好' --format json
# text | json | stream-json; exit 0 completed, 2 failed, 3 blocked/drained, 4 outcome needs reconciliation
```

For the same-origin Web client, `make build-agent-web` builds local assets and embeds them in `bin/pachat`; start that binary with the `serve-agent` arguments above and open its loopback URL. The browser asks for the token each time and keeps it only in page memory. API-only builds have no Web assets. The Web and terminal use the same API SDK and presentation reducer; creating/reloading a view does not submit a message or approval. The run phase, exact approval review, tool output and server capability state come from the server. A stop is a request, not proof of termination. Unknown outcomes must be reconciled against the session, never blindly retried. Non-interactive approval blocks cancel and drain before returning where possible; an unconfirmed drain is reported as reconciliation required. Print does not impose an implicit deadline on a healthy run. For unknown submission or observation outcomes, JSON results include the session and original request IDs (and run ID when known); the client performs only read-only reconciliation and never resends the message.

The Web sidebar can load older session pages; both clients can load older message pages without replacing the live stream. In the terminal use `/history older` for messages and `/resume SESSION_ID` to switch sessions without exiting. The mode and model are immutable within a session. `/help`, `/status`, `/model`, `/context`, `/cost`, `/permissions`, `/history`, `/resume` and `/cancel` expose available operations. Cost is unknown without billing evidence. Hooks remain unavailable; explicit FIFO and bounded Jobs are described below; session context operations and opted-in MCP are described below. Native file operations require supported Linux capability and appropriate workspace approval; the optional exec host setting is **NON-SANDBOXED**, default off, and an exact review is required. Unsupported platforms can use `chat_only`; choosing another mode does not enable exec. No provider probe runs automatically. Configured search is not evidence that a search ran or returned sources. Model text is untrusted: the Web does not render raw HTML or load model-supplied images; terminal output strips escape/control sequences.

这是新 `serve-agent` 协议的本地重建预览，不是旧服务或已恢复的发布版本。安装 Node 24 和 pnpm 11，执行 `pnpm install`；按照上文使用独立运行目录和工作区启动服务。终端从环境变量 `PA_AGENT_TOKEN` 读取令牌；Web 同源页面每次手动输入令牌，仅保存在页面内存中。使用上面的终端命令，或先运行 `make build-agent-web`，再用生成的 `bin/pachat serve-agent` 启动并打开本机地址。

网页侧栏可加载更早会话，网页与终端均可加载更早消息而不覆盖实时状态；终端用 `/history older` 加载旧消息，用 `/resume 会话ID` 在不退出的情况下切换。会话模型与权限创建后不可更改。终端支持 `/help`、`/status`、`/model`、`/context`、`/cost`、`/permissions`、`/history`、`/resume` 和 `/cancel`；hooks 尚不可用；显式 FIFO 和有限后台 Jobs 见下文；会话分支、压缩和 MCP 的范围见下文。费用未知不显示为零。停止只是请求，必须等待服务端终态；未知结果需要核对，不得盲目重试。非交互审批会尝试取消并等待释放，无法确认时返回需核对。 正常运行没有隐式超时；发送或观察结果未知时，JSON 结果包含会话 ID 与原始请求 ID（已知时还包含运行 ID），只做只读核对，不会重发消息。Linux 文件能力和可选的**非沙箱** exec 均受服务端权限与逐次审批约束；其他原生平台可选择仅聊天。不会自动探测模型，也不会把已配置搜索显示成已执行或已返回来源。

### Local client acceptance and uncertain writes / 本地验收与未知写入结果

Both clients display disabled native capabilities with the server reason. After an unknown message response, the composer remains locked and retains the draft. Use the Web “重新同步会话（只读）” button or terminal `/sync` to read a fresh snapshot. Only a matching original request ID confirms admission and releases that uncertainty lock; a missing request remains unconfirmed. No message, approval, or tool is automatically replayed. Server approval/exec authority is unchanged.

两端均显示原生能力不可用的服务端原因。发送响应丢失时锁定发送并保留草稿；点击网页“重新同步会话（只读）”或终端输入 `/sync` 获取快照。仅匹配原请求 ID 的服务端记录能确认该次发送，缺失记录仍保持未确认。不会自动重放消息、审批或工具，服务端权限保持不变。

Local-only acceptance commands (Go/Node/pnpm installed):

```sh
make web-assets
CHROMIUM_PATH=/path/to/official/chromium/headless_shell node --test apps/web/test/{browser,pagination,acceptance}.test.ts
python3 apps/terminal/test/pty_e2e.py
python3 apps/terminal/test/acceptance_pty.py
```

`agent-fixture --safe-exec` is a test-only opt-in, rejects supplied workspace/runtime paths, and offers one fixed shell-builtin command in its owned temporary workspace. It never enables the production exec flag or probes a provider. `--chat-only-fixture` disables workspace authorization and labels the unavailable-native status as a **simulation**, not native macOS/Windows evidence. Fixtures have a five-minute lifetime and owned temporary state is removed on orderly shutdown. Real Chromium and Python PTY cover exact exec review, denial/no effect, repeat input/no duplicate effect, stream replay and unknown-response UI behavior. Firefox/WebKit, native other-OS execution and system IME remain unverified.

`agent-fixture --safe-exec` 仅用于测试，拒绝外部工作区/运行目录，只在自身临时目录提议固定 shell 内建命令，不打开生产 exec、不探测模型。`--chat-only-fixture` 关闭工作区授权，明确标记原生能力不可用是**模拟状态**，不声称 macOS/Windows 原生实测。fixture 最长五分钟，正常关闭时清理自身临时状态。真实 Chromium 与 Python PTY 验证精确命令审阅、拒绝无副作用、重复输入不重复执行、流重连和未知响应处理；Firefox/WebKit、其他原生系统和系统 IME 尚未验证。

### MCP stdio transport milestone / MCP stdio 传输阶段

The transport below is shared by the legacy executor and the explicitly opted-in rebuilt integration described above. Transport alone grants no tool authority; rebuilt sessions add immutable host policy, exact durable approvals and process-generation revocation. Server descriptions, `readOnly` hints, self-reported identity and cache hints are untrusted observations, not permission grants. No server, SDK, provider, or extension is automatically installed or enabled.

下述传输由旧执行器与上文明确授权的重建集成共用。传输本身不授予权限；重建会话额外绑定不可变主机策略、精确持久审批与进程代次撤销。服务端描述、只读提示、自报身份与缓存提示均不构成授权；不会自动安装或启用服务端、SDK、模型服务或扩展。

| Protocol/capability / 协议或能力 | This milestone / 此阶段 |
| --- | --- |
| `2026-07-28` stdio | Newline JSON; `server/discover`, per-request `_meta`, result types; no `initialize`. / 换行 JSON、发现、逐请求元数据及结果类型，不发送 initialize。 |
| `2024-11-05` stdio | Bounded discovery fallback, validated `initialize`, then `notifications/initialized`. / 有界探测回退、校验版本后发送 initialized。 |
| `-32022` | Intersect `error.data.supported` with implemented **modern** versions; retry discovery at most once; never downgrade to legacy. / 仅选择实现的现代版本，最多再探测一次，绝不降级。 |
| HTTP / old HTTP+SSE | Explicitly unsupported; application UI SSE is unrelated. / 明确不支持，与本应用 UI SSE 无关。 |
| Tasks / `input_required` / unknown result types | Not advertised or continued; reported as unsupported, never success or an effect retry. / 不声明、不续传，返回不支持，不算成功且不重放副作用。 |

`internal/mcp.OpenStdio(ctx, StdioConfig)` launches an explicitly trusted **absolute executable** with fixed argv, no added shell and an explicit child environment (`Env`, empty by default). This is **NON-SANDBOXED current-user code**. It can access files/network and launch descendants with the user's OS privileges. The legacy adapter resolves only its configured executable through `LookPath` and passes no inherited environment credentials; servers needing authentication/environment setup require a separately reviewed explicit configuration integration. Windows may receive OS-required `SystemRoot` from `os/exec`. Never put secrets in argv. Stderr is discarded, not accumulated or exposed in errors.

`OpenStdio` 要求可信绝对可执行路径与固定参数，不添加 shell；`Env` 为显式子进程环境，默认空。这是以当前用户权限运行的**非沙箱代码**，可访问文件、网络并创建后代进程。旧适配器只解析所配置命令，不继承环境凭据；需要认证环境的服务端须另做显式配置接入。Windows 的 `os/exec` 可能提供系统必需的 `SystemRoot`。不要把密钥放入参数；stderr 丢弃，不积累或进入错误消息。

The process era is fixed after negotiation. `ListTools` fetches every page with cursor-loop, page, tool-count and byte limits, returns a SHA-256 digest of canonical input/output schemas, and exposes per-page `ttlMs`/`cacheScope` hints. Missing/negative TTL is treated as zero; absent scope defaults private. Freshness is bounded to 24 hours. **No tool-list cache is served:** each call re-fetches from the beginning, never shares authorization contexts, and does not poll. Thus a list-change notification cannot leave a stale cached approval. `CallToolPinned` additionally checks a caller-supplied approved schema digest before sending. This pin does not verify server identity or grant permission, and cannot prevent a malicious server changing behavior after discovery.

进程协议时代协商后固定。`ListTools` 限制游标循环、页数、工具数与字节数，返回输入/输出 schema 的规范化 SHA-256，并保留逐页 TTL/缓存范围。缺失或负 TTL 按零处理，缺失范围按私有处理，新鲜期最多 24 小时。**不复用工具列表缓存**：每次调用重新分页发现，不跨授权上下文共享、不轮询。`CallToolPinned` 可在发送前核对调用者审批的 schema 摘要，但它本身既不验证服务端身份也不授权，更不能保证恶意服务端在发现后保持行为不变。

Input JSON must be an object; malformed JSON, duplicate keys, unavailable tools and schema violations fail **before tools/call**. Validation uses pinned `jsonschema/v6` with a restricted 2020-12/default or explicit draft-07 profile: at most 64 KiB/schema, depth 16, 512 values, 32 local references and a composition branch budget of 64. External references (including local files), dynamic/recursive references, and reference-plus-composition schemas are rejected; Go regular expressions are used. Unsupported dialects/profile constructs fail closed, not permissively. Output schemas, when provided, validate `structuredContent`; tool `isError` is distinct from RPC/transport failure. Numeric values are kept exact but restricted to tokens of at most 128 bytes and decimal exponents in [-308, 308]; schema length/item/property/contains count constraints must be integers in [0, 2147483647] to prevent dependency integer overflow. Unsupported input/schema values are preflight failures, and unsupported numbers in a recognized tool response remain known-response failures. Dependency compile/validation panics are contained as sanitized errors, without an abandoned worker. Local references must resolve to checked schema positions via JSON Pointer; hidden/annotation targets, resource IDs/anchors, cycles and reference graphs exceeding 1,024 expanded schema visits are rejected. This is not full MCP/JSON Schema conformance or an SDK compatibility claim.

输入必须为 JSON 对象；格式错误、重复键、未知工具、schema 不符均在 `tools/call` 前拒绝。使用固定版本 `jsonschema/v6` 校验，默认 2020-12 或显式 draft-07 的受限配置：每个 schema 最多 64 KiB、深度 16、512 个值、32 个局部引用，组合分支预算 64。拒绝外部引用（含文件）、动态/递归引用，以及同时包含引用与组合的 schema；正则使用 Go 语法。不支持时明确失败，不放宽校验。输出 schema 校验 `structuredContent`；`isError` 与 RPC/传输失败分开处理。数值保留精确十进制表示，但数字文本最多 128 字节、指数仅允许 [-308,308]；schema 的长度/元素/属性/contains 数量约束只允许 [0,2147483647] 内整数，避免依赖内部整数溢出。输入/schema 越界在预检阶段拒绝，已识别工具响应中的越界数字仍归类为已知响应失败。依赖编译/校验 panic 转为脱敏错误，不遗留后台校验工作。局部引用仅允许通过 JSON Pointer 指向已检查的 schema 位置；隐藏/注释目标、资源 ID/锚点、循环和展开超过 1,024 次 schema 访问的引用图均拒绝。此阶段不宣称完整 MCP/JSON Schema 或 SDK 兼容。

Request IDs are typed string IDs with a bounded pending map; concurrent replies may arrive out of order. Defaults: 1 MiB frames, 32 pending requests, 32 list pages, 1,000 tools, 8 MiB aggregate list bytes; notification delivery is a 32-item lossy channel. Limits have hard upper bounds. Progress never extends deadlines. Requests default to 30 seconds and the discovery probe to one second; per-call contexts can shorten them. `Close` closes pipes, kills and waits for the **direct process**, and joins its transport workers. Only Linux native fixture behavior was exercised; other OS cross-compilation does not establish native pipe/termination behavior or all-descendant cleanup.

请求使用字符串 ID、有界待响应表，允许并发乱序响应。默认上限：单帧 1 MiB、32 个待响应请求、32 页、1,000 个工具、列表累计 8 MiB；通知队列为最多 32 项的有损通道。进度通知不延长期限；请求默认 30 秒、探测默认 1 秒，调用上下文可缩短。`Close` 关闭管道、终止并等待**直接子进程**，回收传输工作协程。仅 Linux 原生 fixture 已实测；其他平台交叉编译不代表原生管道/终止行为或全部后代进程清理验证。

`CallResult` and `CallError` carry `not_sent`, `possibly_sent`, or `known_response`. Queued cancellation is not sent; cancellation/EOF/timeout after a possible write is uncertain. Cancellation notification is only intent, never proof the server stopped; late replies cannot rewrite a terminal request. A known response, including tool error or RPC error, does **not** prove rollback. There is no tool-call retry/reconnect/continuation. The caller owns durable effects and reconciliation outside transport. Legacy workflow MCP nodes always acquire a durable claim (deriving a stable workflow/node key if omitted); unknown outcomes and evidence/checkpoint/completion-write failures are non-retryable. Claims remain conservative even after preflight failure; do not erase them to retry without review. If evidence storage fails after a response, the observation remains in the result but is not falsely marked persisted. A direct caller bypassing the workflow must supply its own durable exclusion and authorization.

返回结果/错误均携带 `not_sent`、`possibly_sent` 或 `known_response`。排队时取消属于未发送；可能写入后发生取消、EOF 或超时属于未知。取消通知只是意图，不证明服务端停止；迟到响应不会改写终态。已知响应（含工具/RPC 错误）不代表副作用回滚。传输不重试、重连重发或自动续传工具；持久副作用与核对由调用者负责。旧 workflow 的 MCP 节点始终先获取持久 claim（缺失 key 时按 workflow/node 生成），未知结果及 evidence/checkpoint/completion 落盘失败均不可自动重试。即使预检失败，claim 也保守保留，不能未经核对删除后重试。响应后证据落盘失败会保留返回观察，但不伪称已保存；绕过 workflow 直接调用时须自行提供持久排他与授权。

Local, owned-process tests only (never point tests at a real configured server):

```sh
go test ./internal/mcp ./internal/runtime ./internal/cli
go test -race ./internal/mcp ./internal/runtime ./internal/cli
go test ./...
```


### Explicit session context operations / 显式会话上下文操作

Web and Ink share `/branch RUN_ID` (or `/branch latest`), `/rewind conversation RUN_ID`, `/clear confirm`, `/compact`, `/compact view`, and `/compact original`. Branch/rewind switch into a new conversation through the named run, not a Git branch or filesystem rollback. Run IDs are displayed beside inputs. The source transcript is unchanged; parent, boundary and source cursor are recorded. `/clear` first explains that historical inputs will no longer enter the new context; `/clear confirm` creates that empty context without deleting history. `/resume SESSION_ID` only reopens history; continuing an unfinished run remains a separate action.

Branches retain exact immutable workspace/project/model/privacy/permission snapshots, not executable runs, approvals or MCP processes. Active/unresolved sessions and stale cursors block context mutation. MCP inheritance is disconnected, not a new launch; explicitly create a new MCP-opted-in session if needed. Duplicate requests with the same request ID return the same result across restart; clients never automatically retry writes.

Compaction is bounded local `local-omit-resolved-v1`: it omits resolved assistant/tool message bundles and inserts an explicitly untrusted count/provenance record. **It is not semantic summarization or model-based compression.** Every admitted original user input, including failed/cancelled inputs, and immutable user/system policy remain exact. Original transcript snapshots remain durable. `/compact view` inspects original and selected views; `/compact original` restores original context selection. New inputs remain protected. `/context` reports the existing conservative serialized-byte estimate, not provider billing; mandatory authority exceeding budget still blocks. Snapshot creation is limited to 4 MiB. Guarded native file undo is documented below. Commands, MCP and external effects are not reversible through conversation rewind.

网页与 Ink 共用 `/branch 运行ID`（或 `/branch latest`）、`/rewind conversation 运行ID`、`/clear confirm`、`/compact`、`/compact view`、`/compact original`。分支/对话回退以明确运行边界创建并切换新会话，不是 Git 分支，也不回滚文件。输入旁显示运行 ID，原历史不变，保存父会话、分支点和来源游标。`/clear` 先说明不再将旧输入放入新上下文，`/clear confirm` 确认新建空上下文，不删除历史。`/resume 会话ID` 只读恢复历史，不会继续未完成运行。

分支保留原有工作区、项目、模型、隐私与权限的不可变快照，不复制可执行运行、审批或 MCP 进程。活动/待核对会话和过期游标拒绝上下文变更。继承的 MCP 显示断开，需要时明确新建启用 MCP 的会话，不自动启动。相同请求 ID 重复操作及重启后重试返回原结果；客户端不自动重试写入。

压缩方法为有界本地 `local-omit-resolved-v1`，省略已完成的 assistant/tool 数据轮次，添加明确标记为不可信的数量和来源记录，**不是语义摘要或模型压缩**。所有已接收的原始用户输入（含失败/取消输入）及不可变 system/user 策略保持原文。原始快照持久保留，`/compact view` 对照原文/选中上下文，`/compact original` 恢复原文选择，新输入仍受保护。`/context` 显示原有保守序列化字节估算，不冒充实际计费；必要权限内容超预算仍拒绝执行。快照创建上限 4 MiB。**原生文件撤销见下节**；命令、MCP 与外部副作用不因对话回退而撤销。

```sh
node --test apps/web/test/session_ops.test.ts
python3 apps/terminal/test/session_ops_pty.py
```


#### Recovering uncertain context operations / 恢复结果未知的上下文操作

During branch/clear/rewind/compaction, both clients block message submission and navigation while preserving typed text. A confirmed child is selected even if the following session-list refresh fails. Unknown outcomes retain the original source, boundary, cursor, kind and request ID; repeating the original command cannot create another request. The Web stores only this recovery metadata in tab-scoped session storage (never tokens or drafts). Ink prints a copyable `/recover SOURCE_SESSION REQUEST_ID` command for a later terminal restart.

`/recover SOURCE_SESSION REQUEST_ID`, `/sync`, and Web’s read-only context-operation recovery button consult the authenticated Go operation ledger and never replay a write. Missing evidence keeps the operation locked: it is not proof of failure. If needed, `/retry-context confirm` explicitly resends the **retained exact intent with the same request ID**, never a new UUID; this is not automatic retry. A restarted terminal can recover committed operations by the printed identifiers; if no record exists and it did not retain the exact intent, it cannot invent or retry that intent. Operations committed before this recovery migration have no exact-intent lookup record. Immutable original history remains available. Recovery selects the recorded child for branch/clear/rewind, or the source session for compact/original; an already-open matching target is acknowledged without waiting for another mount.

分支、清空、对话回退或压缩进行中，两端阻止消息提交及导航，保留正在输入的文字。即使后续会话列表刷新失败，已确认的子会话也会被选中。未知结果保留原始来源、分支点、游标、操作种类及请求 ID，重复输入原命令不会新建请求。网页仅在当前标签页的 session storage 保存恢复元数据，不保存令牌或草稿；Ink 显示可复制的 `/recover 来源会话ID 请求ID`，供重启终端后只读核对。

`/recover 来源会话ID 请求ID`、`/sync` 及网页“核对上下文操作（只读）”只读取 Go 操作记录，不重放写入。记录缺失不等于失败，仍保持锁定。必要时明确输入 `/retry-context confirm`，仅重发**保留的完整原意及同一请求 ID**，不会生成新 UUID，也不自动重试。终端重启后可凭打印的标识恢复已提交操作；若记录不存在且未保留完整原意，不会猜测或重试。恢复迁移前已提交的旧操作不补造原意记录。原始历史仍保留。恢复分支/清空/回退时选中记录的子会话，恢复压缩或原文选择时选中来源会话；目标已打开时按匹配 ID 完成确认，不等待重复挂载。


### Guarded native file undo / 受保护的原生文件撤销

Web and Ink share `/rewind files` for **read-only** checkpoint discovery and `/rewind files CHECKPOINT_ID` to request a **new action run and fresh approval**. This is separate from `/rewind conversation RUN_ID`. Checkpoints come only from successful, completed native `apply_patch` effect journals, bound to source session/run/proposal, scope, root identity, path and exact pre/postimage SHA256. Pending, denied, refused, unknown, unchanged and inverse operations are not offered as source checkpoints. Exec, MCP and external effects are not reversible. Session branches do not inherit file-undo authority.

The approval shows expected current bytes, proposed restored bytes, hashes, source checkpoint/run, root and limitations. **Even `acceptEdits` asks.** Plan mode, chat-only, unsupported native platforms and changed roots cannot undo. The inverse is Go-owned and deterministic, with no model call, generated answer or invented usage. It uses the same protected existing-file replacement, consumed approval, durable effect journal and offline recovery as ordinary edits. No file creation/deletion, Git checkout or backup overwrite is performed.

Changed current bytes refuse without overwriting. A durable native per-path head also rejects later native operations, even if their resulting bytes happen to match. One source checkpoint reserves **one** undo action; duplicate creation reuses that action, and denial, refusal or cancellation does not grant a new attempt with a new ID. Unknown effects cannot rerun. Inventory dispositions distinguish available, superseded, reserved, refused, closed-without-undo, unknown and undone. Legacy journals without a native head are eligible only when no other native invocation exists for that scope/path; ambiguous old histories fail closed. External writers can still race the final check and atomic rename, and external modify-then-restore-to-identical-bytes histories are not tracked. This is not filesystem CAS or an OS sandbox.

Unknown creation responses use the existing shared quarantine and `/recover SOURCE_SESSION REQUEST_ID` (or `/sync`) **GET-only** recovery. Web retains identity across tab reload; Ink prints recovery IDs. Only `/retry-context confirm` can explicitly resend the retained exact original intent and ID. Restoring/mounting/recovering never POSTs. The new approval is still required after recovering the action. The scoped authenticated v1 API adds `GET /sessions/{id}/edit-checkpoints` and `POST /sessions/{id}/file-undo` (`request_id`, `checkpoint_id`, `expected_cursor`); the existing operation-recovery record includes `kind: file_undo` and `action_run_id`. Inventory is bounded to 1,000 proposals / 4 MiB of file bytes and refuses excess rather than silently truncating. Durable state stays in the same SQLite runtime; migration 0020 adds one-shot reservations and native file heads.

网页与 Ink 共用 `/rewind files` **只读列出检查点**；`/rewind files 检查点ID` 新建独立动作运行并请求**全新精确审批**，不等同于 `/rewind conversation 运行ID`。仅成功且日志已完成的原生 `apply_patch` 副作用可成为来源检查点，并绑定原会话、运行、提案、工作区范围、根目录身份、路径及前后内容 SHA256。等待、拒绝、失败、未知、内容不变及逆向撤销操作不会列为可撤销来源。命令、MCP、外部副作用不支持撤销；会话分支不会继承文件撤销权限。

审批显示预期当前内容、拟恢复内容、哈希、来源检查点/运行、根目录及限制；**`acceptEdits` 也必须审批**。计划模式、纯聊天、不支持原生文件适配的平台及根目录变化均拒绝。Go 确定性生成逆向操作，不调用模型，不伪造回答或用量；复用原有受保护替换、一次性审批、持久副作用日志及离线恢复。不新建/删除文件，不做 Git 回滚或盲目恢复备份。

当前内容不匹配就拒绝覆盖；持久原生文件操作指针还会阻止后续原生操作后使用旧检查点，即使字节相同。每个来源检查点只预留一次撤销动作，重复请求返回同一动作；拒绝、失败、取消后也不允许换 ID 重新获得权限。未知副作用不自动重跑。旧日志若没有文件操作指针，仅在同范围/路径不存在其他原生调用时可用；有歧义则拒绝。外部写入者仍可能在最后检查与原子重命名间竞争，外部修改后恢复完全相同字节的历史无法追踪，不保证文件系统 CAS 或 OS 沙箱。

创建结果未知时，两端复用上下文操作的锁定/恢复机制：`/recover 来源会话ID 请求ID`、`/sync` 只读核对；网页保留跨标签页重载的恢复标识，Ink 显示可复制标识。只有明确执行 `/retry-context confirm` 才重发保留的完整原意和同一个 ID，不自动重试。恢复动作后仍需新的精确审批。API 新增同会话 `/edit-checkpoints` GET 与 `/file-undo` POST（`request_id`、`checkpoint_id`、`expected_cursor`）；操作恢复记录增加 `file_undo` 和 `action_run_id`。清单上限 1,000 个提案 / 4 MiB 文件字节，超出即拒绝，不暗中截断。仍使用同一 SQLite 运行库，0020 迁移保存一次性预留与原生文件操作指针。

### Explicit durable input FIFO / 显式持久输入队列

Busy input stays a draft. Only Web’s **加入队列** button or `/queue add TEXT` explicitly enqueues another turn. This does not steer or mutate the current prompt. `/queue` (or `/queue list`) inspects order, original text, policy digest, state, blocking reason and admitted run ID. `/queue withdraw ITEM_ID` removes **only a pending** item. `/cancel` / Stop targets the active run, not pending items: the next eligible queued turn may start after cancellation completes. Close/disconnect the UI without cancelling and the server continues its owned work.

The FIFO holds at most 16 pending items per session and 128 across the service, each at most 64 KiB. Overflow is rejected before persistence. Inspection shows every pending item and the latest 50 admitted/withdrawn records; older committed operations remain recoverable by request ID and original run history remains accessible. Each item preserves exact content and immutable original model/provider/privacy/mode/workspace/MCP-generation policy. Context changes and ordinary sends cannot jump ahead of pending queue items. Queue admissions share existing Go worker limits, run budgets, approval bindings and effect journal. No additional workflow scheduler or implicit approval exists.

An item becomes `admitted` atomically with its sole run and `run.created` event. `admitted` is not proof of execution: its separate phase shows queued/model/waiting_approval/cancelling/terminal or `unknown_admission` when no execution checkpoint is available. Running, uncertain or reconciliation-required predecessors block later items. Restart can admit never-claimed pending items after safe terminal history, but never automatically reruns an already-admitted run. Provider/scope/MCP revocation is sticky: inspect and withdraw the old item, then give new explicit intent under an appropriate session. Restoring a setting does not silently reauthorize it.

Unknown enqueue/withdraw responses retain the original request identity and lock repeat submission/navigation. `/queue recover [SOURCE_SESSION REQUEST_ID]`, `/sync`, and the Web read-only queue recovery button consult the durable operation ledger, never POST. Absence is not failure evidence. `/queue retry confirm` alone resends the **retained exact payload and original ID**. Browser session storage keeps only recovery identifiers, never draft text/tokens; Ink prints them for restart. After reload/restart, an unrecorded operation whose exact payload was lost cannot be invented or retried. Local queue actions and run transitions refresh inspection; use `/queue` to refresh pending changes from another client.

网页和 Ink 在忙碌时默认保留草稿；只有明确点击**加入队列**或输入 `/queue add 文本` 才把下一轮放入持久 FIFO，不修改当前提示词，也不是实时插话。`/queue`（或 `/queue list`）查看顺序、原文、策略摘要、状态、阻塞原因及已接收的运行 ID；`/queue withdraw 条目ID` 仅撤回尚未接收的条目。`/cancel` 和“停止”只停止当前运行，不清空队列；取消安全完成后下一条可能立即运行。关闭网页或退出终端不等于取消服务端工作。

每会话最多 16 条待接收输入，全服务最多 128 条，每条最多 64 KiB，超限在保存前拒绝。查看包含全部待接收项和最近 50 条已接收/撤回记录；更早操作仍可按请求 ID 核对，原运行历史保留。原文和模型、服务商、隐私、权限模式、工作区、MCP 进程世代策略均绑定原意；上下文变更及普通发送不能越过待接收项。复用 Go 原有唯一工作协程、预算、审批和副作用日志，没有第二套调度器或隐式批准。

条目 `admitted`、唯一运行及创建事件同事务保存，不代表已经执行；另显示运行阶段，缺少执行检查点时明确显示 `unknown_admission`。前序仍运行、结果未知或待核对都会阻塞后续。服务重启可接收从未认领且前序安全结束的条目，但不自动重跑已接收运行。服务商、工作区或 MCP 世代失效后保持阻塞，需要撤回并明确提供新意图，不因设置恢复自动重新授权。

排队/撤回响应未知时保留原请求 ID 并锁定重复提交及导航。`/queue recover [来源会话ID 请求ID]`、`/sync` 和“核对队列操作（只读）”只查持久记录；查不到不证明失败。只有 `/queue retry confirm` 明确重发当前内存保留的完整原文和同一个请求 ID。网页 session storage 仅存恢复标识，不存草稿或令牌；Ink 打印标识供重启核对。重启后若记录不存在且原文已丢失，不猜测重试。自身操作及运行阶段会刷新显示，其他客户端更改待处理队列后可用 `/queue` 手动只读刷新。

Authenticated v1 API: `POST /sessions/{id}/queue` (`request_id`, `content`); `POST /sessions/{id}/queue/{item}/withdraw` (`request_id`); `GET /sessions/{id}/queue`; `GET /sessions/{id}/queue-operations/{request_id}`. Shared SDK: `enqueue`, `withdrawQueue`, `queue`, `queueOperation`. Migration 0021 stores queue/operation state in the same `<runtime-parent>/runtime-rebuild-v1/agent.db`. Default-off exec/MCP and exact approvals are unchanged.

### Independent bounded Jobs / 独立有界后台 Job

Use `/jobs add TEXT` explicitly to create a **read-only** Job. It can run while its origin conversation is busy. `/jobs edit TEXT` explicitly requests native filesystem effects within the parent's existing authority, but every edit requires a **new exact approval**, even when the parent uses `acceptEdits`. Plan/chat-only parents cannot create effectful Jobs. A Job has an immutable origin session and optional verified origin run, its own empty execution conversation, stable Job + Run IDs, and a separate reduced permission snapshot. It does not inherit transcript messages, approvals, executable checkpoints, process claims, exec permission or a live MCP generation. Put necessary task evidence in its explicit input. New ordinary/queued turns cannot receive Job output as prompt input.

Both Web and Ink support `/jobs` or `/jobs list`, `/jobs show JOB_ID` (result, scoped tool logs and exact review), `/jobs stop JOB_ID`, `/jobs approve JOB_ID` / `/jobs deny JOB_ID` **after viewing that Job's exact review**, and `/jobs origin`. `/jobs artifact JOB_ID ARTIFACT_ID` reads a native artifact from that execution session, never another Job/origin. Text is displayed as untrusted data; binary content is explicit base64. Artifacts larger than 256 KiB are explicitly unavailable in this view. Web also has Job-specific read/show/stop/approval controls. The Job panel belongs to the immutable origin; viewing/returning does not change conversation context. Inspection and UI exit do not launch or cancel work. `/cancel` still targets the ordinary conversation run, not Jobs. A sequential one-second read-only observer refreshes already-known unresolved Jobs while their origin UI is mounted, so completion/status (and an open Job result view) updates without changing the origin transcript. It stops on UI exit/navigation or authentication/protocol failure, backs off transient read failures to 30 seconds, and never starts work. Use `/jobs` or the read-only button to discover other clients' new Jobs. No notification or prompt injection is sent.

| Job adapter | Availability / authority |
| --- | --- |
| Chat / model output | Same immutable model/provider/privacy policy; isolated input |
| Native `read`, `search` | Supported Linux workspace only, default plan/read-only intersection |
| Native `apply_patch` | `/jobs edit` only, parent non-plan filesystem authority, new exact Job + Run approval |
| Exec / MCP / browser / external services | **Unavailable for Jobs**; no implicit opt-in, server start or live-generation transfer |
| Legacy `--long` | Separate registered-only behavior; not a Job executor |
| Noninteractive `--print` | Ordinary bounded run only; slash-like text does not create background Jobs |

At most **8 unresolved Jobs per origin, 32 per service, 2 executing Jobs** inside the existing four-worker Go service, leaving ordinary work capacity. Normal stop/control requests use the existing bounded drain path. Input is at most 64 KiB. Each execution has at most 16 steps, 2 minutes, 32,000 total tokens, 2,048 output tokens, 128 KiB model output, 32 KiB per tool result and 512 events. Approval waiting consumes the persisted deadline but no executor slot. Inspection includes every unresolved Job plus the latest 50 records; operation identities remain durable. Artifacts/tool logs use the existing execution-session scope and engine budgets, not the origin's prompt.

`registered` means not admitted; `admitted` means a run was checkpointed but not yet observed executing. `running` requires an actual engine owner. `waiting_approval`, `cancelling`, `stopped`, `completed`, `failed` and `unknown` are distinct. Admission and the stable run creation commit atomically. Only never-admitted registered intent may start on restart. Missing checkpoints, interrupted already-started model work, committed approval without a resumed owner, and uncertain effects are not automatically retried. Offline recovery can finish a fully checkpointed result or preserve a genuinely pending exact approval for an explicit decision. Unknown effects block further execution; there is **no Job resume/retry-execution command or automatic repair**. Stop records intent and signals the real owner; unknown effects are never reported as cleaned up. Parent policy/root/provider or persisted adapter/budget-policy revocation is sticky; inspection never reauthorizes it. Upgrading the earlier 0022 implementation checkpoint preserves identities but blocks unresolved Jobs without a persisted execution-budget policy; never-admitted legacy Jobs may be stopped and recreated explicitly, while started/unknown work requires reconciliation, not replay.

Creation, stop and decisions use retained request identities and a transactional ledger. Unknown responses lock new mutations/navigation. `/jobs recover [ORIGIN_SESSION REQUEST_ID]` and the Web recovery button are read-only. `/jobs retry confirm` resends only the original frozen in-memory payload and ID, never inventing a new execution. Reload retains recovery identifiers only; an absent ledger entry with a lost payload stays unresolved. Jobs are not copied into ordinary session lists, and their execution sessions reject ordinary messages/queue/context writes and generic run controls.

Authenticated versioned API: `POST /sessions/{origin}/jobs` (`request_id`, `content`, optional `effects`, `origin_run_id`), `POST /sessions/{origin}/jobs/{job}/stop`, `POST /sessions/{origin}/jobs/{job}/decision` (`request_id`, `approval_id`, `digest`, `decision`); `GET /sessions/{origin}/jobs`, `GET /sessions/{origin}/jobs/{job}`, `GET /sessions/{origin}/job-operations/{request_id}`, `GET /sessions/{origin}/jobs/{job}/artifacts/{artifact_id}`. The existing approval-review endpoint resolves the **executing Job session**, not its parent. SDK methods: `createJob`, `jobs`, `job`, `stopJob`, `decideJob`, `jobOperation`, `jobArtifact`. Migrations 0022–0023 use the same private `<runtime-parent>/runtime-rebuild-v1/agent.db`.

明确输入 `/jobs add 文本` 才创建默认**只读**后台 Job；来源会话忙碌时也可独立运行。`/jobs edit 文本` 明确请求原生文件编辑，但权限必须在父会话范围内，且每次编辑都需新的精确审批，父会话的 `acceptEdits` 不会继承为免审批授权。plan、chat-only 来源不能创建可写 Job。Job 保存不可变来源会话/可选已核验运行 ID，使用独立空对话、固定 Job + Run 标识和收窄的权限快照。不复制历史消息、审批、执行检查点、进程所有权、exec 授权或 MCP 活跃世代；所需证据请明确写入任务文本。结果不会进入来源会话的新提示词。

网页和 Ink 均支持 `/jobs list`、`/jobs show JobID`（结果、工具记录、精确审批）、`/jobs stop JobID`、查看后 `/jobs approve JobID` / `/jobs deny JobID`、`/jobs origin`。用 `/jobs artifact JobID 产物ID` 只读查看该独立执行会话的产物，不能跨 Job/来源访问；文本按不可信数据展示，二进制明确显示为 base64，超过 256 KiB 的产物在此视图明确不可用。网页另有对应按钮。Job 面板保留在原来源下，返回原会话不改变其上下文；查看、刷新及退出 UI 不触发执行/取消。普通 `/cancel` 不取消 Job。来源 UI 打开时以每秒一次的串行只读查询观察已知未解决 Job，自动更新完成状态及已打开的结果视图，不修改来源记录；退出/导航即停止观察，从不启动工作。用 `/jobs` 或只读按钮发现其他客户端新建的 Job；不发送通知、不自动把结果插入提示词。

能力范围：模型输出保留原模型/服务商/隐私约束；Linux 原生 `read`、`search` 默认只读；`apply_patch` 仅限明确 `/jobs edit` 和全新精确审批。**Job 的 exec、MCP、浏览器及外部服务适配器不可用**，不隐式启动进程或转移能力。旧 `--long` 不是 Job 执行器；`--print` 仍只执行普通运行，不把命令样式文本解释为后台任务。

每来源最多 8 个未解决 Job，全服务 32 个，现有 4 个 Go 执行槽中最多 2 个运行 Job，为普通会话保留容量。每次执行限制 16 步、2 分钟、32,000 总 token、2,048 输出 token、128 KiB 模型输出、每工具结果 32 KiB、512 事件；输入 64 KiB。审批等待不占执行槽但计入持久截止时间。显示全部未解决项及最近 50 条，操作身份持续保存。工具日志/产物仍归独立执行会话及预算约束。

状态区分未接收 `registered`、已接收 `admitted`、真实 owner 执行中 `running`、等待审批、取消中、已停止、已完成、失败及未知。接收与唯一运行创建同事务提交。重启只可启动从未接收的明确意图；已启动但缺少检查点、模型中断、审批已提交但尚未继续、或副作用未知时不重放。离线恢复可完成已有完整结果，或保留仍待决定的精确审批。**没有 Job 执行恢复/重试命令或自动修复**；未知副作用阻断继续，不假报已清理。来源权限/根目录/服务商或持久适配器/预算策略失效后保持撤销，不因查看重新授权。0023 升级保留早期 0022 检查点的身份，但缺少执行预算策略的未解决 Job 会被阻塞，从未接收的旧任务可停止后明确重建；已启动/未知任务必须核对，不能重放。

创建、停止、审批均使用原请求 ID 和事务账本。响应未知时锁定新变更/导航；`/jobs recover [来源会话ID 请求ID]` 及网页核对按钮仅查询。只有 `/jobs retry confirm` 重发当前内存保留的完整原请求和同一个 ID。重载只保留恢复标识；账本不存在且原文丢失时保持未解决。独立执行会话不混入普通历史清单，也拒绝普通消息、队列、上下文变更及通用运行控制。API/SDK 与 0022–0023 存储位置见上文；审批详情始终按实际执行 Job 会话读取，不借用父会话授权。

### Operator inventory and exact metadata actions / 管理目录与精确元数据操作

Open **Management / 管理** in Web, or `/manage projects` in Ink. Web uses same-origin `#manage/RESOURCE` navigation, with browser Back/Forward and Escape/Close returning to the mounted live conversation. Ink **Ctrl+O** opens management without consuming a draft; Escape/Ctrl+O closes, Tab changes section, left/right arrows navigate history and `n` reads the next page. `/manage RESOURCE [OWNER_ID]` selects a specific inventory; `/manage back|forward|next|close` are also available from the chat command line. Chat streams, exact approvals, context-operation recovery, queue and selected Job state remain owned by the same controller. Management does not resolve uncertain writes or weaken their navigation fences.

The shared `AgentClient.operator(sessionID, resource, {owner, filter, cursor, limit, signal})` calls **GET `/api/v1/sessions/{id}/operator/{resource}`** under the existing loopback, exact Host/Origin, mandatory bearer, CSP/no-store envelope. All reads use the already locked rebuilt runtime DB. A page has at most 50 records (25 by default), fixed 512-character metadata projections (project labels preserve complete text up to 2,048 UTF-8 bytes; oversized labels are unavailable) and a two-second query deadline. Opaque cursors bind session/resource/owner/filter; state filters are exact allowlisted values. Child inventories require an existing typed owner. Cursors are live keyset pagination, not a transactionally frozen export. No arbitrary DB/root/path, file download, raw config, environment, manifest, provider error or document-body route is exposed.

| Inventory / 目录 | Available reads / 可读内容 | Remaining management actions / 后续写入能力 |
|---|---|---|
| `tasks`, `workflows` | Business metadata; `evidence` by task owner, `nodes` / `checkpoints` by workflow owner | Create, admit, run, resume, cancel via a proved workflow execution bridge |
| `projects` | Project metadata and explicit execution classification; current immutable ScopeID is separate | Metadata-only create plus rename/archive/restore are available (7E5B); general policy update/use/session association remain unavailable / 可创建仅元数据项目及改名、归档、恢复；策略更新与会话关联未开放 |
| `knowledge` | Scoped managed inventory and explicitly invoked local BM25 with real current-revision provenance | Explicit text/safe relative file ingestion, reversible deactivation and reindex via exact preview/confirmation (7E3B below). Cross-scope assign, remote fetching, full-text export and permanent deletion unavailable |
| `models` | Configured/resolved **startup snapshot**, configured chat/tool flags, synthetic capability evidence distinct from live health; current admission | Configure and explicit probe, never automatic view-time probing |
| `skills`, `versions`, `mcp` (legacy read-only management) | Legacy workflow-manifest inventory/version checksum; already-known MCP process/tool-count snapshots. Distinct from rebuilt `/sessions/{id}/skills`. | Any skill execution/evaluation; MCP configuration/reconnection admission |
| `triggers`, `trigger-state`, `trigger-history`, `deadletters` | Stored definitions/state/history/dead letters, with typed trigger owner for state/history | Create/update/enable/disable/manual run, dead-letter retry/delete; proven single execution owner required |
| `notifications` | In-app title/status metadata, **not external delivery** | Mark-read is available below; delete unavailable; external delivery adapters unsupported |
| `permissions`, `approvals`, `audit` | Immutable current session mode/scope, policy-constraint counts, session-only pending approvals; legacy audit metadata with explicit incomplete coverage | No in-place session policy widening; the exact metadata actions (including 7E5B creation) and 7E3B knowledge operations have atomic action/audit admission. Existing chat/Job approvals stay in their exact-review flow |
| `diagnostics` | OS, native capability reasons, schema-at-startup attestation and storage row counts | Integrity check, safe snapshot backup/export, offline import/restore, retention/vacuum; no GET write tests or watcher scans |

Lists are **not CRUD/execution parity**. Business tasks/workflows, interaction runs, queued inputs and Jobs remain distinct; only stored provenance is displayed. Opening views never launches MCP, providers, browsers, workflows or filesystem probes. Business emptiness means **“runtime-rebuild-v1: legacy data not imported”**, not that historical records never existed. Knowledge is limited to the selected session's immutable workspace scope, so chat-only has no knowledge access. Project catalog entries cannot grant workspace authority, and empty restrictive permissions do not become allow-all. External browser/coding/notification adapters and unvalidated native macOS/Windows file execution remain explicitly unsupported; default-off NON-SANDBOXED exec is unchanged. Legacy CLI commands remain migration-period entrypoints, not hidden HTTP adapters or safe rebuilt mutations.

Read-only noninteractive output requires an existing session and does not create one or submit a message:

```sh
PA_AGENT_TOKEN=... pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx \
  --url http://127.0.0.1:7319 --session SESSION_ID --manage projects --format json
# Further pages/children: --cursor CURSOR, --owner OWNER_ID, --filter STATE
```

网页点击 **Management / 管理**；Ink 输入 `/manage projects`，或用 **Ctrl+O** 保留当前草稿直接打开。网页支持浏览器前进/后退、Esc/关闭返回；终端用 Tab 切换目录、左右方向键浏览历史、`n` 下一页、Esc/Ctrl+O 返回。聊天流、审批、上下文恢复、队列和已选 Job 不会因切换视图被销毁或重放。`/manage RESOURCE [OWNER_ID]` 与非交互 `--session ID --manage RESOURCE --format json` 只读取，不新建会话或发起运行。

API 始终使用已加锁的 `runtime-rebuild-v1/agent.db` 和原有认证安全边界。每页默认 25、最多 50 条；元数据字段在 SQLite 内截断至 512 字符，查询限时两秒；游标绑定会话、资源、所属记录及状态筛选。知识仅显示当前不可变工作区拥有且修订匹配的元数据，聊天模式无此权限。项目目录不等于工作区授权；空限制集合不会被误解为全部允许。标题作为不可信文本显示，不加载外部资源。

上表明确区分已实现读取和待实现管理操作，**列表不代表完整 CRUD 或执行能力**。模型列表是启动时配置/解析/合成验证快照，不证明当前网络健康；MCP 发现不代表调用；触发器启用记录不代表调度器运行；通知记录不代表邮件/Slack/推送送达。业务表为空明确提示“旧数据尚未导入”，不声称历史记录不存在。此阶段不暴露原始配置、密钥环境、错误正文、文档正文、任意文件下载或旧 HTTP handler。除下述两个精确元数据动作和 7E3B 知识操作外，其余写操作、备份/恢复、外部适配器及原生平台验证仍按上表后续完成。

Operator drilldowns preserve ordinary dotted IDs such as `local.lookup` exactly (`/manage versions local.lookup`). Web and terminal share display-only removal of terminal escapes and Unicode formatting controls, including bidi controls in record IDs; DTOs, JSON output and request owner identities are not rewritten.

管理目录支持 `local.lookup` 等普通点分 ID，并原样传递所属记录标识（如 `/manage versions local.lookup`）。网页和终端共用仅用于显示的清理规则，移除终端转义及 Unicode 格式控制符，包括 ID 中的双向文字控制符；不会改写 DTO、JSON 输出或请求中的原始身份。


#### Exact metadata preview and confirmation / 精确元数据预览与确认

The original metadata operations change **one existing project's display name or catalog state** (`project.rename`, `project.archive`, `project.restore`) and **one in-app notification's read state** (`notification.mark-read`). Metadata-only `project.create` is also available as described in 7E5B below. Inventory navigation and refresh remain GET-only. Web offers Preview rename / archive / restore / mark read; Ink supports `/project archive ID`, `/project restore ID`, `/op rename PROJECT_ID NEW NAME` and `/op read NOTIFICATION_ID`. These prepare durable previews, not execution. Inspect full exact old/new values, target, immutable originating session/scope/policy, revision, digest and ten-minute expiry before explicit confirmation. Formatting controls are shown as visible escapes in the exact preview, never silently substituted in request identities.

Use Web's confirm/cancel buttons, Ink `y`/`n`, or `/op confirm` / `/op cancel`. A single SQLite transaction rechecks authority and prior state, applies only the display name, catalog active/archived state, or unread→read plus read timestamp, and commits the operation result and audit together. Already-read, identical-name, or already-requested catalog-state requests are explicit `no_op`; unsupported notification states are rejected. No model turn, fake usage, generic permission expansion, tool approval, external notification or workflow execution is created. Cancelling a preview does not undo a previously committed action.

`pending`, `committed`, `no_op`, `rejected`, and `expired` are durable/effective operation states. Transport `unknown` is not failure: keep original identifiers and use `/op recover [SESSION_ID REQUEST_ID]`, Web's read-only recovery, or CLI lookup. Never resend with a new ID. Pending/unknown metadata actions quarantine conflicting mutations and navigation while keeping the live chat/draft; Ink retains keyboard focus on known results so repeated `y` cannot approve an unrelated tool. Enter/Escape or `/op dismiss` closes a known result. Browser reload retains only three recovery identifiers in session storage, not tokens, names or drafts. Failed persistence warns to retain those IDs manually. A known commit remains known even if inventory refresh fails. An absent lookup record after transport loss stays unresolved.

```sh
# Explicit stable IDs are mandatory. Preview does not modify the project.
PA_AGENT_TOKEN=... pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx \
  --session SESSION --op rename --target PROJECT --new-name 'Exact new name' \
  --request-id PREVIEW_ID --format json
# Copy operation_id and digest from the exact reviewed preview.
PA_AGENT_TOKEN=... pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx \
  --session SESSION --op confirm --operation-id PREVIEW_ID --digest DIGEST \
  --request-id DECISION_ID --format json
# GET-only recovery. Use --op cancel instead of confirm to reject a pending preview.
PA_AGENT_TOKEN=... pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx \
  --session SESSION --op lookup --request-id DECISION_ID --format json
```

CLI requires an existing session and cannot mix metadata operations with a prompt, MCP startup or inventory options. JSON stdout is one structured result. Durable rejected/expired results and definite refused previews (`state: refused`, no recovery ID) exit 1. Unknown transport and failed lookups exit 2. A definite decision-request refusal is `unresolved` (exit 2), not rejection of the original operation: `request_id` retains the attempted decision ID, while `recovery_request_id` and the GET command use the original `operation_id`. Unknown transport keeps the attempted request ID for recovery. Raw error text is never printed. It never retries a POST automatically. APIs are `POST /sessions/{id}/operator-actions/preview`, `POST .../decision`, and session-scoped `GET .../{request_id}` under `/api/v1`. Migration 0024 retains immutable previews/request fingerprints and terminal evidence; exact retries return the original result, conflicting reuse refuses. Recognized prior schema prefixes migrate; unknown/future schemas still refuse.

开放**现有项目的显示名称、目录归档/恢复**与**一条站内通知标记已读**；7E5B 下述独立流程另支持仅元数据创建。预览会保存待确认记录和审计，但不修改目标；须核对完整旧值/新值、准确 ID、来源会话/范围/策略、修订、摘要与十分钟有效期，再显式确认。确认时同一 SQLite 事务核对原权限与旧状态，原子写入目标、结果与审计。已经已读或同名显示 `no_op`；不支持的通知状态直接拒绝。项目名称只是元数据，不是指令、工作区绑定或权限。取消仅拒绝待处理预览，不会撤销已提交改动。

网页使用预览/确认/取消按钮；Ink 使用 `/op rename ID 新名称`、`/op read ID`，核对后 `y` 确认、`n` 取消。未知响应保留原标识，以 `/op recover [会话ID 请求ID]` 或网页只读核对恢复，绝不换新 ID 重发。待确认/未知时冻结冲突操作与导航，聊天流和草稿不丢；已知结果不会因刷新失败降级成失败。浏览器仅持久化三个恢复 ID，不保存令牌、新名称或草稿。终端结果保留键盘焦点，重复 `y` 不会批准其他工具；Enter/Esc 收起。非交互 JSON 输出保持单条结构，拒绝/过期退出 1，未知/未解决退出 2。

All other O3–O6 management writes remain unavailable, including project create/use, cross-scope knowledge assignment, skill lifecycle in the legacy management surface (the separate rebuilt catalog API/SDK is B1), trigger execution, dead-letter retry/delete, backup/restore and policy changes. P3 workflow/skill filter completeness remains deferred and is not claimed fixed by this slice. / 其余 O3–O6 写入能力仍未开放；P3 工作流/技能筛选完整性仍延期，本次不计为修复。

### Task 7E3A historical checkpoint: trusted knowledge kernel / 可信知识内核历史检查点

At the historical 7E3A checkpoint (superseded by the 7E3B API/UI section below), this added internal Go `Store` knowledge preview/decision/recovery, bounded list/search, and migration 0025. **No new HTTP route, SDK method, Web button or terminal command is enabled. Task 7E3 is not complete.** Existing knowledge writes remain unavailable in the rebuilt operator UI; legacy `pachat knowledge` commands are not adapters for this kernel. The separately reviewed 7E3B client/API slice is still required.

- Ownership is the session's immutable workspace **ScopeID**, never a business Project.ID or source metadata. Only existing workspace sessions in `default`/`acceptEdits` can prepare writes, with exact explicit confirmation; `plan` and `chat_only` refuse indexing. No session policy or execution permissions are modified. Read-only scoped inventory/search also permit `plan`.
- Explicit valid UTF-8 text (including empty text), or one safe workspace-relative regular file, is limited to **64 KiB**. Preview binds SHA-256, byte length, source identity, policy, scope and expected lifecycle/index revisions; at most 512 bytes of excerpt are returned. Private SQLite staging is retained, not stored in the browser. Content remains untrusted evidence and can enter future model context via scoped retrieval. No remote fetch, embeddings, provider call, watcher or auto-ingestion occurs.
- Same-scope text duplicates use content identity and preserve the first source label; files use their relative-path identity. Exact unchanged active content is `no_op`, not a new effect. The same content in different scopes has different document IDs. Reindex explicitly rereads a saved safe relative file path for a new review, never a legacy URI. Text reindex uses retained content. Empty content is `empty`, not falsely described as searched/indexed evidence.
- Confirm atomically commits content/chunks/current-generation ownership, retained provenance version, operation result and audit. Removal means reversible **deactivation**, not deletion. Original content and operation evidence remain. Managed knowledge is excluded from legacy unscoped and vector-ID readers; scoped readers require the active matching revision and immutable scope. Vector hits must resolve to an allowed current local chunk with matching supplied document/source identity. Missing/denied/stale chunk lookups never reconstruct remote payload text; legacy remote-only vector payloads are no longer accepted as evidence. Root/policy/content/revision drift refuses. Failed chunk or audit insertion rolls back the entire effect; recovery is read-only and never replays it automatically.
- Bounds: 128 retained documents per scope, 512 chunks per admitted document, 4,096 retained previews globally and 64 MiB total staged content (hitting a bound refuses, never silently deletes). List pages are 1–50; local BM25 query is at most 1,024 bytes with 1–8 results and a bounded current scoped corpus. Cancellation uses Go contexts. There is no import/export, retention purge, arbitrary archive download, remote indexing or full operator parity.
- Native matrix: Linux requires the existing `openat2` containment, persistent root identity and pinned regular-file reader; symlinks, mount crossings, hardlinks, traversal, protected dot/data/node_modules paths and a workspace containing the runtime DB refuse. macOS/Windows/other native adapters remain unavailable with the existing exact capability reason, not a weaker fallback. A final file reread detects observed changes; an external writer can still change a file after that check. The committed index represents the exact staged bytes, **not a promise that the external file stays unchanged**.

7E3A 历史检查点（当前 API/界面状态见下方 7E3B）仅增加 Go 内部可信知识内核及 0025 迁移，**未开放 HTTP、SDK、Web 或终端知识写入；整体 7E3 尚未完成**。7E3B 仍需单独完成共享预览/确认/恢复界面与验证，现有不可用能力保持不可用。

知识归属取自已保存的不可变工作区 ScopeID，不是业务 Project.ID，也不信任导入元数据。仅 `default`/`acceptEdits` 工作区可在精确确认后本地入库；`plan` 与纯聊天拒绝写入，不修改会话策略或执行权限。文本/安全工作区相对路径文件上限 64 KiB，预览绑定哈希、字节数、来源、策略和修订；SQLite 私有暂存不进入浏览器持久化。检索内容始终是不可信证据，可能进入后续模型上下文；不会远程抓取、调用向量服务或自动监控文件。

同范围相同文本保持首个来源标签并返回无操作；文件按相对路径标识。跨范围相同内容隔离。重新索引文件必须重新读取并确认；移除仅停用检索，保留原文及来源版本，可明确重新索引恢复。内容、当前索引所有权、结果与审计同事务提交，分块/审计失败整体回滚；未知应答只按原 ID 查询，不自动重放。所有检索拒绝停用/过期修订，旧无范围及向量 ID 读取不能绕过工作区隔离。向量命中必须解析为获准的当前本地块，且提供的文档/来源身份须一致；缺失或被拒绝的本地块不再回退到远端 payload 文本，旧远端独有 payload 不作为证据。

限制为每范围 128 个保留文档、每文档最多 512 块、全局 4,096 次保留预览/64 MiB 暂存、每页最多 50 条、查询最多 1,024 字节/8 个结果；达限拒绝而非静默删除。Linux 沿用强原生安全读取；macOS/Windows 等未验证适配器明确不可用。确认前最后一次读取只能检测已观察到的文件变更，无法阻止外部进程在检查后修改；索引保证精确暂存内容，不承诺磁盘文件之后不再变化。未提供导入导出、永久清理或完整知识管理平替。


### Task 7E3B: authenticated local knowledge / 认证本地知识操作

Web **Management → Knowledge** now separates read-only inventory from explicit local search and exact staged writes. Choose explicit text + label or a safe server-workspace-relative UTF-8 file. Preview **saves text in private backend storage**, but does not yet index it. Review immutable scope, source, SHA-256, UTF-8 bytes, bounded excerpt, policy, lifecycle/index revision, digest and expiry. Confirm/cancel, deactivate and reindex are visibly separate. List/select/refresh never writes. Results show actual `local_bm25` document/chunk/source/index-revision evidence, or explicit search-not-invoked/empty/failed states. Untrusted evidence is inert text, never external Markdown resources or a new user/system instruction.

All routes below use the mandatory existing loopback/Host/Origin/bearer envelope, locked DB, CSP and no-store. POST requires the rebuilt protocol header and JSON. They read stored session metadata without provider resolution, MCP startup or session capability probing in every session mode. No caller-selected root, scope, project or URI exists. Unknown/duplicate JSON fields, invalid UTF-8/lone surrogate escapes, nulls, trailing data, excessive bodies, invalid IDs/queries and foreign cursors refuse. SDK knowledge POSTs do not make a `/status` provider probe.

| Route under `/api/v1/sessions/{session}/knowledge` | Exact contract |
|---|---|
| `POST /preview` | Six required strings: `request_id, kind, document_id, source_kind, source, content`. `ingest`: empty document ID, `text` label+content or `file` relative path+empty content. `deactivate`/`reindex`: document ID only; three source/content strings empty |
| `POST /decision` | Four required strings: `request_id, operation_id, digest, decision` (`confirm` or `cancel`) |
| `GET /operations/{request_id}` | Original preview **or decision** ID, session-owned durable read-only recovery; no query |
| `GET /` | Optional `limit` 1–50 (default 20), opaque session-bound `cursor`; managed index metadata, not content download |
| `GET /search?q=...` | Actual current-scope local BM25; UTF-8 query ≤1,024 bytes, optional `limit` 1–8 (default 8) |

Generated Go-derived TS DTOs/JSON Schema and strict runtime parsers preserve the kernel states: `pending → committed | no_op | rejected | expired`; index views distinguish `staged`, `indexed`, `empty`, `deactivated`, `not_applied`, inventory `stale`. A transport `unknown` is not proof of rollback. Stable recovery IDs are shown; only GET reconciliation is allowed, never automatic POST retry. Known commits survive failed list/search refresh or persistence cleanup. Pending/unknown quarantines chat/context/queue/Job/metadata mutations and session/navigation changes without destroying the mounted live conversation or draft. Browser reload requires token reentry and restores only session/request/operation IDs, never text, path input or credentials. Ink keeps result focus until deliberate dismissal, so repeated `y`/`n` cannot approve another tool.

Ink commands (no full-text download/export):

```text
/knowledge list [CURSOR]
/knowledge search QUERY
/knowledge text {"label":"owned note","content":"explicit UTF-8 content"}
/knowledge file relative/note.txt
/knowledge deactivate DOCUMENT_ID
/knowledge reindex DOCUMENT_ID
/knowledge confirm
/knowledge cancel
/knowledge recover [SESSION_ID REQUEST_ID]
/knowledge dismiss
```

Machine CLI uses an **existing** session, never creates a hidden session or sends a model prompt. JSON stdout is a single result. Known operation rejection/expiry and definite preview-request refusal exit 1. A pre-admission refusal has CLI state `refused`, an allowlisted static `code` and explanation, empty `operation_id`/`recovery_request_id`, and no advice to look up a nonexistent preview. A definite decision-request refusal exits 2 with state `unresolved`, retains attempted `request_id`, and points `recovery_request_id` and the GET lookup command at the **original operation ID**; it does not label that operation rejected. Unknown write transport still exits 2 with state `unknown` and recovery by the **attempted request ID**. Failed reads remain `unresolved`/exit 2. No automatic POST retry. Failure records never include raw error messages, source/path/content inputs or tokens. Invalid flags fail before transport. Text previews/reviews visibly escape C0/C1/DEL/Unicode formatting controls without changing raw identities.

```sh
# Existing supported workspace session; new stable ID for each preview/decision.
pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx --session SESSION \
  --knowledge text --label 'Owned note' --text 'orchid local evidence' --request-id PREVIEW --format json
# Read the exact returned preview, then explicitly decide:
pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx --session SESSION \
  --knowledge confirm --operation-id PREVIEW --digest SHA256 --request-id DECISION --format json
pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx --session SESSION \
  --knowledge lookup --request-id DECISION --format json
pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx --session SESSION \
  --knowledge search --query orchid --format json
# Also: --knowledge file --file RELATIVE_PATH; deactivate|reindex --document ID;
# cancel with exact operation/digest; list [--cursor CURSOR]. Token remains PA_AGENT_TOKEN.
```

网页 **管理 → 知识** 已接入同一个 Go 内核。明确区分只读目录、实际本地 BM25 查询与入库预览；预览会把明确提供的文本保存到后端私有存储，但尚未进入检索。确认前显示精确范围/来源/哈希/字节数/有界摘录/策略/修订/摘要/有效期。文本与服务器工作区相对文件分别选择；停用只隐藏检索，保留原文来源，可明确重新索引恢复。读取、选择和刷新不触发写入。

所有知识 API 使用已锁定的重建数据库及现有认证安全边界，仅读取已保存的会话绑定，不解析模型供应商、不启动 MCP、不调用远端搜索/向量/模型。查询按钮才实际搜索，显示真实当前修订的文档/块/来源证据，区分未执行、空结果和失败；结果只作为不可信纯文本展示。拒绝无效 UTF-8、孤立代理项、重复/未知字段、越界输入和跨会话游标；客户端也严格核对身份、状态和界限。

Ink 的 `/knowledge text` 接收明确 JSON 标签/内容；`file` 仅接收安全工作区相对路径；`confirm/cancel/recover/dismiss` 保持精确操作焦点。非交互 `--knowledge` 要求已有会话，JSON 标准输出纯净。确定未获准的预览以 `refused` 退出 1，保留安全静态错误码/说明，不建议查询不存在的预览记录；已知操作拒绝/过期同样退出 1。确定被拒的确认/取消请求以 `unresolved` 退出 2，保留尝试的 `request_id`，但 `recovery_request_id` 与只读查询指向原 `operation_id`，不会误称原操作已被拒绝。未知写入仍以 `unknown` 退出 2，按尝试的请求 ID 查询；读取失败为 `unresolved`/退出 2。绝不自动重发 POST；失败记录也不输出原始错误、路径、内容或令牌。网页重载需重新输入令牌，只恢复三个 ID；不持久化原文、路径输入或令牌。未知/待确认隔离冲突操作，但保留聊天、草稿、队列、Job 和原审批；已知提交不会被刷新失败降级。

The 7E3A bounds and native limitations above remain unchanged. `plan` can list/search but cannot write; `chat_only` has no knowledge scope. Unsupported native capability reasons remain explicit, with no weaker reader. There is no remote ingestion, import/export parity, permanent deletion/automatic retention, project assignment/policy widening, skills/triggers execution, or real-provider/native macOS/Windows validation. Independent review and complete-source backup are still required before overall 7E3 acceptance.

7E3A 的容量与原生平台限制不变。`plan` 可读/查不可写，纯聊天无知识范围。保留明确的原生能力拒绝原因，不降级绕过读取边界。尚无远端入库、完整导入导出、永久删除/自动清理、跨项目分配或策略扩权、技能/触发器执行；未验证真实供应商与 macOS/Windows 原生效果。整体 7E3 仍须独立审查及完整源码归档后验收。


### Task 7E4: catalog lifecycle, not an execution gate / 项目目录生命周期，不是执行权限

`project.archive` requires `new_value: "archived"`; `project.restore` requires `new_value: "active"`. Existing authenticated operator-action routes, four-string intents, exact single-use previews and decision tuples are reused. Metadata actions work in **all** session modes, without a resolver/provider/MCP call. Names, all policy columns, workspace ScopeIDs, session model/policy bindings, active approvals, queue/Jobs, knowledge ownership/chunks and retrieval remain unchanged. **Catalog only; not permission revocation. Archive does not stop jobs/runs, revoke tools or remove knowledge.** Restore changes catalog state only. Rename works in either state and never restores implicitly.

Migration `0026_project_catalog_lifecycle.sql` adds a sidecar with generation, monotonic revision and active/archived state. Every existing row starts active, including literal archived-looking names; no historical suffix is interpreted. Legacy inserts receive a fresh generation, including delete/recreate of an ID. Actual name changes increment revision. All project preview revisions bind ID, generation, revision, exact name and state, so rename/state A→B→A and delete/recreate reject stale confirmation. Missing sidecars refuse; HTTP never repairs them. Old pending rename previews reject stale confirmation after upgrade; retained terminal results/audits are not rewritten. No project table rebuild or whole-object upsert is used by these actions.

Inventory `GET .../operator/projects?filter=active|archived` supports bounded filter-bound cursors; omit the filter to see both. Privacy is a separate field. A project inventory page containing an ID over the 512-byte action bound refuses the read and exposes no mutation controls; it never substitutes an actionable display ID. A real literal `[identifier exceeds bound]` remains valid. Project IDs are business labels, **not** workspace ScopeIDs, isolation or execution grants. Names and IDs render with visible escaped controls; exact values remain unchanged in JSON and requests. Invalid UTF-8 and lone-surrogate JSON escapes refuse rather than normalize.

```sh
# Preview only. Requires an existing session and explicit stable request ID.
PA_AGENT_TOKEN=... pnpm --filter @personal-agent/terminal exec tsx src/cli.tsx \
  --session SESSION --op archive --target PROJECT --request-id ARCHIVE_PREVIEW --format json
# Restore is --op restore with a fresh preview ID; confirm/cancel uses the exact
# operation_id + digest as above. GET lookup accepts original or recorded decision ID.
```

Shared Web/Ink commands: `/project archive ID`, `/project restore ID`, `/project rename ID EXACT NAME` (also `/op archive|restore|rename`). These prepare only. `/op confirm|cancel|recover|dismiss` retains existing quarantine and repeated-key isolation. The CLI never creates sessions, sends prompts, retries POSTs or starts models/MCP for these actions. Successful pending/committed/no_op lookups exit 0; known rejected/expired or definite preview refusal exit 1; unknown/unresolved exit 2. In a refused second decision, inspect the original operation; in true acknowledgement loss, inspect the attempted request ID. Browser reload preserves only recovery IDs and requires token reentry, not draft restoration.

目录操作使用现有四字段意图、精确预览/确认及只读恢复接口：归档新值必须是 `archived`，恢复必须是 `active`。**仅整理目录，不撤销权限，不停止运行或 Jobs，不移除知识。** 恢复只改目录状态；归档状态下改名不会自动恢复。隐私字段独立显示；项目业务 ID 不是工作区 ScopeID，也不构成知识隔离或沙箱。

0026 迁移保留原项目表和全部策略字节，所有旧记录回填 active，名称含“archived”也不猜测历史状态。侧表代次与递增修订防止改名/状态来回变化及删后重建造成 ABA。旧待确认改名会拒绝过期修订，已完成结果保持可恢复；缺少侧表记录直接拒绝，不在 GET/HTTP 中补写。项目 ID/名称的控制字符可见转义，JSON/请求身份不改写，非法 Unicode 不静默替换。目录页若含超过 512 字节操作边界的项目 ID，会拒绝读取且不显示操作控件，绝不把显示占位文字当作目标 ID；真实的 `[identifier exceeds bound]` 字面 ID 不会被拉黑。

网页有归档/恢复预览按钮和 active/archived 筛选；Ink 使用 `/project archive ID`、`/project restore ID`，核对后确认或取消。命令行 `--op archive|restore` 只预览，确认仍需原操作 ID 与摘要。确定被拒绝的预览以 `refused` 退出 1 且不给不存在的恢复 ID；确定被拒绝的决定请求以 `unresolved` 退出 2，保留尝试 ID，但用原操作 ID 只读核对。响应丢失用尝试请求 ID 核对，不自动重发。已知结果不因刷新失败丢失，重复 y/n 不会误批其他工具。

**7E4 historical boundary / 7E4 当时边界:** this lifecycle slice did not expose creation. The subsequent 7E5A kernel and 7E5B below now support metadata-only creation; client use/session-label selection and policy editing remain unavailable; the 7E6A server-only admission kernel is described below. Empty legacy Allowed* still means unrestricted for legacy policies only. No push, merge or release is part of these slices.

### Task 7E5A: execution-classification kernel only / 仅执行分类安全内核

**7E5A kernel, now used by 7E5B below.** This prerequisite safety boundary does not itself provision execution. Metadata-only creation is now available through the exact preview/confirm operation, Web form and Ink/CLI commands described below. Existing rename/archive/restore still organize the catalog only. Optional immutable session association remains separate.

Migration `0027_project_execution_classification.sql` adds an immutable sidecar without changing the eleven legacy project columns, existing policy bytes or lifecycle generation/revision. Existing rows and trusted legacy inserts retain `legacy_policy`; empty legacy allowlists retain their original unrestricted semantics. Future rebuilt creation must insert `metadata_only` classification and the project row in the same transaction. The classification is retained across deletion/recreation, cannot be changed by UPDATE/DELETE/REPLACE, and is displayed separately from active/archived catalog state. No promotion API exists.

`metadata_only` grants **no tools, models, workflows or knowledge isolation**. All domain `Allows*` checks and concrete legacy task/workflow/trigger admission refuse it, as well as unknown/missing classification or a missing nonempty project ID, before retrieval/planning/execution. The actual empty no-project legacy path remains available. Catalog actions cannot convert the business ID into a workspace ScopeID, memory owner, root or session binding.

Generic legacy `Project.Store.Save` remains a trusted in-process policy writer for legacy projects, not a metadata create/import path. It refuses metadata copies/overwrites and retained metadata IDs. JSON decoding always marks a Project untrusted, even if `execution_class` is absent or spoofed as `legacy_policy`; generic Save cannot use that value to provision authority. **Portable imports containing any projects now explicitly refuse as unsupported**, including old backups and spoofed fields. Portable exports containing metadata-only projects also refuse rather than emit a lossy authority representation. Legacy project export remains read-only; explicitly project-free portable artifacts retain their non-project import behavior. This is a deliberate compatibility limit, not a general old-backup migration promise. Preserve the source; the importer never silently removes projects or repairs classification.

**7E5A 安全内核现由下述 7E5B 使用。** 已开放仅元数据创建的精确预览/确认、网页表单和 Ink/CLI 命令，不开放执行授权；可选的不可变新会话关联仍独立待做。0027 只新增不可变执行分类侧表，旧策略字节、项目表字段与目录代次/修订保持不变。旧记录及可信旧插入属于 `legacy_policy`，空 Allowed* 仍按原义不限制；将来的重建创建必须同事务写入 `metadata_only` 和项目记录。分类在删后重建时保留，禁止 UPDATE/DELETE/REPLACE 改类，没有提升权限接口。

`metadata_only` **不启用工具、模型、工作流或知识隔离**。领域 Allows* 与旧任务、工作流、触发器入口在检索/规划/执行前拒绝元数据类、未知/缺失分类和不存在的非空项目 ID；真正空的无项目旧路径保留。改名/归档/恢复不会修改工作区、会话、记忆范围或知识归属。目录中分别显示执行分类与 active/archived 状态。

通用旧 Save 仅用于可信进程内旧策略写入，拒绝元数据项目复制/覆盖、保留身份重建及 JSON 解码所得对象；即使外部伪造 `legacy_policy` 或删去分类字段也不会得到可信来源。**含任何项目的 portable 导入现在明确拒绝，包括旧备份；含 metadata_only 的 portable 导出同样拒绝。** 旧项目只读导出仍可用，明确不含项目的 portable 文档仍可导入其他类别。这是有意的兼容边界，不承诺任意旧备份迁移；保留源文件，不会静默删除项目或修复分类。


#### Metadata-only project creation (7E5B) / 仅元数据项目创建

Web Projects has **Project name → Preview create project**. Ink accepts `/project create EXACT NAME`; this prepares only. Both show the server project ID separately from operation/recovery request IDs, `execution_class=metadata_only`, all fixed defaults and “no tools/models/workflows/knowledge isolation enabled”. Enter in the Web name form never confirms or submits. Use the existing explicit confirm/cancel controls. Name text is not saved in browser recovery storage; only session/request/operation IDs are persisted. Exact Unicode/control characters stay intact in stored names and are visibly escaped in previews, not interpreted as markup or terminal controls.

```sh
# Existing session only. Empty target is mandatory; do not supply --target.
pnpm --filter @personal-agent/terminal start --url http://127.0.0.1:7319 \
  --session SESSION --op create --new-name 'My project' --request-id PREVIEW_ID --format json
# Inspect the returned server-generated target_id and digest; only then confirm.
pnpm --filter @personal-agent/terminal start --url http://127.0.0.1:7319 \
  --session SESSION --op confirm --operation-id PREVIEW_ID --digest DIGEST \
  --request-id DECISION_ID --format json
# If acknowledgement is lost, GET recovery only. Never automatically repeat a POST.
pnpm --filter @personal-agent/terminal start --url http://127.0.0.1:7319 \
  --session SESSION --op lookup --request-id DECISION_ID --format json
```

The authenticated API uses the existing `/sessions/{session}/operator-actions/preview` body: `{request_id, kind:"project.create", target_id:"", new_value:NAME}`. Only these four string fields are accepted. Names must contain non-whitespace and be valid Unicode, at most 2,048 UTF-8 bytes, with no normalization. The server generates a UUIDv4 `project-…` ID inside the durable ten-minute preview. Same-request replay returns the same ID, including after restart; no project, lifecycle or classification row exists before confirmation. A pending ID is not a usable project.

Confirmation inserts `metadata_only` classification **before** the INSERT-only project row; the existing trigger creates its catalog generation. Classification, project, lifecycle, terminal operation/result, decision request and audit commit together. Existing IDs and retained classification/lifecycle tombstones refuse, never upsert. Fixed storage defaults are `privacy_class=local_private`, empty repository/knowledge/Allowed* lists, empty memory scope and `{}` budget. These are inert storage, not permission configuration. Generic Save/import/promotion guards remain unchanged. Creation enables no workspace root, policy, scope/grant, knowledge ownership, project sandbox or automatic current/new-session association. Catalog rename/archive/restore remain usable afterward; actual legacy runtime/workflow/trigger admission still refuses this project.

Global create admission bounds: at most 10,000 distinct retained catalog/classification/lifecycle identities, 10,000 retained create operations, and 64 unexpired pending create previews. Catalog capacity is rechecked at confirmation. Cancelled/expired previews and audit evidence stay retained; expired previews do not occupy the live-pending allowance. There is no automatic evidence purge or recovery-by-new-ID. Capacity refusal is explicit, not eviction. Existing legacy/catalog operations retain their previous behavior.

Machine JSON preserves `target_id` (project ID), `operation_id` (original preview request), and `decision_request_id` separately. Known pending/committed results exit 0; rejected/expired and definite preview refusal exit 1; unknown/unresolved exit 2. Definite decision refusal points recovery to the **original operation**, not the refused decision request. Unknown transport points to the **attempted request ID**. Recovery is read-only; known committed results survive failed refresh or storage. All modes use the stored session binding without model/MCP resolution or hidden session creation.

网页项目目录提供“Project name → Preview create project”，Ink 输入 `/project create 精确名称`，均只预览。预览显示独立的服务端项目 ID、操作/恢复请求 ID、`metadata_only` 分类、固定默认值及“不启用工具、模型、工作流或知识隔离”。网页名称框按 Enter 不提交或确认；必须明确确认/取消。浏览器只保留会话/请求/操作 ID，不存名称或令牌。控制字符保留原字节并可见转义，不解释为 HTML/终端命令。

API 必须传空 `target_id`，只接受四个字符串字段，名称为有效 Unicode、非全空白且不超过 2,048 UTF-8 字节，不做规范化。十分钟持久化预览生成 UUIDv4 项目 ID，同请求及重启恢复保持一致；确认前没有项目、生命周期或分类实体。确认同事务先插入 `metadata_only` 再 INSERT 项目，随后生命周期、结果、请求和审计一起提交；现存或保留墓碑身份拒绝覆盖。固定默认值不授予权限，MemoryScope 不会设为项目 ID，旧运行时/工作流/触发器仍拒绝执行，也不能通过 Save 或 portable 导入提升。

全局限制：最多 10,000 个保留项目身份（含分类/生命周期墓碑）、10,000 条保留创建操作、64 个未过期待确认预览；确认时复查目录容量。取消/过期保留证据，不自动清理，过期不占活跃预览额度。元数据项目可改名/归档/恢复，但创建本身不启用根目录、知识所有权、策略编辑或自动会话关联；服务端显式新会话标签见下述 7E6A。丢失确认响应时只按原请求 ID 读取恢复，不自动重发；明确拒绝决策请求时按原预览操作恢复，未知传输时按本次尝试请求恢复。JSON 和退出码与其他精确元数据动作一致。


### Task 7E6A: atomic admission and immutable project-label kernel / 原子会话接纳与不可变项目标签内核

**Historical stage A scope; the client implementation is described in the following section.** Authenticated `POST /api/v1/sessions` accepts an optional string `catalog_project_id` alongside the existing explicit mode/model/MCP intent. Omit it or use `""` for no label. It selects an active, extant catalog record with a recognized execution classification; `metadata_only` is valid **as a label**, not as execution policy. Only the server captures `project_label: {project_id, generation, revision, name}`. Callers cannot supply the snapshot, root, scope, permissions or a current-session setter. Existing unassociated sessions remain unassociated.

Every new-session admission reserves a durable single-use request before potentially slow setup. Setup occurs outside the service lock and the publishing transaction. Complete session, immutable workspace/chat policy, API binding, current active catalog snapshot, exact response and terminal admission state commit together. A failed publication leaves no partial session; the retained unresolved claim cannot be replayed to repeat setup or MCP startup. At most 20 preparations may be in flight; this does not increase the run-worker or MCP-process limits. The historical `interaction_mcp_admissions` table retains its name and now also records ordinary session preparations. `GET /api/v1/admissions/{original_request_id}` is authenticated, bounded, query-free, read-only and provider-independent; completion includes the immutable snapshot when present. Unknown results require inspection, not a POST probe. Existing interrupted MCP claims and old partial mappings are never relaunched or silently completed.

Changing the selected project under the same request ID conflicts. Rename/archive during preparation is settled by the final write transaction: archive before publication refuses; a rename before publication supplies the then-current snapshot. Rename/archive/delete afterward never change that snapshot. Branch, clear, conversation rewind and Job-derived sessions copy the historical label even after catalog deletion/recreation. Labels never enter `RunRequest.ProjectID`, Allowed* policy, context instructions or project-memory lookup. **Two labels on the same workspace share the same knowledge scope; different workspace roots remain scoped separately. A label is not a sandbox, isolation, permission grant or evidence that any model/tool ran.**

Generated response contracts and strict shared parsers preserve bounded optional snapshots and reject malformed/authority-bearing fields. **At the A-stage checkpoint, B was not yet implemented:** no Web project picker, Ink next-session label command, machine CLI selection option, label-specific session controller/navigation recovery, or end-user provenance display is enabled by A. No automatic selection from project management. The subsequent B implementation and browser/PTY coverage are described below.

**以下记录 A 阶段的历史范围；客户端实现见下一节。** 已认证的新会话 `POST /api/v1/sessions` 在原有明确 mode/model/MCP 意图之外可接收可选字符串 `catalog_project_id`；省略或空串表示无标签。只能选择存在、active 且执行分类已识别的目录记录，`metadata_only` 仅可作描述标签，不变成执行策略。服务端同事务捕获 `project_label` 的项目 ID、目录代次、修订和精确名称；客户端不能提供快照、根目录、知识范围或权限，也没有修改当前会话标签的接口。旧无关联会话保持无关联。

新会话在慢准备前保留持久单次请求声明，准备不占服务锁或发布事务。完整会话、工作区/纯聊天策略、API 绑定、活动项目快照、响应与终态一次提交；失败没有半会话，未知/未解决声明不会重放准备或 MCP 启动。最多同时准备 20 个，不增加回合执行器或 MCP 进程上限。沿用旧表名的接纳账本现也记录普通会话；原请求 ID 的 `GET /api/v1/admissions/...` 需认证、拒绝查询参数、只读且无需提供方在线，完成记录可读原快照。未知结果只读检查，不以 POST 探测。旧中断 MCP 声明和旧部分绑定不会重新启动或偷偷补齐。

同请求 ID 改换项目会冲突。准备期间归档/改名由最终事务确定：发布前归档拒绝，发布前改名捕获新快照；发布后改名/归档/删除都不改历史。分支、清空、对话回退和 Job 派生会话复制原标签，即使目录已删除/重建。标签不传给旧 `RunRequest.ProjectID`、Allowed*、上下文指令或项目记忆检索。**同一工作区的两个标签共享同一知识范围，不同根目录仍分开；标签不是沙箱、隔离、授权或工具/模型已执行的证据。**

已生成可选响应契约，严格解析器拒绝损坏快照和夹带权限字段。**在 A 阶段检查点，B 尚未实现：** A 不开放网页选择器、Ink 下一会话标签命令、机器 CLI 参数、标签意图的控制器/导航恢复或面向用户的历史标签展示；项目管理不会自动选择/新建会话。后续 B 阶段的双端实现与真实浏览器/PTY 流程验收见下节。

### New-session descriptive project labels (rebuilt preview)

Web: use **Load active labels**, choose an exact ID/name/classification or **No label**, then explicitly create a new session. Browsing uses authenticated bounded `GET /api/v1/catalog/projects` (25 per page, maximum 50), without a throwaway session, provider resolution or MCP startup. Selection applies only to the next admission. `metadata_only` is not execution permission. Existing sessions, branches and Job execution details show the immutable historical label, not the renamed/archived live catalog. Workspace, mode, scope and policy remain separate; inspect **Management → permissions** for the actual binding. Different labels on the same workspace share knowledge.

Ink: `/new labels [next]`, `/new label EXACT_ID`, `/new label none`, then `/new create confirm chat_only|default|plan|acceptEdits [mcp]`. The optional `mcp` starts trusted **non-sandboxed** servers; it is never inferred from a label. `/history`, the current header and `/jobs show ID` show historical provenance.

Machine CLI: `--project-label ID` applies only to new admission, not `--session`, management, metadata or knowledge commands. `--new-session --format json` creates an empty session; `--project-label ID --print TEXT --format json` uses the existing one-shot message path after a confirmed creation. No-label remains the default. `--admission ORIGINAL_REQUEST_ID --format json` performs GET-only recovery against the same `--url`. Uncertain admission exits 4 with original request/session IDs and a recovery command, never retries POST or submits initial content after recovery. Initial content is not part of the server's session-create transaction; if admission acknowledgement is lost, recovery inspects the existing empty session and the intended first prompt remains **not sent**. A later message must be an explicit new user action.

Pending/unknown admission quarantines conflicting input, approvals, context, queue, Job, metadata and knowledge writes. Web stores only bounded admission request/session IDs in session storage, never token, content, chosen ID or name. Reload requires token reentry and explicit read-only recovery. Known confirmed sessions survive list refresh/storage-cleanup failures. No cancellation API exists for admission: **Cancel intent** / `/new cancel` does not prove setup/process startup was canceled and does not stop existing runs. Starting/not-found claims stay quarantined. Only a verified unresolved/revoked claim allows explicit **Acknowledge failed claim** / `/new acknowledge-failed`; this allows a new authorized action, not replay of the old request. Copy displayed recovery IDs if browser storage is unavailable.

### 新会话项目标签（重建预览）

Web 先点 **读取标签**，选择精确 ID、名称及分类或 **无标签**，再明确新建会话。目录通过有界、已认证的 `GET /api/v1/catalog/projects` 读取，不偷偷建会话，不解析提供商或启动 MCP。选择仅用于下一个新会话；`metadata_only` 不赋予执行权限。历史会话、分支及 Job 执行详情显示不可变的原始标签，重命名或归档不会改变历史。标签与工作区、权限模式、scope、策略分开；实际绑定在 **管理 → permissions** 查看。同一工作区的不同标签共享知识，不是项目知识隔离。

Ink：`/new labels [next]`、`/new label 精确ID`、`/new label none`，然后 `/new create confirm chat_only|default|plan|acceptEdits [mcp]`。只有显式 `mcp` 才启动可信的**非沙箱**服务器。机器 CLI 新建时可加 `--project-label ID`；不能与 `--session` 恢复、管理、元数据或知识命令混用。`--new-session --format json` 创建空会话；`--print TEXT --format json` 仅在新建确认后进入既有单次消息流程。

结果未知时退出码为 4，输出原始请求及已知会话 ID。用同一 `--url` 下的 `--admission 原请求ID --format json` 只读核对；不会重复 POST、重启 MCP 或重放初始内容。初始消息不属于服务端创建会话事务：创建回执丢失后仅核对已有空会话，初始消息**尚未发送**，后续发送必须是新的明确操作。Web 只持久化有界恢复 ID，令牌、内容、所选 ID 和名称不写入浏览器存储；刷新后重新输入令牌并明确恢复。

待确认的新建会话会锁定冲突的输入、审批、上下文、队列、Job、元数据和知识写入。没有取消 admission 的 API，**请求取消意图**或 `/new cancel` 不代表进程未启动，也不停止已有运行。starting/not-found 保持锁定；仅核实为 unresolved/revoked 后，明确确认失败记录才允许新的操作，不重放旧请求。目录刷新或清理存储失败不会丢失已确认的会话；存储不可用时请保留界面中的恢复 ID。

Admission compatibility is checked by authenticated `GET /api/v1/protocol`, a static resolver-free handshake. Unsupported/old protocols refuse before POST; selection/recovery never uses provider status as a probe. Admission GET includes durable `mcp_opt_in`, derived from the original claim, not current discovery/connection health. Discovery still does not prove a tool ran. Ink `/new label-json JSON_STRING` selects exact special IDs (including a literal `"none"`, whitespace or control characters); `/new label none` clears the next-session choice. Recovery JSON includes exact `recovery_args` and a shell-quoted command for the original daemon.

新建前通过已认证、静态且不解析提供商的 `GET /api/v1/protocol` 协商协议；旧协议在 POST 前拒绝，不以提供商状态作为探测。Admission GET 的 `mcp_opt_in` 来自原始持久化 claim，与当前连接/发现状态分离；发现服务器仍不是已执行工具的证据。Ink `/new label-json JSON字符串` 可精确选择特殊 ID（包括字面值 `"none"`、空白及控制字符），`/new label none` 则清空标签。恢复 JSON 同时提供原服务的精确 `recovery_args` 及 shell 安全引用的命令。

Protocol preflight failures (including network failure, an old server's 404 and malformed replies) are explicitly **not sent**: no admission POST was invoked, the draft remains editable, and another attempt requires a new explicit action. Once POST is invoked, a lost acknowledgement still stays quarantined even if admission GET returns not-found. A new idle recovery discards the previous request's display state; the same already-confirmed request retains its known result. Ink applies recovery of the already-active session without remounting its controller, so the next explicit input remains available. Ink catalog IDs and Job provenance use control-escaped JSON that remains copyable into `/new label-json` without changing IDs or confusing literal escape text with actual controls.

协议预检失败（网络失败、旧服务 404、损坏响应）明确属于**未发送**：尚未调用 admission POST，草稿仍可编辑，下一次尝试必须由新的明确操作发起。POST 一旦调用，回执丢失仍保持隔离，即使 GET 暂时查不到也不能解锁。空闲时恢复另一个请求会清除旧请求的展示状态，同一个已确认请求则保留已知结果。Ink 恢复当前已打开的同一会话时直接确认应用，不重建控制器，后续明确输入仍可正常发送。Ink 目录 ID 和 Job 来源使用控制字符转义的 JSON，可复制给 `/new label-json`，不改变原始 ID，也不混淆字面转义与实际控制字符。

### Skill catalog usage / Skill 目录使用

Web: open **Skill catalog / 技能目录** in an existing session. Paste exact UTF-8 YAML or explicitly choose a local file (≤65536 bytes, BOM preserved), validate, preview import, then confirm or cancel. Refresh the catalog, inspect exact catalog/version records and preview enable for the chosen immutable revision. Disable also requires confirmation. Management's legacy inventory remains a separate read-only surface.

Ink: `/skills`, `/skills next`, `/skills show CATALOG [REVISION]`, `/skills versions CATALOG [next]`, `/skills validate-file PATH`, `/skills import-file PATH`, `/skills enable CATALOG REVISION`, `/skills disable CATALOG`. File commands consume the entire literal remainder after one separating space: do not shell-quote or normalize paths. During a review use `y` / `n` or `/skills confirm` / `/skills cancel`; after resolution press Enter/Esc or `/skills dismiss`. Unknown acknowledgments allow `r` or `/skills recover [ORIGINAL_SESSION ORIGINAL_REQUEST]` only. Repeated skill confirmation keys do not authorize a waiting tool.

Machine CLI: `--skill list|versions|show|validate|import|enable|disable|confirm|cancel|lookup`, always with `--session ID` and `--format text|json`. Use `--catalog ID`, exact `--revision ID`, `--manifest-file PATH`, and `--cursor CURSOR` only where applicable. Import/enable/disable require an explicit `--request-id`; confirm/cancel additionally require the preview's `--operation-id`, exact `--digest` and a distinct decision request ID. Unknown results exit nonzero and include `recovery_args` preserving the original `--url`, session and request. No hidden session, POST retry, latest revision fallback or skill execution.

Web persists only recovery IDs in sessionStorage. Ink persists only those IDs in `$XDG_STATE_HOME/personal-agent/skill-<daemon-url-hash>.json` (default `~/.local/state/personal-agent`). Restart restores quarantine before new-session admission, and recovery is original-request GET only. Keep reported IDs if storage fails. File reading refuses invalid UTF-8, oversize, nonregular files and symlinks before transmission; file paths never become server authority.

中文：在已有会话打开独立 Skill 目录面板，粘贴或选择不超过 65536 字节的 UTF-8 文件，校验、预览，再明确确认或取消。保留 BOM 与原始字节；导入 active 清单不会自动启用。必须选择精确不可变版本，启用不代表已评估或可执行。Ink 命令如上，文件路径取分隔空格后的完整原文，不使用 shell 引号。机器 CLI 只接受动作需要的参数，确认必须使用预览的 ID/digest 和新的请求 ID。结果未知时仅按原始 ID 读取恢复，绝不自动重发 POST、创建隐藏会话或调用旧 Skill 执行器。Web 与终端仅保存恢复 ID，不保存清单、令牌或文件路径。

### Internal Skill execution status / 内部 Skill 执行状态

The Go kernel now implements separately reviewed and explicitly confirmed `readonly-workspace-v1` runs using the existing Engine, durable invocation journal and worker ownership. It allows only exact workspace read/search node inputs, with pinned scope/policy/revision, permanent selection revocation, bounded output/retention and offline recovery. This does not change the catalog's immutable historical structural review. Separate public run preview/decision endpoints and TypeScript Web/Ink/machine execution controls are now available as documented below; catalog commands above remain metadata-only. Semantic evaluation remains `not_run`, and Hooks remain unavailable. See `SKILL_CATALOG.md` for the kernel boundaries.

Go 内核现已实现独立预览、明确确认的 `readonly-workspace-v1` 运行，复用现有 Engine、持久调用日志及工作线程所有权。仅允许精确工作区读取/搜索输入，固定范围、策略与修订，支持永久撤销、输出/保留限额及离线恢复。目录中不可变的历史结构审核不会被改写。独立的公开执行预览/确认接口与 TypeScript Web/Ink/机器 CLI 执行控件现已提供，详见下文；上述目录命令仍不运行 Skill。语义评估保持 `not_run`，Hooks 仍未开放。内核边界详见 `SKILL_CATALOG.md`。


### Read-only Skill runs / 只读 Skill 运行

This is a bounded deterministic `readonly-workspace-v1` profile, not a legacy workflow/model bridge. Catalog structural review, enabled revision selection, run eligibility/admission, actual node execution and evaluation are separate. `completed` means the admitted native reads/searches succeeded; evaluation remains `not_run`, verification `not_evaluated`. Only Linux native fixtures are qualified, with no OS-sandbox claim. Hooks and other profiles are unavailable.

Web: open Skill catalog in an existing plan/default/acceptEdits session with supported native workspace binding. Under Read-only Skill execution, enter the exact catalog/revision IDs and a JSON object keyed by the manifest's same-named object inputs. Read accepts `{path:"note.txt"}`; search accepts `{paths:["note.txt"],query:"literal"}`. Preview is content-free; inspect scope/root/policy, compiled native calls, hashes, fixed budgets and expiry, then explicitly confirm. Read the admitted run, or use full-evidence run history/detail and exact stop/resume. No provider probe or hidden session is needed. chat_only and unsupported profiles remain blocked.

Ink: `/skill-run preview CATALOG REVISION INPUTS_FILE`, then `y`/`n` or `/skill-run confirm|cancel`. The file is a bounded regular UTF-8 file; its entire literal path remainder is used, with no shell parsing or server-path authority. Use `/skill-run list [CURSOR]`, `/skill-run show RUN`, `/skill-run stop RUN`, `/skill-run resume RUN`. Dismiss known results with Enter/Esc or `/skill-run dismiss`. Unknown responses permit `r` or `/skill-run recover [ORIGINAL_SESSION ORIGINAL_REQUEST]` only. Repeated confirmation keys do not approve an underlying tool, release its draft, or resend a request.

Machine: `--skill-run preview|confirm|cancel|lookup|list|show|stop|resume --session ID --format text|json`. Preview needs `--catalog ID --revision EXACT_ID --inputs-file PATH --request-id ID`. Confirm/cancel need `--operation-id ORIGINAL_PREVIEW --digest SHA256 --request-id DISTINCT_ID`. Show uses `--run-id ID`; stop/resume also need their own stable request ID. Original operation recovery is `--skill-run lookup --request-id ORIGINAL_ID`. Original control recovery additionally requires `--run-id ORIGINAL_RUN --control cancel|resume`; emitted `recovery_args` include these identities and the original daemon URL. One JSON document is stdout; unknown/unresolved exits 2, local/refused exits 1, known results exit 0 (stale admission exits 1). No automatic POST retry or hidden session.

The authenticated API is `/api/v1/sessions/{session}/skill-runs`: POST `/preview`, POST `/decision`, GET `/operations/{original_request}`, GET collection (`limit` 1..100, exact retained-run cursor), GET `/{run}`. Control recovery is GET `/controls/{run}/{cancel|resume}/{original_request}` over the existing control reservation and exact retained acknowledgment. Missing acknowledgment stays `needs_reconciliation`; GET never reconstructs an outcome or schedules work. Native controls remain `/api/v1/runs/{run}/cancel|resume`. SDK/UI history requests one full-evidence run per page and follows `next_cursor`.

Only recovery identity is persisted: Web sessionStorage `agent-skill-run-operation`, Ink `$XDG_STATE_HOME/personal-agent/skill-run-<daemon-url-hash>.json` (default `~/.local/state`). Run inputs, results, pins, manifests and tokens are not copied into recovery storage. Unknown prepare/confirm/cancel/control restores global session/navigation/approval quarantine before admission. If persistence fails, keep the displayed original IDs. Existing session events carry execution progress; full per-node observations and immutable pin provenance are available in run detail. A read that won the durable claim before revocation may finish; its actual result is retained. Disable/selection-away permanently blocks future nodes, even after re-enable/selection-back. Already observed reads cannot be undone.

Fixed limits: 16 attempts/nodes, 120-second admission deadline, 32 KiB canonical input, 64 KiB serialized result envelope per node, 1 MiB aggregate outputs/artifacts, 512 ordinary plus 64 control/recovery events. Lifetime reservations/retained copies are also bounded; capacity refusals do not silently truncate outputs, create another run or bypass confirmation. Logical payload limits do not promise SQLite/WAL physical size. No purge, real-provider qualification, MCP/network execution, platform widening or semantic evaluation is included.

中文：独立运行入口现已接入 Web、Ink 与机器 CLI，仅支持固定工作区的确定性只读读取/搜索。目录历史结构审核、选中版本、运行资格、准入、节点实际结果与语义评估严格分离。每次运行均需精确版本、JSON 输入、独立预览与明确确认；不调用模型，不创建隐藏会话。完成只代表原生读取/搜索成功，evaluation 始终为 `not_run`，verification 为 `not_evaluated`。未知响应及重载/重启后保持全局隔离，仅按原始会话/请求/运行/动作 GET 读取恢复，绝不重发 POST。只保存恢复标识，不保存输入、令牌、清单或结果。停用或切换版本会永久撤销后续节点；已完成的读取不能撤回，已有证据保留。限额、保留容量、支持平台及 Hooks 边界如上，不能将只读运行误认为沙箱或通用 Skill 执行器。
