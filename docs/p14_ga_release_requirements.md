# P14 v1.0 GA Release Requirements

P14 turns the local-first Personal Agent into a distributable v1.0 package for macOS Apple Silicon workstations and Linux servers. This document extends `docs/p14_pre_completion_requirements.md`; it does not add new Runtime architecture.

## Scope

- Define release metadata for application, config schema, database schema, Skill manifest schema, and API version.
- Build release archives for `darwin/arm64`, `linux/amd64`, and `linux/arm64`.
- Record version, commit, build date, Go version, and SHA256 checksums.
- Provide bootstrap install and upgrade scripts that create config/data directories, validate config, back up data, run migrations, and support health checks.
- Provide systemd and macOS LaunchAgent examples with local data/log paths and conservative service limits.
- Provide safe production config examples without inline secrets.
- Provide quickstart, user, administrator, security/threat model, troubleshooting, upgrade, RC E2E, soak, performance, and recovery drill documentation.
- Provide a release checklist command that verifies tests, vet, build, build matrix, checksums, and required release documents.
- Add migration compatibility coverage for upgrading from a supported P13/P14-pre schema state to the latest embedded migrations.

## Non-Goals

- Package manager publishing.
- Windows release support.
- External CI provider configuration.
- Production-grade kernel or browser isolation beyond documented service hardening examples.

## Acceptance

- `go test ./...` passes.
- `make build` passes.
- `make smoke` passes.
- `make release-build` creates all required platform archives under `dist/`.
- `make checksums` creates `dist/SHA256SUMS`.
- `pachat version` prints all version model fields.
- `pachat release check --quick` passes in a local development checkout.
- README contains English and Chinese P14 release notes.
