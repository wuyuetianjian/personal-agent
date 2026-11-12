# Stage D: read-only private SQL diagnostics

Experimental observation overlays only. No original driver/core file is edited,
no SQLite branch or return code changes, and no VFS is registered by these files.
Production gates remain closed. Compilation is not native Stage-D qualification;
Stage C must first finish its native qualification.

## Files and installation

- `scripts/sqlite-persistent-vfs/private_sql_diagnostics_windows.go.txt` installs
  as `lib/nativeprobe_sqlite_private_sql_diagnostics_windows.go` in the verified
  temporary module copy.
- `scripts/sqlite-persistent-vfs/private_sql_driver_diagnostics_windows.go.txt`
  installs as `nativeprobe_sqlite_private_sql_diagnostics_windows.go` at that
  copy's module root, package `sqlite`.

Both additionally require `nativeprobe_sqlite_private_sql`, Windows amd64/arm64,
plus persistent/main-read/SHM-resource/SHM-slot tags. They depend on existing
bridge declarations, not on future D registration symbols. Install exclusively
into an owned copy, verify all original source hashes before/after, and pin both
patch hashes independently in the eventual D runner. Never modify GOMODCACHE.

## Interfaces

Driver entry:

```
ExperimentalPrivateSQLDiagnostics(*sql.Conn)
    (sqlite3.ExperimentalPrivateSQLState, error)
```

The driver uses `Conn.Raw`, asserts its actual internal `*conn`, holds its mutex,
and passes db/TLS only to the lib shim. Errors are sanitized, including panic
errors, rather than exposing arbitrary callback text or pointer-bearing errors.
The lib shim holds SQLite's database mutex, traverses main database Btree/Pager/
Wal, verifies the actual main file matches the pager and live private registry,
and holds the existing main-file mutex while consulting the trusted witness.
No returned field has pointer/uintptr type. Returned integers describe counters,
readmark index and flags, never handles or addresses.

Trusted adapter interface:

```
type ExperimentalPrivateSQLStorageWitness interface {
    ObserveSHM(key ExperimentalSHMResourceKey, region int32,
        mapped uintptr, readSlot int32) (mappingMatches, sharedReadSlotHeld bool)
}
```

Only the internal experimental authority implements this. Region is currently 0;
`mapped` is actual `Wal.apWiData[0]`. `readSlot` is **Wal.readLock's original
readmark index 0..4**, or -1 with no active read transaction. The adapter maps
index i to logical C slot 3+i (native byte 123+i). The adapter compares the live
B attachment/generation/view address and C shared-lock ledger; it must not take
this pointer as authority to acquire a resource, retain it, log it or expose it.
The snapshot API never returns the key or pointer. False results are observations,
not permission to repair state or take a missing lock.

Required lock/lifetime contract: the D session holds a real execution/Rows pin
and serializes diagnostic observation with Rows.Next/Close, Stmt execution/Close
and connection shutdown. Pause after an actual row while the transaction remains
live. `Conn.Raw` serializes driver access but does not substitute for that session
contract. Observer callbacks must be read-only, must not reenter SQL or acquire
the already-held main-file mutex, and must not wait for an operation which needs
that mutex. Order is session observation admission -> database/sql Raw -> driver
mutex -> SQLite db mutex -> private main mutex -> short host/ledger observation.
The D implementation must check this order against its C/B host locks.

## Meaning and limitations

`NormalMode`, `HeapMode`, `SHMUnreliable`, readonly bits, readmark index, cached
index-page count and WAL snapshot frame count are copied from the actual Wal.
`LivePageZeroMatches` requires a nonzero page and trusted B match.
`LiveReaderSlotMatches` requires index 0..4 and the trusted C shared-lock match.
A paused successful private SQL row plus these facts supports: **this SQL read
is using a live shared index at the observation point**. It does not prove that
no earlier phase used heap memory, nor that all future operations remain safe.
Any whole-query-history claim needs admission, map/lock/resource event evidence
and observation at each relevant phase, not one final snapshot.

If page0 matches, diagnostics atomically loads checkpoint backfill and readmarks
1..4. `ReadmarkCeiling` is the cached WAL frame limit, lowered by an active
snapshot limit if present. `SuitableReadmarks` counts marks <= that ceiling;
zero and older marks qualify. This follows the candidate predicate in pinned
`_walTryBeginRead`, not a requirement that a mark equal the latest frame.
These are separate atomic loads, **not a coherent multi-field snapshot** under
concurrent writers. The count alone does not prove a readmark can be locked,
and read-lock0/backfill shortcuts are separate. A negative fixture must quiesce
its cooperating actors and validate its exact prerequisites. No active stock
reader does not imply no suitable mark. If stock APIs do not naturally yield
the desired state, label any negative as controlled synthetic-index testing,
not normal stock SQL interoperability. Never silently rewrite the index.

A missing main/WAL, stock/nonprivate file, missing witness, closed/poisoned private
main, or callback panic produces an error, not affirmative evidence. A false
match remains false. Normal-mode/readonly/shared-match assertions belong to D's
real tests and strict event gate. This module cannot prove raw-truncate safety,
unknown-alias confidentiality, ordinary-user qualification or production access.

## Fixed source and type provenance

Module `modernc.org/sqlite@v1.58.0`, module sum
`h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=`, go.mod sum
`h1:rsD2CckafgObKC4DhBlGBf+RiHxkc3hINGt1Xw32tVY=`.
SQLite version 3.53.4, source ID
`2026-07-24 19:02:57 bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc`.

- `conn.go:22` owns db/TLS; `conn.go:917` uses close_v2. It is not proof that
  retained statements have already been finalized.
- `rows.go:85,111` close/reset/finalize and later Next/step; `stmt.go:17,64`
  cached statement ownership/finalize. Query return is not pointer retirement.
- `lib/sqlite.go:4874,4912,5112` defines BtShared, Btree and Db;
  `30544` implements `_sqlite3BtreePager`; `7429,7453` define checkpoint/index
  headers. The readmark area follows two actual `TWalIndexHdr` objects, not the
  generated zero-valued WALINDEX_HDR_SIZE macro placeholder.
- `lib/sqlite_g_000000000002c4cf.go:43` defines TPager; Ffd/FpWal at 75/105.
- `lib/sqlite_g_000000000003feef.go:1164,1388` defines TWal/Tsqlite3.
- `lib/sqlite_windows.go:120217-120266` contains readmark selection and the
  SHM_RDONLY gate. `lib/sqlite_g_0000000000060000.go:113005` contains mapping
  CANTINIT-to-heap handling; `lib/sqlite_g_000000000003fea8.go:2517` contains
  Windows64 begin-read transaction propagation. These inform interpretation;
  none is changed by the overlays.

The runner must pin this full set, in addition to existing bridge/source pins:

| Source | SHA256 |
|---|---|
| go.mod | `27b1c930e66040c2291da891f90b05e3af8571ab37c61664581eb597795ab1c3` |
| sqlite.go | `52fefb00e9f7a9a1bad23b034b57ce47b156bf29bebe7c2aef01083f5880759e` |
| conn.go | `85d2a7884941f345cbd42bb20a0923289d63304096f92167bbe30dfcc307fb17` |
| rows.go | `bdd566da5998406cb76852d08127b22694024c099824de4f303a853df1d31f33` |
| stmt.go | `ae8a35a88b725796ca41ce2c1c36bb0d49780f28cd2beee7faf587d0ce67d9de` |
| lib/sqlite.go | `8210957d4836c361efb18808fb6fd4dc5f2a7ca362fd83881d6018e9913ee4d6` |
| lib/sqlite_windows.go | `414fac0347bda229e81decf8b765a276dc59f382cde33542f7246862e7022d98` |
| lib/sqlite_g_000000000002c4cf.go | `fb4aff5fbbf7d2a43da789ac4179a4ba3ae38bdab04d516585110b51bd0a586d` |
| lib/sqlite_g_000000000003feef.go | `7401d63086a97b5133dbdb27b567ed1bc0dcfcd6f5118c64d60a6395e74c0bf6` |
| lib/sqlite_g_000000000003fea8.go | `bf257278d229722ff39238bc26a9506e1fbf094bf991e67a9c909a292cdac3c1` |
| lib/sqlite_g_0000000000060000.go | `8416138b3519a8ad4a2e4a76928c34781333915f09ef5d5444eb75d5cc4eabe7` |
