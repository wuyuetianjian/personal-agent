# macOS trusted pre-opened FD experiment

Status: native RED confirmed at `157dc58`, run `43269525085`, job
`131362015030` on macOS 15.8.2. Independent log parsing found six exact tests,
each run/fail three times, each with its real full-flags-zero APFS trusted-file
Pread control and the specific explicit-unimplemented refusal: 18 of each, no
skips. The implementation is now a GREEN candidate awaiting exact-commit native
qualification; prior assertions beyond RED's first API call were not covered.

- Spec: [trusted FD boundary](darwin-ownedfd-design.md)
- Plan: [test-first checkpoints](darwin-ownedfd-plan.md)
- API: `ReadTrustedRegular(context.Context, *os.File, int64) ([]byte, error)`
- Build tag: `darwin && nativeprobe_darwin_ownedfd`; `CGO_ENABLED=0`.

This is an explicitly trusted pre-opened descriptor consumer, not a source
acquisition/authorization mechanism. The one-shot owned duplicate is closed
before bytes may escape, allowing cleanup errors to return nil data. It never
accepts a pathname, opens a listed leaf or upgrades an O_EVTONLY descriptor.
Before/after observations do not establish an immutable snapshot. Existing
metadata/bulk experiments and all production capability gates are unchanged.

Linux cross-compilation verifies Darwin type/build correctness only. Native
APFS runs, exact per-test evidence and independent review are required before
calling even this narrow descriptor-consumption capability qualified.

## Tightened admission contract before native RED

The independent fixture positive control requires the complete F_GETFL result
to equal O_RDONLY (zero). GREEN must reject every extra status flag, not merely
O_EVTONLY or non-readonly access bits. The existing admission contract includes
a real O_NONBLOCK status control on a readonly descriptor; it remains behind
the initial explicit refusal in RED and is not yet claimed executed. No new
top-level tests or relaxed gates are introduced. Control is reserved for dup;
afterDup runs only after it returns. Fault fixtures separately verify successful
native close before injecting a reported close error. That checkpoint retained the unchanged explicit refusal; the implementation
below was added only after its native RED evidence.

## GREEN candidate, native acceptance pending

The implementation duplicates within RawControl, executes any afterDup handshake
outside Control, qualifies its owned FD with exact flags zero and APFS regular
single-link bounds, and reads with at most 32 pread calls of at most 64 KiB each.
It performs no internal EINTR retry. It rechecks metadata and filesystem identity,
then closes its owned duplicate exactly once before bytes can return. Errors,
controlled seam panics and cancellation observed through cleanup yield nil bytes.
No cancellation deadline for blocking native calls or metadata-based snapshot
guarantee is claimed.

The six existing test bodies also cover the 1 MiB positive boundary, zero/invalid
read lengths, partial-data errors, interrupted calls, late cleanup cancellation
and three controlled seam panic positions. Fault-close tests separately verify
that the real close succeeded. These are authored qualification contracts, not
executed Darwin coverage until the native GREEN run passes. Linux checks and
Darwin cross-compilation do not substitute for that evidence.
