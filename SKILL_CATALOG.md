# Rebuilt skill catalog (kernel, API and metadata clients)

Metadata-only catalog kernel and public API/shared-client contract, plus the separately reviewed internal deterministic run kernel described below. The HTTP/generated protocol, strict SDK and durable controller are now connected to Web, Ink and machine CLI. All client actions remain metadata-only. No push, merge or release is part of this increment.

## Authority and state

The kernel accepts explicit manifest **text**, never a server path, URL, archive, directory, package, script or external discovery source. `SkillIntent` has only `request_id`, `kind`, `manifest_text`, `catalog_id`, `revision_id`. Import requires text and forbids both IDs; enable requires both IDs and forbids text; disable requires catalog ID and forbids text/revision ID. No scope/project override or evaluation result is accepted.

Catalog metadata is service-local, not scope-isolated knowledge. Any API-bound session can read it. Operations/recovery belong to their original session only. Default, acceptEdits, plan and chat_only can prepare and explicitly confirm these metadata changes. The session's stored policy/binding and scope are frozen into the digest and rechecked at confirmation. No root filesystem access or knowledge indexing authority is involved.

A catalog has one immutable ID derived from the declared skill ID, one immutable random generation, a monotonic integer revision, and state `disabled` or `enabled`. `disabled` has empty selected revision. `enabled` selects one exact immutable revision. New catalog import is always disabled, regardless of manifest status. New version import increments catalog revision without changing selection. Enable and disable increment revision only when state/selection changes. No semantic-version comparison and no `latest` resolution exist. Version strings are opaque, exact, case-sensitive identities.

Revisions retain exact original bytes and SHA-256, typed canonical JSON and SHA-256, parser/review versions, full structural review and server-created provenance (text source, operation ID, session ID, descriptive scope ID, import time). Unique catalog + declared version is enforced. Identical bytes for the same version and same current review are `no_op`; different bytes or an old review conflict. There is no in-place rereview or replacement. Older-review activation fails closed until a new version is explicitly imported under the current profile.

**Enabled alone grants neither evaluation nor execution.** The immutable catalog structural review is historical metadata, separate from run eligibility. Every capability and role target, known-looking or unfamiliar, is displayed unchanged as `unsupported`. Evaluation is `not_run`; execution is `unavailable`, reason `bridge_not_implemented`. Permission/privacy/budget declarations are descriptions, not grants. No `internal/skill` loader/normalizer/store/registry, workflow runtime, Jobs, provider, tool, process, MCP or RAG executor is called.

## Strict manifest schema

Parser profile: `strict-manifest-v1`. Review profile: `catalog-no-execution-v1`.

Fields mirror `internal/skill/types.go`'s `Manifest` data, but exclude its generated `CreatedAt`. Dedicated types live in `internal/interaction/skill_manifest.go`. The parser does not call legacy validation or normalization.

Every field below is required except node `capability`, `role`, and `depends_on`. Exactly one of capability/role must be nonempty. Empty containers must be explicit (`inputs: {}`, `capabilities: []`). No default status, budget, privacy or permission is invented.

- `id`: ASCII identity, 1–128 bytes.
- `version`: opaque ASCII identity, 1–64 bytes. Numeric-looking versions must be quoted; no YAML scalar-to-string coercion.
- `name`: nonblank UTF-8, 1–256 bytes; `description`: UTF-8, 0–4096 bytes.
- `status`: draft, active, deprecated, disabled. Descriptive only.
- `inputs`: map, up to 32 names (ASCII identity, 1–64 bytes); each value is exactly `{type: string|integer|number|boolean|object|array}`. These are structural declarations, not an execution/input validator.
- `requires`: exactly `{capabilities: [...]}`, at most 64 unique ASCII identities, each 1–128 bytes. Must equal the set of workflow capability targets, not merely a superset; roles do not create capability grants.
- `permissions`: exactly `{max_level: read_only|write}`. These are the safe named levels present in legacy capability ranking. Legacy denied admin/destructive and undefined levels are rejected, not normalized.
- `privacy`: exactly `{max_external_trust: local_private|local_sandboxed|trusted_remote|public_remote}`. Matches source trust-level spellings but confers no outbound permission.
- `budget`: exactly `{max_iterations: integer 1..1000, max_tool_calls: integer 0..10000}`. No actual execution budget is issued.
- `verification`: exactly `{min_coverage: finite number 0..1, allow_conflicts: boolean}`. No evaluation occurs.
- `workflow`: exactly `{nodes: [...]}`, 1–64 nodes.
- Node: `id` ASCII identity 1–64 bytes, exactly one `capability` or `role` ASCII identity 1–128 bytes, optional `depends_on` array of at most 64 distinct existing node IDs. Node IDs must be unique. All dependency cycles, including multi-node cycles, are rejected.

ASCII identities use `[A-Za-z0-9._-]`, are nonempty and may not be `.` or `..`. Strings are checked after decoding as well as the exact input's UTF-8 check. NUL is forbidden. Raw artifact maximum is 65,536 bytes. One YAML document only, including rejection of trailing empty documents; duplicate and unknown keys at every level, nulls, anchors, aliases, explicit tags (including standard tags), YAML merges, malformed Unicode and scalar coercions are rejected. YAML syntax tree maximum is 2,048 visited nodes and depth 16, counting mapping keys/values from root depth 1; sequence length is at most 64. Shape checks may reject structurally invalid nesting before the numeric depth limit. The 64 workflow-node limit is separate from syntax nodes.

Canonical JSON preserves declared ordering and values, uses Go's deterministic struct/map JSON encoding, and does not change status or sort workflow arrays. Semantically similar but byte-different artifacts still conflict at the same declared version.

## Store entrypoints for 7F1B

- `ValidateSkillManifest(text) (SkillReview, error)`: pure bounded structural parsing, no DB or external calls.
- `Store.PrepareSkill(ctx, session, SkillIntent) (SkillOperation, error)`.
- `Store.DecideSkill(ctx, session, OperatorDecision) (SkillOperation, error)`; decision fields are request ID, original operation ID, exact preview digest, confirm/cancel.
- `Store.SkillOperation(ctx, session, originalRequestID)`: recover either the prepare or decision request. The prepare ID follows its final result; a decision ID returns its exact saved result. Pending expiry is an effective read state without DB mutation.
- `Store.ListSkills(ctx, session, afterID, limit)`: summary only.
- `Store.SkillCatalogItem(ctx, session, catalogID)`: catalog summary, no arbitrary latest revision.
- `Store.SkillVersions(ctx, session, catalogID, afterRevisionID, limit)`: summaries, no raw/canonical/review blobs.
- `Store.SkillRevision(ctx, session, catalogID, exactRevisionID)`: one bounded original artifact, structural review/canonical representation and provenance.

List/version pagination is ascending lexicographic ID, exclusive `after`, limit 1..50; pass last returned ID as cursor. Empty results are `[]`. There is no semantic sort and no automatic page drain. IDs/request IDs/cursors use the ASCII identity grammar, at most 128 bytes. Missing/foreign recovery does not reveal another session's result. Detail requires a revision belonging to the specified catalog.

`ErrInvalid` denotes invalid shapes/fields; pure validation uses `ErrBudget` for artifact byte overflow, while oversized `SkillIntent` is invalid before parsing. `ErrBudget` also denotes capacity refusal; `ErrConflict` denotes request reuse, foreign/nonpending/bad-digest decisions, conflicting version bytes/review or corrupt revision evidence. Missing catalog/revision reads return `sql.ErrNoRows`. Staleness, cancelled confirmation and expiry return a recorded operation state rather than mutating a target. Infrastructure/transaction errors roll back and leave the pending original recoverable; they do not imply retry authority.

B must use strict JSON, namespace/protocol/auth/Host/Origin gates, capped body sizes accounting for escaped 64 KiB text, and read/decision error mappings. Do not expose generic YAML `new_value` actions or eval fields. Display exact review, hashes, provenance and selection consequences; render all manifest text as untrusted text, never executable markdown/HTML. Do not mislabel `unsupported` targets as verified capability availability. Do not present selection as run authorization. No automatic POST retry: on unknown acknowledgment, GET the original request ID, retain the draft/digest until resolved, and distinguish missing result from known failure. The B1 public API/SDK/controller contract is documented below. The B2 catalog clients below consume this contract.

## Persistence, digest, transaction and bounds

Migration `0029_immutable_skill_catalog.sql` creates five independent tables: catalog, revisions, operations, requests, history. Nothing imports legacy skill rows. Exact-prefix schema recognition automatically incorporates this migration through the embedded catalog reference; missing/modified triggers or gapped migration histories fail closed.

Preview digest binds session/scope/policy, kind, catalog ID, expected generation/revision/state/selection, intended state/selection, exact revision ID, parser/review versions, full structural review and its hash, exact artifact/content hashes, capability/role/privacy/permission/budget declarations, and expiry (10 minutes). Confirmation reparses the retained exact artifact with the current profile, checks the saved review and current immutable target, and applies under the same serialized SQLite transaction as final operation, request result, history and audit. Cancel/expired/stale/replay mismatch never change catalog data.

Revisions/history/requests/preview/staged bytes are guarded against update/delete/replace. Terminal operations are single-use; catalog identity/generation cannot change or be deleted/replaced; updates must increase revision exactly once and enabled selection must reference a revision belonging to that catalog. No-op adds a result/audit but no catalog revision/history. Disabling an already disabled catalog has no artifact/revision to review; its review still explicitly reports evaluation not_run and execution unavailable, with empty artifact fields. New catalog/revision and each changed selection append history. Audit labels contain stable IDs/status/notices rather than raw manifest text.

Hard application limits, rechecked inside serialized write transactions:

- 256 retained catalogs, 1,024 retained immutable revisions, 4,096 retained operations (at most two request records per operation through these APIs).
- 128 live pending operations globally, 64 per session; expired pending evidence is still retained and counts against total retention.
- 64 MiB retained revision payload accounting (raw + canonical + review + provenance); a new revision reserves 2 KiB for provenance in the capacity check.
- Separate 64 MiB operation retention reservation: `4 * (preview UTF-8 bytes + 4096) + staged bytes` per operation. Covers preview/current result/decision result/staging and bounded identity/audit/history overhead conservatively; not a total SQLite file/page/WAL size guarantee.
- SQLite per-blob guards: raw/staged 64 KiB, canonical 128 KiB, review 256 KiB, provenance 2 KiB, operation preview/result/request result 512 KiB. Catalog ID/declared ID/generation/selection at most 128 bytes; declared version 64 bytes, parser/review version 64 bytes, SHA-256 hex columns 64 characters.

At capacity, admission fails; no automatic purge or retention deletion route exists. Existing evidence remains recoverable. Limits are deliberate v1 scope, not an unlimited skill library.

## 中文摘要

当前已提供独立的 Skill 目录后端内核、HTTP API、共享 TypeScript SDK、可恢复控制器及 Web/Ink/机器 CLI 目录入口。另有下文所述内部确定性只读执行内核；独立只读运行接口和 Web/Ink/机器 CLI 现已提供，并支持有界、会话拥有的声明式拒绝/审计 Hooks；可执行/通用 Hooks 仍未开放。`enabled` 只表示选定一个不可变版本，目录的历史结构审核仍保留 `not_run` 与 `unavailable / bridge_not_implemented`，不代表新的运行资格判断。旧 Skill 表、registry、runtime、Jobs、工具、模型和全局 RAG 不作为此目录的权限来源。

只接收明确的 UTF-8 YAML 文本，最多 64 KiB、单文档。字段、类型、枚举、字符串长度、集合大小和依赖图均严格校验；拒绝重复键、未知键、锚点、别名、显式 tag、隐式类型转换、无效 Unicode、循环依赖与歧义节点。上方列出了完整版本化 schema，保留每个目标原文并标记 unsupported，不猜测执行能力。

导入预览仅保存私有原文，确认前不建立目录/版本。相同版本完全相同字节且 review 相同为 no_op；不同字节或旧 review 冲突。导入 active 不激活；新增版本不改变选择；启用必须指定精确版本，停用不改写历史版本。目录为服务本地元数据，并不提供工作区/项目/检索/执行授权；plan 和 chat_only 可在明确确认后操作这些元数据。

摘要绑定原始会话、scope、policy、目标代际/修订/状态、精确原文和 review、声明能力/隐私/预算及有效期。确认重核并在一个事务中写入目录、不可变版本、历史、结果、请求账本和审计。取消、过期、陈旧、跨会话、重复冲突均不产生目标修改。未知响应必须按原始请求 ID 查询，不自动重发 POST。数量与保留字节有硬上限，不自动删除证据。B1 提供 HTTP/共享 SDK/控制器；B2 Web/Ink/CLI 公开入口已接入，边界见下文。


## Public B1 transport and shared client contract

All routes use the existing loopback Host/Origin/authentication gates. POSTs require the existing `X-Agent-Protocol` header and JSON content type (the protocol header uses the existing exact `InteractionProtocolFlavor` value). The canonical root is `/api/v1/sessions/{session}/skills`. Only a stored API session binding is read: no `/status` preflight, live Resolve, workspace traversal, registry, provider, process, tool or execution call. Both `plan` and `chat_only` can explicitly confirm this service-local metadata. The generic operator route is unchanged; its legacy skill inventory is a separate read-only catalog.

- `POST /validate`: exactly `manifest_text`; returns a structural `SkillReview`. It creates no operation/request/catalog rows.
- `POST /preview`: exactly `request_id`, `kind`, `manifest_text`, `catalog_id`, `revision_id`; returns `SkillOperation`. Every field is required, including empty strings for irrelevant fields. No filename, URL, scope, evaluation or authority field.
- `POST /decision`: exactly `request_id`, `operation_id`, `digest`, `decision`; confirm/cancel the exact preview. A distinct decision request ID is required by the shared client.
- `GET /operations/{original_request_id}`: session-owned recovery for either original prepare or decision ID. Unknown/missing results do not authorize another POST.
- `GET /`: `SkillCatalogPage` (use root without the trailing slash).
- `GET /{catalog_id}`: exact `SkillCatalogEntry`, never a latest version.
- `GET /{catalog_id}/versions`: `SkillVersionPage`.
- `GET /{catalog_id}/versions/{revision_id}`: exact `SkillRevisionRecord`, including original text, immutable structural review and provenance.

List and version pages alone accept `limit` (canonical decimal 1..50; default 20) and `cursor`. Cursor is canonical unpadded base64url of `[session_id, catalog_id_or_empty, last_id]`, bound to the original session and resource. No automatic page drain or semantic latest lookup. Empty lists are arrays. All other query fields, repeated values, malformed queries and unexpected query strings are refused. Encoded path aliases and trailing slash aliases are refused. These APIs use summary pages and bounded exact details.

POST body cap is `6 * 65536 + 4096 = 397312` encoded bytes, supporting worst-case valid JSON escaping without unbounded reads. Decoded manifest text remains at most 65536 UTF-8 bytes. Duplicate/unknown/missing/non-string/null fields, malformed raw UTF-8, lone escaped surrogates and trailing JSON are refused. Stable error codes are `skill_invalid_request` (400), `skill_request_conflict` (409), `skill_not_found` (404), and `skill_outcome_unknown` (500), alongside existing session/auth/protocol envelope codes. No raw manifest is echoed in errors.

Generated TypeScript/JSON Schema includes the complete nested DTO graph and exact nullable fields in zero-artifact reviews / omitted dependencies. Strict decoders reject wrong ownership, unknown fields at every level, missing bounds, invalid enum/hash/ID/state combinations, invalid dependency graphs, unsupported profiles and any evaluated/executable claim. Zero-artifact disable review is accepted only for disabling an already disabled catalog, not as a valid executable manifest. Response JSON has a 4 MiB byte cap before UTF-8 decoding, duplicate-key/Unicode checks, a depth cap of 24 and 10000 value nodes. Summary/detail decoders then enforce their smaller typed bounds. Canonical and artifact hashes are verified; review hashes preserve exact Go serialization, including negative zero and exponent spelling.

`AgentClient` methods: `validateSkill`, `prepareSkill`, `decideSkill`, `skillOperation`, `listSkills`, `skillCatalog`, `skillVersions`, `skillRevision`. Metadata methods make direct requests with no provider/status handshake and never retry a POST. `SkillNotSentError` distinguishes local validation/auth-header preparation before invoking fetch from an uncertain invocation or malformed acknowledgment. Response decoding failure after a POST stays unknown. Exact filenames remain client-only in the B2 file readers; the server never accepts them.

`SkillOperations` holds a frozen input and immutable exact preview in memory. It persists only `{session_id, request_id, operation_id}`, validates unique local IDs, restores as unknown, and offers GET-only original-request recovery. Unknown stays locked through missing/malformed responses. Definite decision conflicts recover the original operation, not the unused attempted decision ID. Known committed results survive storage failure/read refresh. A new request clears previous result evidence. Generation guards fence navigation; the optional last `SessionController` constructor argument integrates the controller into message, queue, job, metadata and approval quarantine. Both applications now supply that argument and fence catalog/navigation/mutation controls.

`skillExact` and `formatSkillOperation` preserve untrusted literal controls with visible escapes. They do not render HTML/Markdown or execute links. Full manifests, structural profiles, declared capabilities/roles/privacy/budgets, target selection and hashes remain inspectable as text. Production Web/Ink/CLI commands now use that controller and SDK. The existing `pachat skill` commands must not be used as adapters.

### Catalog clients (B2) / 目录客户端

Web has a separate literal-text catalog panel, bounded paste/file import, validation, catalog and version pages, exact detail, explicit preview/confirm/cancel, and global recovery after reload. Ink uses `/skills` commands and a review-focused key/command buffer separate from ordinary drafts. Machine `--skill` actions require an existing session and strict compatible flags; no action creates a hidden session or silently selects the latest version. See README for complete grammar and local recovery paths.

Both authenticated clients own one SkillOperations instance, shared across session navigation and passed into SessionController. Unknown responses retain quarantine; restored IDs are loaded before new session admission. Pending confirmation/cancellation remains usable. A known committed result is not reclassified when a later list refresh or persistence update fails. Untrusted manifests/reviews are literal escaped evidence, not Markdown, HTML or terminal instructions.

Local readers preserve UTF-8 bytes including BOM, reject invalid UTF-8 and >65536 bytes, and never transmit a path as server authority. The Node reader opens nonblocking/no-follow, verifies a regular file, and reads at most 65537 bytes before deciding. Browser files are explicitly selected File objects and are size-checked before decoding. Persistence contains only bounded original session/request/operation IDs. Original-request GET is the only recovery; the CLI returns the original daemon's recovery argument array and a nonzero status for refusal or uncertainty.

中文：Web、Ink、机器 CLI 已接入目录元数据生命周期；旧管理目录保持只读且分离。输入和审核原文只作字面证据，保留 BOM，拒绝超限/无效 UTF-8；本地路径不成为服务端权限。恢复只保存原始 ID，重载/重启后先隔离，再按原始请求 GET 查询。确认和取消仅作用于目录，不会批准其他工具或发送保留草稿。选中版本本身仍不授权执行；内部只读运行内核见下文，独立只读运行接口和客户端现已提供，并支持有界、会话拥有的声明式拒绝/审计 Hooks；可执行/通用 Hooks 仍未开放。

## Internal deterministic read-only kernel / 内部确定性只读内核

The internal Go kernel now supports a separate `readonly-workspace-v1` run review and explicit confirmation. Catalog enablement alone never starts a run. Public Skill-run HTTP endpoints, generated TypeScript clients and actual Web/Ink/machine execution controls are available as a separate lifecycle; the immutable catalog structural review remains unchanged.

内部 Go 内核新增独立的 `readonly-workspace-v1` 执行预览与确认。目录启用不会自动执行；Skill 运行 HTTP 接口、生成的 TypeScript 客户端以及 Web、Ink、机器 CLI 执行控件现已作为独立生命周期提供；不可变的目录结构审核保持不变。

Each workflow node must have one same-named object input. `workspace.read` accepts exactly `{path}` and `workspace.search` accepts exactly `{paths, query}`. The server injects the immutable workspace scope. Dependencies only order successful completion; they do not interpolate arguments or pass outputs. Roles, models, scripts, MCP, writes, semantic verification, executable/general Hooks and legacy workflow execution are unavailable. Session-owned declarative deny/audit Hooks for this profile are described below. Positive minimum coverage blocks this profile; evaluation remains `not_run`, including completed reads.

每个工作流节点必须对应一个同名 object 输入。仅允许 `workspace.read` 的 `{path}` 和 `workspace.search` 的 `{paths, query}`；服务端注入不可变工作区范围。依赖仅控制成功完成的顺序，不插值、不传递输出。角色、模型、脚本、MCP、写入、语义验证、可执行/通用 Hooks 与旧工作流不受支持；本配置的会话声明式拒绝/审计 Hooks 见下文。非零最低验证覆盖率会阻止执行；读取完成也不代表验证通过。

Execution uses the existing Engine, invocation journal, session serialization and service worker limits. The preview is valid for ten minutes. Admission pins the selected revision, content, plan, input, workspace and policy identities. Disabling or selecting another revision permanently revokes future nodes of pinned runs. Reads admitted before revocation can finish; their observations remain visible. Re-enabling does not revive revoked runs. Unknown invocation outcomes require reconciliation and are never automatically retried.

执行复用现有 Engine、调用日志、会话串行化与服务工作线程限制。预览十分钟有效；确认后固定修订、内容、计划、输入、工作区及策略身份。禁用或切换修订会永久撤销已有运行的后续节点；撤销前已获准的读取可以完成，已观察结果会保留。重新启用不会恢复旧运行；结果未知的调用需要核对，不自动重试。

Hard limits: 16 nodes/attempts, 120 seconds from admission, 32 KiB canonical input, 64 KiB full serialized ToolResult per node, and 1 MiB aggregate raw artifacts plus output envelopes. Durable event limits are 512 ordinary plus 64 control/recovery events, with four slots reserved for indispensable outcomes/cancellation/reconciliation. Lifetime retention is bounded without automatic purge: 256 runs, 1024 operations, 2048 request records, 64 global/16 per-session live previews, 64 MiB operation reservations and 256 MiB execution reservations (up to 8 MiB per live run). These are logical payload limits, not promises about SQLite/WAL physical size. Native platform restrictions remain fail-closed; this profile is not an OS sandbox.

硬限制为 16 个节点/尝试、确认起 120 秒、32 KiB 规范输入、每节点完整序列化 ToolResult 64 KiB、原始产物加输出封装合计 1 MiB。事件限额为 512 条普通事件加 64 条控制/恢复事件，其中四条保留给必要结果、取消及核对证据。生命周期保留限制：256 次运行、1024 个操作、2048 条请求记录、全局 64/每会话 16 个有效预览、操作预留池 64 MiB、执行预留池 256 MiB（每个运行最多 8 MiB）。不自动清除历史；这些是逻辑负载限制，不是 SQLite/WAL 文件大小承诺。原生平台限制仍然关闭式拒绝，本能力不是操作系统沙箱。

Existing cancel/resume acknowledgments for Skill runs share the same request/copy limits. Capacity is reserved durably before the control action. A lost acknowledgment never authorizes replay, and terminal controls do not reopen the execution pool.

Skill 运行的现有取消/恢复确认记录也共享请求及副本限额；控制动作前先持久预留容量。确认响应丢失不代表允许重放，终态控制也不会重新打开执行预留池。


### Read-only Skill runs / 只读 Skill 运行

This is a bounded deterministic `readonly-workspace-v1` profile, not a legacy workflow/model bridge. Catalog structural review, enabled revision selection, run eligibility/admission, actual node execution and evaluation are separate. `completed` means the admitted native reads/searches succeeded; evaluation remains `not_run`, verification `not_evaluated`. Only Linux native fixtures are qualified, with no OS-sandbox claim. Session-owned declarative deny/audit Hooks for this profile are available as described below; executable/general Hooks and other execution profiles remain unavailable.

Web: open Skill catalog in an existing plan/default/acceptEdits session with supported native workspace binding. Under Read-only Skill execution, enter the exact catalog/revision IDs and a JSON object keyed by the manifest's same-named object inputs. Read accepts `{path:"note.txt"}`; search accepts `{paths:["note.txt"],query:"literal"}`. Preview is content-free; inspect scope/root/policy, compiled native calls, hashes, fixed budgets and expiry, then explicitly confirm. Read the admitted run, or use full-evidence run history/detail and exact stop/resume. No provider probe or hidden session is needed. chat_only and unsupported profiles remain blocked.

Ink: `/skill-run preview CATALOG REVISION INPUTS_FILE`, then `y`/`n` or `/skill-run confirm|cancel`. The file is a bounded regular UTF-8 file; its entire literal path remainder is used, with no shell parsing or server-path authority. Use `/skill-run list [CURSOR]`, `/skill-run show RUN`, `/skill-run stop RUN`, `/skill-run resume RUN`. Dismiss known results with Enter/Esc or `/skill-run dismiss`. Unknown responses permit `r` or `/skill-run recover [ORIGINAL_SESSION ORIGINAL_REQUEST]` only. Repeated confirmation keys do not approve an underlying tool, release its draft, or resend a request.

Machine: `--skill-run preview|confirm|cancel|lookup|list|show|stop|resume --session ID --format text|json`. Preview needs `--catalog ID --revision EXACT_ID --inputs-file PATH --request-id ID`. Confirm/cancel need `--operation-id ORIGINAL_PREVIEW --digest SHA256 --request-id DISTINCT_ID`. Show uses `--run-id ID`; stop/resume also need their own stable request ID. Original operation recovery is `--skill-run lookup --request-id ORIGINAL_ID`. Original control recovery additionally requires `--run-id ORIGINAL_RUN --control cancel|resume`; emitted `recovery_args` include these identities and the original daemon URL. One JSON document is stdout; unknown/unresolved exits 2, local/refused exits 1, known results exit 0 (stale admission exits 1). No automatic POST retry or hidden session.

The authenticated API is `/api/v1/sessions/{session}/skill-runs`: POST `/preview`, POST `/decision`, GET `/operations/{original_request}`, GET collection (`limit` 1..100, exact retained-run cursor), GET `/{run}`. Control recovery is GET `/controls/{run}/{cancel|resume}/{original_request}` over the existing control reservation and exact retained acknowledgment. Missing acknowledgment stays `needs_reconciliation`; GET never reconstructs an outcome or schedules work. Native controls remain `/api/v1/runs/{run}/cancel|resume`. SDK/UI history requests one full-evidence run per page and follows `next_cursor`.

Only recovery identity is persisted: Web sessionStorage `agent-skill-run-operation`, Ink `$XDG_STATE_HOME/personal-agent/skill-run-<daemon-url-hash>.json` (default `~/.local/state`). Run inputs, results, pins, manifests and tokens are not copied into recovery storage. Unknown prepare/confirm/cancel/control restores global session/navigation/approval quarantine before admission. If persistence fails, keep the displayed original IDs. Existing session events carry execution progress; full per-node observations and immutable pin provenance are available in run detail. A read that won the durable claim before revocation may finish; its actual result is retained. Disable/selection-away permanently blocks future nodes, even after re-enable/selection-back. Already observed reads cannot be undone.

Fixed limits: 16 attempts/nodes, 120-second admission deadline, 32 KiB canonical input, 64 KiB serialized result envelope per node, 1 MiB aggregate outputs/artifacts, 512 ordinary plus 64 control/recovery events. Lifetime reservations/retained copies are also bounded; capacity refusals do not silently truncate outputs, create another run or bypass confirmation. Logical payload limits do not promise SQLite/WAL physical size. No purge, real-provider qualification, MCP/network execution, platform widening or semantic evaluation is included.

中文：独立运行入口现已接入 Web、Ink 与机器 CLI，仅支持固定工作区的确定性只读读取/搜索。目录历史结构审核、选中版本、运行资格、准入、节点实际结果与语义评估严格分离。每次运行均需精确版本、JSON 输入、独立预览与明确确认；不调用模型，不创建隐藏会话。完成只代表原生读取/搜索成功，evaluation 始终为 `not_run`，verification 为 `not_evaluated`。未知响应及重载/重启后保持全局隔离，仅按原始会话/请求/运行/动作 GET 读取恢复，绝不重发 POST。只保存恢复标识，不保存输入、令牌、清单或结果。停用或切换版本会永久撤销后续节点；已完成的读取不能撤回，已有证据保留。限额、保留容量、支持平台及 Hooks 边界如上，不能将只读运行误认为沙箱或通用 Skill 执行器。

Declarative Hook extension / 声明式 Hook 扩展：`PrepareSkillRunWithHooks` creates a schema-2 review with an immutable complete policy snapshot, while the compiler's `SkillRunReview` stays unchanged. Session-owned policies require separate import and enable/disable confirmation. Material selection changes revoke all nonterminal session Skills, including legacy/no-policy pins; disable/re-enable cannot revive them. Receipts expose only bounded rule/node/capability/status identity, never paths, tool arguments/results or user text. Known denial remains `not_started`, null result and empty node-result proposal ID. Public Hook API and Web/Ink/machine commands now use coordinated strict v1/v2 clients: new run previews/admissions include the schema-2 snapshot, and retained schema-1 reads remain supported. / 运行预览包含完整不可变策略快照，不修改编译器结构审核。策略导入和启用/停用分别确认；实际选择变化永久撤销所有未终结运行，包括旧版无策略运行，重新启用不能恢复它们。回执仅包含有界规则、节点、能力和状态标识，不包含路径、工具参数/结果或用户文本。明确拒绝保持 `not_started`、空结果和空节点结果 proposal ID。公开 Hook API 和 Web/Ink/机器命令现已接入严格 v1/v2 客户端；新运行预览/准入包含 schema-2 快照，保留的 schema-1 历史记录仍可严格读取。

## Session declarative Hooks / 会话声明式策略

The public read-only run API now creates schema-2 previews/pins with an exact immutable `declarative-hooks-v1` snapshot; strict retained schema-1 reads remain supported. The `skill_readonly_v1` execution kind and context exclusion are unchanged. Policies cannot be named by manifests, copied across session ownership, hot-swapped into an admitted run, used to widen permission or used as execution callbacks. Old pending previews confirm only as stale `hook_review_required`; cancellation remains explicit.

规范接口：`GET /api/v1/sessions/{s}/hooks`，`GET /hooks/revisions?limit=1..32&cursor=...`（默认 20），`GET /hooks/revisions/{revision}`，`POST /hooks/preview`，`POST /hooks/decision`，`GET /hooks/operations/{request}`；缩写均接在相同会话路径后。运行收据为 `GET /api/v1/sessions/{s}/skill-runs/{run}/hooks`。准备请求只有 `request_id,kind,revision_id,policy_text` 四项，原始请求体上限 53248 字节；决定只有 `request_id,operation_id,digest,decision` 四项，上限 4096 字节。确认与取消均要求不同于准备 ID 的稳定请求 ID。

Read-only control identity remains `GET /api/v1/sessions/{s}/skill-runs/controls/{run}/{cancel|resume}/{request}`, recovering the original `POST /api/v1/runs/{run}/{cancel|resume}`. A decode/hash/version error never authorizes model-control fallback. Missing receipts and lost detail reads stay in shared ID-only GET recovery quarantine. Full rule sets appear in run reviews; receipt results expose only sanitized IDs/status/matches, never paths, tool arguments/results or full policy copies.

导入是惰性的；选择改变与运行授权分离。任何实质选择改变永久撤销全部未结束的同会话技能运行，已完成的真实观察保留。确认相同选择不增加代数。禁止执行脚本、正则/表达式、文件/网络/环境/凭据读取、自动批准、修改参数、替换工具或重试。所有语言、字节、保留容量及 Linux 证据边界见 README 的 Declarative Hooks 段；这些限制不代表 OS 沙箱、物理数据库大小或语义验证。
