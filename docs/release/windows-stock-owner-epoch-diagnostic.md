# Stock owner epoch protocol diagnostic

Experimental stock SQLite oracle, not a private VFS implementation or security
qualification. No ACL, file format, journal mode downgrade, migration, broker
change or neighbor restriction is introduced. Synthetic temporary databases only.

Two exact top-level tests run three times. They reuse only the stock subprocess
and temporary-root helpers from the existing sidecar diagnostic, which is unchanged.
The child helper is selected explicitly in subprocesses, not the parent manifest.

`TestStockOwnerEpochIdleProtocol` reserves a physical `sql.Conn`, executes WAL
SQL, finishes all statements and keeps no explicit transaction or Rows open.
With **no extra broker/file guards**, independent short-lived data-capable handles
must see main shared-range and SHM DMS exclusive-lock conflicts. Those witnesses
share delete and release any acquired locks before closing. Two external stock
processes commit/read, close normally and reopen; the owner reads every committed
row and all three identities stay stable. Last-owner close must release the main
lock, remove sidecars and permit subsequent normal stock recreation.

`TestStockOwnerEpochDrainAndClose` adds an explicitly test-only admission model.
It wraps actual Root.readWithHandleOps with active-operation accounting, and uses
identities whose lifetime is maintained by the actual stock owner (not persisted
IDs). A deterministic hook pauses a real neighbor read after native data-handle
acquisition. Shutdown first closes admission; new calls refuse before root.mu.
The owner and genuine SQLite locks must remain alive until the reader drains.
The model then closes the owner and remains terminal while a later stock process
creates a new sidecar generation. Registered content refuses, benign neighbor
bytes are delivered to an already-admitted reader, and no later read is admitted.

This models the lifecycle proposal, not an implemented production enforcement
point. It does not intercept direct Root.Read callers. It does not prove private
owner error handling, bootstrap registration, WAL/SHM fork correctness, alias
coverage, inherited retired objects, or arbitrary external creation. An idle
*uninitialized* sql.Open is not the tested condition. An actual private owner
must eventually repeat these contracts rather than inherit stock qualification.

Source rationale in modernc.org/sqlite v1.58.0:
- `_pager_unlock` (sqlite_g_0000000000060000.go:100320) does not drop main DB
  locks in the pWal branch; it ends the WAL read transaction.
- `_sqlite3WalClose` (:111395) requires main EXCLUSIVE before checkpoint/deletion.
- `_sqlite3PagerOpenWal` (sqlite.go:35162) requires only SHARED; sidecar creation
  is not globally serialized merely by having a main shared lock.
- Windows SHM open precedes DMS acquisition. DMS locks one SHM object, not names.

A PASS here supports testing a genuine owner-lifetime boundary; it does not
invalidate the earlier relaxed-known-ID gap counterexample. File-symbolic-link
aliases and first-open/bootstrap windows remain separate required diagnostics.
No claim is made about historical provenance before an explicit protection
baseline. Alias diagnostics are deliberately not bundled into these two tests.
