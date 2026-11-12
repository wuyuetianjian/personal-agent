# Trusted pre-opened Darwin FD byte experiment

## Decision

Implement only an independently tagged, one-shot `ReadTrustedRegular(ctx context.Context, source *os.File, maxBytes int64) ([]byte, error)` experiment. Input is explicitly trusted and already readable; no pathname is accepted. The caller must authorize the object before calling. This does not obtain that trust or authorization and cannot back Read(path).

The one-shot form is deliberate: duplicate, qualify, pread, re-observe and close before publishing bytes. A close error therefore yields nil output, rather than invalidating already-published bytes. No owned FD or persistent token escapes. Caller retains its descriptor; its closure after duplication does not invalidate our reference. Concurrent mutation of file status flags or contents by the trusted supplier is not supported as an immutable source contract.

## Exact contract

- Build only `darwin && nativeprobe_darwin_ownedfd`; Go/x/sys, CGO_ENABLED=0. No existing probe/bulk/production modifications.
- Duplicate via SyscallConn.Control and F_DUPFD_CLOEXEC. Never open/openat/EVTONLY-upgrade/resolve a source path. Dup retains the same open file description; pread preserves its shared offset.
- Require F_GETFL access mode exactly O_RDONLY, reject O_EVTONLY; fstat regular, nlink==1, nonnegative size <= maxBytes; fstatfs APFS. Hard ceiling maxBytes=1 MiB; negative or larger limits rejected.
- Read the observed length with <=64 KiB per pread and at most 32 calls, including interrupted attempts. All returned data lengths and counts checked. Unexpected short EOF, read errors, exhausted calls or cancellation produce nil bytes. No retry beyond this budget.
- Recheck descriptor type/device/inode/link count/size/mtime/ctime and APFS identity before publication, ignoring atime. Observed change refuses. Equality is NOT a snapshot guarantee: concurrent write/ABA can evade it. No claims about current pathname ancestry, immutable contents or persistent identity.
- Check cancellation before duplication, around native steps and after cleanup. Cancellation does not interrupt a blocking kernel syscall; this bounds returned bytes and userspace work, not syscall latency.
- Close each acquired duplicate exactly once, join primary/close errors, and publish no bytes if either failed. An uncertain close is never retried. No write, unlink, migration, writable mapping or source ownership transfer.

## Six native contracts

1. TestOwnedFDReadPreservesOffset: exact bytes, unchanged caller offset, owned CLOEXEC.
2. TestOwnedFDCallerCloseAndPathReplacement: dup retains original regular object after caller close/rename and original pathname replaced by FIFO; no substitute leaf is opened.
3. TestOwnedFDAdmissionRefusals: nil/closed source, non-readonly, EVTONLY, directory/pipe, multiple links. Fixture creates these deliberately; consumer never opens them.
4. TestOwnedFDBoundsAndObservedMutation: exact/oversize/invalid budgets, empty bytes, bounded slow progress and observed mutation zero output.
5. TestOwnedFDCancellationAndCloseRace: pre-cancel, cancel after duplication, source.Close at controlled owned boundary; cancellation+cleanup no data and once-only cleanup.
6. TestOwnedFDReadAndCloseFailures: real duplicate with controlled pread failure and close-error-after-real-close; joined causes, nil data, one cleanup, no leaked duplicate.

Each begins with independent actual APFS/readonly/regular/single-link fixture qualification and pread content control. Each then requires a successful public API read. RED must fail specifically at the compiling explicit refusal, not setup/build. Later assertions are not claimed covered in RED. GREEN fault hooks are per-call private test seams, never global OS overrides. They inject reported failure after a real close, not proof of a kernel uncertain-close recovery.

## Evidence and boundaries

Native runner records macOS/kernel/arch/APFS and Go/toolchain, requires six exact run/pass events three times, rejects fail/skip/build-fail, and checks untagged release dependency graph and CGO0 builds. Linux cross-compilation is not native qualification. Production gate stays closed; this adds only trusted descriptor consumption, not safe pathname acquisition, source authorization or file-provider security.

Sources: Apple XNU xnu-11215.81.4 bsd/sys/fcntl.h; Apple dup(2) and pread(2) manuals:
- https://github.com/apple-oss-distributions/xnu/blob/xnu-11215.81.4/bsd/sys/fcntl.h
- https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/dup.2.html
- https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/pread.2.html
