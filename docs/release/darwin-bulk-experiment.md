# macOS bounded directory metadata experiment

Status: design reviewed against the current disconnected foundation; native RED
qualification pending. This is not Search or content Read. Production build flags
and disabled capability gates remain unchanged.

## Decision and limits

Use the public `getattrlistbulk(2)` API through a thin SDK-compiled C bridge,
selected only by `darwin && cgo && nativeprobe_darwin_bulk`. Keep default release
`CGO_ENABLED=0`. No hand-coded syscall numbers, private libc symbols, entitlement,
Seatbelt profile or unsigned runtime helper is introduced. A future production
CGO policy remains a separate decision. Build this experiment on the native Apple
runner with Xcode's SDK and record the SDK/compiler/architecture.

The existing supplied trusted-directory contract is retained. Under its mutex,
open `.` relative to its owned descriptor with O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC,
then verify APFS, device, filesystem ID and inode against the pinned endpoint
before enumeration. This fresh open has an independent cursor; dup alone does
not. Close exactly once and report its error. Keep all existing lineage owned.
No listed leaf is opened, resolved, read or recursively enumerated.

Request only NAME, RETURNED_ATTRS, ERROR, OBJTYPE, FLAGS and FILEID with
FSOPT_PACK_INVAL_ATTRS. Parse packed records with checked arithmetic; require the
returned masks for every used value, reject per-entry errors, unsupported types,
firmlink flags, zero IDs, embedded NUL, invalid single components and duplicate
names. Preserve names as bytes; do not normalize/case-fold them into authority.
Regular files and directories are metadata observations only. In particular,
a file ID here is neither a hardlink check nor permission to open its contents.

Use a 64 KiB zero-initialized call buffer, at most 16 calls, 1024 returned raw
records, and 256 visible records. Count all calls, including interrupted calls;
no hidden retry. Require an EOF response within the budget, otherwise return an
error with no entries. Each record must fit entirely inside its returned buffer,
including its signed relative name offset and NUL terminator. Compare endpoint
identity/safety metadata before and after; refuse observed changes. No partial
result on parse, cancellation, limit, observed mutation or close errors.

This bounds userspace returned-data/parse work, not kernel work or syscall time.
XNU can fall back to readdirattr, and suppress an error if it returned entries;
the experiment cannot expose those suppressed errors. Metadata is not an atomic
snapshot and timestamp equality cannot prove no transient mutation. APFS mount,
rename, aliases, restart authorization and safe content acquisition remain open.
The official manual states symlinks/firmlinks describe the link, not the target,
and mount-point attributes describe the underlying directory. Reject unsafe
returned types/flags rather than using them to traverse.

## Implementation and qualification plan

1. Native test-first checkpoint: a compiling explicit-refusal stub and positive
   APFS tests must fail at that refusal, not at setup/build. Preserve failure logs.
2. Add portable parser/protocol fixtures before implementation: valid records,
   missing masks, truncated/overflowing/overlapping name references, missing NUL,
   unknown/unsafe types, firmlinks, duplicates, budgets, cancellation, and cleanup
   errors. Run fixtures on Linux; do not call them native proof.
3. Implement the isolated bridge and ownership wrapper. Test raw SDK sizes and
   constants; no C/Go pointer retained after a synchronous call. Return errno
   separately from count. C bridge does exactly one getattrlistbulk call.
4. Native GREEN must pass each required test three times on actual APFS with no
   skips: names/IDs and positive syscall evidence, independent cursor, unsafe
   entries/no FIFO reader, limit/no partial, cancellation/closed handle, and exact
   descriptor cleanup. Add injected parser/cleanup/race errors before claiming
   those error paths qualified. Explicitly recheck untagged command dependency
   graph and CGO=0 builds. No main merge, Release, or capability enablement.

## Sources and rejected alternatives

- Apple XNU `xnu-11215.81.4`, `bsd/man/man2/getattrlistbulk.2` and
  `bsd/vfs/vfs_attrlist.c` (`getattrlistbulk`): public call/packing/EOF/error rules.
- Go x/sys v0.47.0 `unix/syscall_darwin.go`: Getdirentries rescans with
  fdopendir/readdir_r, can be O(n²), and discards closedir failure.
- Apple Libc `include/dirent.h`: public getdirentries is unavailable with 64-bit
  inode mode. Do not manually declare a private getdirentries64 replacement.
- O_EVTONLY, clonefileat, stat/open, openbyid_np and Seatbelt do not solve the
  strict leaf-acquisition contract; see native-foundation.md.
