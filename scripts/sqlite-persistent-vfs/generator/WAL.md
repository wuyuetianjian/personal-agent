# Linked WAL generator stage boundary

`-linked-wal` now emits one existing-WAL initializer, independently pinned by
`wal-manifest.json`. `-linked -linked-wal` remains rejected. This is an
initializer only, not registration, resource qualification or WAL SQL success.

Native prerequisites supplied by the coordinating thread:
- Stock flag evidence: c12ccca / run 43231504076 / job 131227711089,
  6 passes and 12 events. Both main modes request WAL 0x80006 and stock
  reports outflags 0x2.
- Private RED: 6548fb3 / run 43234055242 / job 131236592921,
  12 FxOpen failures at input 0x80006 plus 3 RO-resource failures, without
  setup failure; existing main/access tests stayed GREEN.

The agreed proposed bridge interface is:

```
_paWinOpenExistingWALBound(tls *libc.TLS, pVfs, zName, file uintptr,
    requestFlags int32, pOutFlags uintptr) int32
```

The initializer accepts exactly READWRITE|CREATE|WAL (0x80006), the observed
stock request. After the bridge safely qualifies an existing resource, it
reports READONLY (0x1), initializes actual TwinFile readonly state, retains
invalid Fh, disables mmap, and leaves method publication to the bridge. This
RO output intentionally differs from stock's 0x2 and needs native private
verification. CREATE in SQLite's request is not permission to create a file.
Other request combinations refuse.
The initializer must perform no open/create, pathname lookup, method publishing,
SHM operation, or native handle acquisition.

## Manifest and compatibility

`wal-manifest.json` is independent from `linked-manifest.json`. Its `WAL`
record holds `LinkedOutputSHA256` and `DerivedInitializer`, which contains:

- `Name`: `_paWinOpenExistingWALBound`;
- `SourceFile`: `lib/sqlite_windows.go`;
- `SourceFunction`: `_winOpen`;
- `SourceSHA256`: the exact pinned official declaration span;
- `Transforms`: AST anchor counts, input/output span hashes, constructor
  signature/flag-policy transformation and complete derived-output hash;
- `OutputSHA256`: exact formatted derived declaration hash.

The derived initializer is **not** a second official `_winOpen` declaration and
must not appear as a newly discovered upstream function. The WAL artifact contains only this additional initializer with the explicit experimental tags
and license. Record dependency on the exact existing linked output hash; compile
beside it to reuse its 16 helper algorithms, not copy them again. Validate both
manifests independently. Never change existing linked helper/initializer bytes
or its checked-in manifest to enable WAL. Keep original stock sources untouched.

Review and test requirements: strict official AST anchor matching;
missing/duplicate/changed anchors rejected; exact signature and flag predicates;
no stock method/handle acquisition or mutable stock dispatcher; correct readonly
initialization and pOutFlags; malformed manifest, altered derived/source hashes,
transform counts or linked dependency hash rejected; existing linked byte output
unchanged; at most one additional declaration and no duplicated helper symbols.


Implementation shares the main constructor's official AST-anchor extraction,
then performs AST-only role/name/request-state transformations on that derived
constructor. The manifest retains intermediate main-initializer transformations
and the final stock-to-WAL initializer hash; neither step scans/replaces source
text globally. The old linked output and manifest remain byte-identical.
