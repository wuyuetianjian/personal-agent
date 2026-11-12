# Darwin Owned FD Implementation Plan

**Goal:** Qualify bounded byte consumption from an explicitly trusted already-readable APFS regular FD without pathname acquisition.

**Architecture:** One-shot owned duplicate, metadata qualification, bounded pread and mandatory cleanup before bytes escape. A private per-call syscall seam exercises cleanup without changing global OS functions.

**Tech Stack:** Go 1.26.4, golang.org/x/sys/unix, darwin && nativeprobe_darwin_ownedfd, CGO_ENABLED=0.

**Spec:** docs/release/darwin-ownedfd-design.md

## Global constraints

No existing probe/bulk or production changes. No path input, no leaf open, no content snapshot claim. 1 MiB maxBytes ceiling; 64 KiB per pread; 32 total calls. Exactly six top-level native tests, each three rounds. Publication is limited to the authorized public test branch; no production enablement.

## Task 1: Native RED checkpoint

- [ ] Write the six tests in internal/nativefs/darwinprobe/ownedfd_experimental_darwin_test.go first; every test independently qualifies and reads a real trusted APFS fixture before requiring the new API.
- [ ] Add only a compiling refusal in ownedfd_experimental_darwin.go; expose no functioning read path.
- [ ] Cross-compile amd64/arm64 CGO0, run untagged portable suite/diff checks; distinguish these from native RED.
- [ ] Hand six exact names/markers to independent runner author. Publish RED only after independent review; collect 18 actual refusal failures and 18 fixture controls with no setup failures.

## Task 2: GREEN, only after native RED

- [ ] Implement owned F_DUPFD_CLOEXEC inside Control, then afterDup outside Control, and strict full F_GETFL==O_RDONLY plus fstat/fstatfs checks, bounded pread, before/after comparison and joined exactly-once cleanup. Per-call seams must still perform real descriptor acquisition.
- [ ] Exercise all refusal/error/budget/cancellation/namespace replacement assertions behind the positive control, including a verified real O_NONBLOCK readonly-status refusal; separately assert successful native close before accepting reported close-error injection. Native six-by-three passing is required; extend precise subtests only if the gate is updated.
- [ ] Independently review source input/dup lifetime, syscall counts, unknown close handling and zero-output error paths.
- [ ] Verify untagged release graph excludes the new API and CGO0 release still builds. Preserve full source and native logs at each checkpoint.

## Review focus

Caller-close versus acquisition; FD number reuse after cleanup; shared open-file offset/flags; metadata equality not snapshot; special-file refusal without consumer leaf open; no partial data on late errors. No claim of ordinary arbitrary-path workspace Read follows from these tests.
