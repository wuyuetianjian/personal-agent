# Linked main-only generation: experimental, awaiting native acceptance

The main-method refusal boundary was reached natively at `887aacb`: both tests
failed at the designated private constructor, with independent native lock
controls before refusal. This mode now generates a linkable helper file for the
separately reviewed bridge. It does not register a VFS, acquire a file, close a
native handle, install methods, write data, map SHM or make WAL SQL work.

```
go run ./scripts/sqlite-persistent-vfs/generator -linked \
  -source "$VERIFIED_MODULE_COPY" -cache-root "$VERIFIED_GOMODCACHE" \
  -manifest scripts/sqlite-persistent-vfs/generator/linked-manifest.json \
  -out "$OWNED_RUNNER_TEMP/main-generated.go.txt"
```

All original input/hash/license checks still run. Output must be a new `.go.txt`
outside source AND the entire real supplied GOMODCACHE, even in linked mode.
Runner then independently checks `OutputSHA256` and writes the bytes exclusively
to `lib/nativeprobe_sqlite_main_generated_windows.go` in its owned module copy.
No original source changes. The existing non-linked review mode remains separate.
The generated build constraint includes Windows amd64/arm64 and both experimental
persistent/main-read tags. BSD copyright, conditions and disclaimer are retained.

## Actual initialization, not a fake winFile

Signature agreed with bridge author:

```
_paWinOpenBound(tls *libc.TLS, pVfs, zName, file uintptr,
    flags int32, pOutFlags uintptr) int32
```

Despite its name this is **initialization only**. It accepts only exact
READONLY|MAIN_DB and nonzero inputs. It verifies `unsafe.Sizeof(TwinFile{}) == 96`.
The zero/invalid-handle statements and successful field-initialization tail are
extracted from the actual pinned `_winOpen` AST. Each required anchor matches
once; Fh matches twice (initial invalid handle and final acquired h assignment).
The manifest records original/rewritten span hashes and counts, including the
whole original function -> initializer transformation.

Kept: true 96-byte memset, invalid Fh, FpVfs, readonly flag block, FlastErrno,
FzPath, FhMap, FpMapRegion, mmap size. No alternative Go struct or offset table is
invented. The readonly block's generated upstream field access is retained.
Dropped: all pathname/temp/conversion/attributes/CreateFile/readonly-fallback
logic; acquired-h assignment; stock method selection/publication; URI-controlled
PSOW block. PSOW is conservatively off for this read-only stage, not emulated.
Rewrite: mmap maximum is 0, not global `_sqlite3Config.FszMmap`.

FpMethods remains zero from memset; Fh remains invalid. Bridge acquires/registers
the real lease and file binding and publishes its explicit table last. During a
per-file serialized Borrow only, it installs the borrowed Fh, invokes the helper,
and restores invalid Fh in defer. Initialization success is not open authority.
If bridge publication later fails it clears outFlags and consumes its candidate
once. Names must retain SQLite-compatible lifetime through close.

## Algorithm closure and syscall isolation

The linked closure contains 16 original algorithms/helpers plus initializer:

- `_winRead`, `_winFileSize`, `_winLock`, `_winUnlock`, `_winCheckReservedLock`;
- `_winGetReadLock`, `_winLockFile`, `_winUnlockFile`, `_winUnlockReadLock`;
- `_winRetryIoerr`, `_winLogIoerr`, `_winLogErrorAtLine`, `_winGetLastErrorMsg`;
- `_winMbcsToUnicode`, `_winMbcsToUtf8`, `_winUnicodeToUtf8`.

Every selected function/reference becomes `_paWin*` at exact AST identifier
spans. Original function bytes, output function bytes and every transformation
key/count/input/output digest are in `linked-manifest.json`. All remaining call
expressions and unresolved identifiers (including types/fields, conservatively)
are counted in `Linked.Calls` and `Linked.ExternalIdentifiers` and compared exactly
on generation. New names/counts fail manifest validation rather than silently
extending the permitted closure.

Do not replace FpCurrent with FpDefault: stock default slots initially contain
zero. Instead inspect the unique official init function whose first statement
is `p := unsafe.Pointer(&_aSyscall)`. Exact assignments bind offsets to original
libc targets; the whole init span is hashed. For each used index, replace only
that AST selector with `__ccgo_fp(original-libc-target)`. No runtime `_aSyscall`
read or write remains. The approved index -> target map is:

| Index | Original libc target | Purpose |
| --- | --- | --- |
| 0 | XAreFileApisANSI | Error-message encoding choice |
| 14 / 15 | XFormatMessageA / XFormatMessageW | Error formatting |
| 23 | XGetFileSize | Bound-handle file size |
| 26 | XGetLastError | Native error observation |
| 46 | XLocalFree | OS error-message allocation cleanup |
| 47 / 48 | XLockFile / XLockFileEx | Stock real byte-range locking |
| 50 | XMultiByteToWideChar | Error-string conversion |
| 52 | XReadFile | Bound-handle read |
| 55 | XSleep | Retry/backoff, including private lock helper |
| 57 / 58 | XUnlockFile / XUnlockFileEx | Stock real byte-range unlocking |
| 60 | XWideCharToMultiByte | Error-string conversion |

Index 55 is also validated before rewriting calls to `Xsqlite3_win32_sleep` into
`libc.XSleep`; otherwise that public wrapper would reenter the mutable stock
syscall table. Native tests must install/restore a stock ReadFile counting
forwarder through public stock VFS syscall controls: private reads must bypass
it, and a fresh stock connection must exercise it. Compilation alone is not this
behavioral proof. The private VFS's own override slots remain absent.

## Remaining dependency classification

The manifest lists every call/identifier, not just these summaries:

- `_sqlite3PendingByte`: shared SQLite protocol configuration, read only; preserve
  stock byte offsets rather than invent a competing lock protocol. Changing this
  test-oriented global concurrently with live connections is not supported.
- `__ccgo_ts`: read-only pinned string table. `__ccgo_up` / `__ccgo_fp`: original
  translated pointer/function representation. True `TwinFile`, OVERLAPPED and
  Windows aliases: pinned type definitions, not new layouts.
- `_paWinIoerrRetry` / `_paWinIoerrRetryDelay`: private constants initialized from
  the pinned default retry macros; no reading mutable stock retry globals.
- `Xsqlite3_log`, `Xsqlite3_snprintf`: standard SQLite logging/formatting; logging
  configuration stays ordinary shared library configuration, not file authority.
- `_sqlite3MallocZero`, `Xsqlite3_free`, `_sqlite3BeginBenignMalloc`,
  `_sqlite3EndBenignMalloc`: existing SQLite allocation/error machinery.
- `Xsqlite3_randomness`: retained only in the original compile-time-dead else
  branch of `_winGetReadLock` (`if int32(1) != 0`). It cannot route a live NT lock
  operation through default-VFS randomness. Any change to that condition changes
  the pinned source/function hash and needs a fresh review.
- `tls.Alloc/Free`: call-local scratch. `libc.Xmemset/Xmemcpy`: original buffer
  zero-fill/copy. Main mmap stays zero; bridge must reject unexpected mapping
  state before helper dispatch. Integer/boolean conversions and VaList are the
  original libc representation helpers. Other unresolved identifiers are the
  enumerated scalar constants, type names, field names and Go builtins.

Generation explicitly rejects residual stock table/method/node references,
stock sleep wrapper, CreateFile/DeleteFile/CloseHandle/map/unmap libc targets.
The generated file has no registration/init side effects. Close is exclusively
the bridge/lease owner's consume-once path, not `_winClose`.

## Required tests and limits

Generator tests cover AST-only edits, missing/duplicate declarations, input and
function/hash/count mismatch, indirect function references, syscall target/offset
uniqueness, missing constructor anchors, dropping path/PSOW/method publication,
private original syscall/sleep rewrites, unknown slots, exact checkout bytes and
source/cache/output protection. Real official-source generation and both Windows
architectures must compile with the independently maintained bridge.

Native main-method tests still own behavioral acceptance: header, size,
SHORT_READ zero fill, independent byte-range lock attribution, consume-once close,
callback panic, recycled file-address generation, default-VFS isolation and the
override control. Do not claim these from extraction or compilation alone.

Bridge must explicitly refuse writes, truncate, sync, dangerous/unknown file
controls, temp/ATTACH/non-main roles, SHM and mmap. xAccess must not fake absent
hot journals. No SQL/WAL compatibility claim follows from raw main-file methods;
the held-WAL SQL positive contract remains separate and cannot pass without the
later genuine SHM/DMS/mapping, retirement and recovery implementation.
