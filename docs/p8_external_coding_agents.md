# P8 External Coding Agents

P8 adds a library-level External Coding Agent runtime for developer workflows. The first implementation keeps the Personal Agent Runtime as the orchestrator and treats Codex CLI and Claude Code CLI as governed execution backends, not model providers and not replacement runtimes.

## Scope

- Add `internal/codingagent` contracts, registry, policy selection, safe process execution, workspace isolation, CLI adapters, environment sanitization, and evidence collection.
- Support Codex CLI and Claude Code CLI through independently configurable adapters.
- Keep execution location and inference trust separate. A locally executed CLI can still use public remote inference.
- Run write tasks in isolated Git worktrees by default.
- Require real evidence such as Git diff, changed files, commands, and tests before a coding agent result can be treated as successful.
- Leave CLI/API automatic external coding execution for a later Runtime integration increment.

## Behavior

External Coding Agents receive bounded coding task slices. Each request declares repository path, task ID, node ID, prompt, privacy class, required capabilities, write permission, and optional test commands. The registry selects the first enabled healthy backend that satisfies capability and repository privacy policy. Disabled, unhealthy, and privacy-incompatible backends are skipped.

Codex and Claude credentials remain owned by their CLIs. The Personal Agent never reads credential stores or records API keys/tokens. Environment variables passed to external processes are reduced to a minimal allowlist plus explicit adapter-provided variables.

## Configuration

`coding_agents` config declares per-backend enablement, binary path, timeout, concurrency, execution location, inference trust, privacy policy, and capabilities. Defaults must not be inferred from executable paths. Public remote inference can be denied for confidential repositories by policy.

## Validation

P8 validation covers:

- registry register/lookup, capability matching, fallback, disabled backend, and health failure;
- process completion, timeout, context cancellation, and child process termination;
- worktree isolation and diff collection without mutating the source worktree;
- Codex and Claude adapter request mapping through mock binaries;
- repository privacy denial for confidential repositories and public remote inference;
- secret stripping from external process environments;
- evidence validation requiring non-empty diff or test evidence instead of self-reported completion.
