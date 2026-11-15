# Preview Packages Implementation Plan

> Execute task-by-task with TDD and independent review; parent handles commits.

**Goal:** Four clearly labeled preview artifacts with embedded Web and runnable Node CLI.
**Architecture:** Independent Node packaging coordinator, locked TS compiler/dependency deployment, Go cross/native compilation, extraction-based smoke and read-only CI artifacts.
**Tech Stack:** Node 24, pnpm 11.25.0, Go go.mod toolchain, existing esbuild/Vite.
**Spec:** `docs/release/preview-packages-design.md`.

## Constraints and review focus

No production permission changes or legacy release edits. No user data/provider
network. Fail on stale output, unsafe names, absent runtime dependencies or hash
mismatch. Test path spaces and relocation; preserve dependency licenses. Native
addons/OS packages must be identified and not mislabeled portable. Every claimed
native smoke must run on matching architecture, not merely compile.

## Tasks

- [x] Verify locked pnpm deployment and relocated TS bundle in /tmp. If tooling
  cannot preserve its supply-chain checks, report blockage; do not bypass them.
- [x] Add `scripts/build-preview.test.mjs` contracts for target/version validation,
  clean staging, payload allowlist/manifest, runtime relocation and archive.
  Run against rejecting/minimal implementation and record expected failures.
- [x] Implement `scripts/build-preview.mjs` and Chinese package instructions;
  verify dependencies contain no required target-native addons before sharing.
- [x] Add `scripts/preview-smoke.mjs` to verify extracted manifest and actually
  execute host service/CLI against an owned loopback synthetic fixture.
- [x] Add `.github/workflows/preview-packages.yml`, pinned actions, contents:read,
  test branch only, four target outputs and explicit native/cross evidence.
- [x] Run local tests, project regressions, build/relocation smoke, YAML/CI
  contract checks. Hand stable files to independent review and parent for push.
  Local verification: default npm tests 56+332+4 passed, TypeScript passed,
  all script tests 434 passed, full Linux Go suite passed. Four target archives
  cross-built and verified after extraction outside the checkout. A separate,
  explicitly test-only Linux archive actually ran service version/Web/status,
  synthetic CLI SSE and real Ink POSIX PTY. These are not Mac/Windows native
  execution results. Development archives are evidence, not final deliverables.
- [ ] Parent commits/pushes the reviewed source to the existing test branch;
  CI rebuilds final artifacts and executes matching Mac arm64/amd64 and Windows
  amd64 native smokes. Windows arm64 remains cross-build/archive-only evidence.
  No GitHub Release or tag. Actual native CI outcomes qualify only observed
  targets; untested Windows ConPTY/non-admin usage remains explicitly unverified.
