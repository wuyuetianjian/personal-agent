# P14 v1.0 GA Release Requirements

P14 turns the local-first Personal Agent into a distributable v1.0 package for macOS Apple Silicon workstations and Linux servers. This document extends `docs/p14_pre_completion_requirements.md`; it does not add new Runtime architecture.

## Scope

- Define release metadata for application, config schema, database schema, Skill manifest schema, and API version.
- Build release archives for `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Record version, commit, build date, Go version, and SHA256 checksums.
- Provide bootstrap install and upgrade scripts that create config/data directories, validate config, back up data, run migrations, and support health checks.
- Provide systemd and macOS LaunchAgent examples with local data/log paths and conservative service limits.
- Provide safe production config examples without inline secrets.
- Set the default local Ollama model in `configs/config.example.yaml` to the locally available `qwen3.8:27b-mlx` model.
- Wire configured OpenAI-compatible local providers into the Runtime chat path so the interactive CLI uses the selected local model.
- Wire the configured Leader OpenAI-compatible provider into `BoundedModelPlanner` when `agent.planner.enabled` is true and the selected model supports `chat` and `json_schema`.
- Provide quickstart, user, administrator, security/threat model, troubleshooting, upgrade, RC E2E, soak, performance, and recovery drill documentation.
- Provide a release checklist command that verifies tests, vet, build, build matrix, checksums, and required release documents.
- Add migration compatibility coverage for upgrading from a supported P13/P14-pre schema state to the latest embedded migrations.

## Non-Goals

- Package manager publishing.
- Windows release support.
- External CI provider configuration.
- Production-grade kernel or browser isolation beyond documented service hardening examples.

## Functional GA Runtime Brain Slice

`BLOCK-01` closes the first Functional GA blocker by composing the configured local Leader model into the bounded workflow planner:

```text
Config
  -> Model Registry
  -> agent.leader.model_id
  -> OpenAI-compatible ChatProvider
  -> BoundedModelPlanner
  -> Runtime.Planner
```

The planner is enabled by `agent.planner.enabled` and bounded by `agent.planner.max_nodes`. It remains fail-closed: disabled or unavailable providers leave `Runtime.Planner` unset, unknown capabilities are rejected, disabled capabilities are denied, and invalid DAGs such as cycles fail validation before workflow creation.

`HIGH-01` and `HIGH-02` add model-backed local reasoning and synthesis on top of the same configured private provider. Reasoning receives the task, compressed evidence references, and constraints, and returns claims, a decision summary, confidence, evidence IDs, and provider usage without storing raw chain-of-thought. Synthesis receives verified evidence context and returns the final answer while excluding unsupported claims and surfacing conflicts when evidence is insufficient. If no configured private provider is available, both nodes keep the existing deterministic local fallback.

## Acceptance

- `go test ./...` passes.
- `make build` passes.
- `make smoke` passes.
- `make release-build` creates all required platform archives under `dist/`.
- `make checksums` creates `dist/SHA256SUMS`.
- `pachat version` prints all version model fields.
- `pachat release check --quick` passes in a local development checkout.
- README contains English and Chinese P14 release notes.
- `Runtime.Build()` creates both `Runtime.ChatProvider` and `Runtime.Planner` for the default `qwen3.8:27b-mlx` local planner configuration.
- Planner tests reject unknown capabilities, disabled capabilities, cycles, and node counts over `agent.planner.max_nodes`.
- Runtime workflow tests verify model-backed `reasoning.local` and `synthesis.local` execution with provider usage and final-answer propagation from the synthesis checkpoint.
