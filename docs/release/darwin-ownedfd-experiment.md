# macOS trusted pre-opened FD experiment

Status: native RED pending. The implementation currently refuses explicitly;
no descriptor is duplicated and no bytes are read by the experimental API.
All six contracts first independently qualify/read an actual APFS, readonly,
regular, single-link trusted fixture, then require a successful API call. Native
RED must show 18 exact refusal failures and 18 fixture controls, not setup errors.
Assertions after those refusals remain unexecuted until GREEN.

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
