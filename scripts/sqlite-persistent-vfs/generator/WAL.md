# Linked WAL generator stage boundary

Current `-linked-wal` is an explicit refusing mode, including `-propose`.
It does not emit an initializer or manifest. `-linked -linked-wal` is rejected.
No source/output/cache path is touched after detecting either refusal.
This stage only tests routing and refusal; it is not native private-WAL RED
and must not be described as an implementation of WAL access.

The agreed proposed bridge interface is:

```
_paWinOpenExistingWALBound(tls *libc.TLS, pVfs, zName, file uintptr,
    requestFlags int32, pOutFlags uintptr) int32
```

A later accepting initializer requires genuine native private-method RED first.
Its fixed-source request hypothesis is exactly READWRITE|CREATE|WAL for the
readonly database's existing WAL, to be checked against native flag evidence.
After the bridge safely qualifies the existing resource, the initializer would
report READONLY, initialize the actual TwinFile readonly fields, retain invalid
Fh, disable mmap, and leave method publication to the bridge. CREATE in SQLite's
request is not permission to create a file. Other request combinations refuse.
The initializer must perform no open/create, pathname lookup, method publishing,
SHM operation, or native handle acquisition.

## Manifest shape and compatibility requirements for the next stage

`wal-manifest.json` will be independent from `linked-manifest.json`. Do not
write a provisional accepting manifest or invent source hashes before the
initializer exists. Planned additional `DerivedInitializer` record contains:

- `Name`: `_paWinOpenExistingWALBound`;
- `SourceFile`: `lib/sqlite_windows.go`;
- `SourceFunction`: `_winOpen`;
- `SourceSHA256`: the exact pinned official declaration span;
- `Transforms`: AST anchor counts, input/output span hashes, constructor
  signature/flag-policy transformation and complete derived-output hash;
- `OutputSHA256`: exact formatted derived declaration hash.

The derived initializer is **not** a second official `_winOpen` declaration and
must not appear as a newly discovered upstream function. The WAL artifact should
contain only this additional initializer with the explicit experimental tags
and license. Record dependency on the exact existing linked output hash; compile
beside it to reuse its 16 helper algorithms, not copy them again. Validate both
manifests independently. Never change existing linked helper/initializer bytes
or its checked-in manifest to enable WAL. Keep original stock sources untouched.

Next tests, after native RED authorization: strict official AST anchor matching;
missing/duplicate/changed anchors rejected; exact signature and flag predicates;
no stock method/handle acquisition or mutable stock dispatcher; correct readonly
initialization and pOutFlags; malformed manifest, altered derived/source hashes,
transform counts or linked dependency hash rejected; existing linked byte output
unchanged; at most one additional declaration and no duplicated helper symbols.
