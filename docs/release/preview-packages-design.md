# Explicit preview packages

## Approved scope

Artifact-only test-branch packages for Darwin arm64/amd64 and Windows amd64/arm64.
They contain the Go `serve-agent` service with rebuilt TypeScript Web, and a
compiled TypeScript Ink CLI requiring an independently installed Node 24.
No Release, tag, installer, auto-update, signing, capability widening or actual
provider request is part of this work. Windows ARM is compile-only until an
actual matching runner executes it. Existing failing full-platform tests remain.

## Approach

Do not use the legacy release builder (which lacks Windows and Web assets).
Do not embed a Node runtime or claim a standalone executable terminal. Compile
our TS/workspace sources with locked esbuild, retain Ink/React as ordinary ESM
production dependencies, and deploy them using the existing locked pnpm graph.
Verify the result outside the repository; never depend on workspace symlinks.
If dependency portability or deployment cannot be verified, stop and report
rather than publishing an incomplete CLI. Third-party licenses travel with it.

## Package boundary

Only explicitly staged service executable, terminal bundle/runtime dependencies,
example configuration, Chinese startup instructions and generated manifest may
enter a fresh output directory. No repository tree copy, `.git`, runtime DB,
environment files, credentials or user data. Packaging refuses existing output.
A file SHA256 manifest binds shipped bytes; it does not prove trust or native
qualification. The archive and its outer checksum are CI artifacts only.

## Verification

First exercise a disposable deployment and relocated CLI. Then TDD preview-only
versions/targets/clean staging, manifest integrity and archive contents. Rebuild
and embed Web, compile Go, unpack outside the checkout and verify every hash.
Actual host smoke uses a temporary loopback synthetic provider fixture, never a
real model: service version, static Web and CLI/SSE completion. Run native checks
only on the actual matching OS/architecture; record cross-build distinctly.
Interactive POSIX PTY and Windows ConPTY are separate evidence classes.

The permissions remain explicit trusted host+session Read/Search only. No
malicious replacement or historical-copy isolation claim; full Files, writes,
Exec/MCP remain unavailable on non-Linux. Windows ordinary-user acceptance,
macOS signing/notarization and actual external models remain separate work.
