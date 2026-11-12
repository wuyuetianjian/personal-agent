# Private persistent SQL vertical slice: RED only

This is a disconnected test, not a working persistent VFS or production driver.
The independent stock test child creates a synthetic WAL database and holds its
connection open. Parent stock reads prove the committed row before and after a
private SQL attempt. The private registration clones the real pinned sqlite3_vfs
(nondefault), but creates NO winFile and NO io_methods. Its xOpen refuses. The
positive test MUST remain red until real private SQL reads work. A setup failure,
skip, default-VFS fallback or missing child readiness is not the intended RED.
Native evidence must contain `PERSISTENT_SQL_EXPECTED_RED` three times, following
successful fixture setup and default-stock reads. The workflow must fail, not turn
expected failure into passing CI. This change is prepared for review; native
execution/publication is the coordinator's next step.

## Exact boundary

* xOpen: count and SQLITE_CANTOPEN, no acquisition, no output methods.
* xDelete/xAccess: SQLITE_IOERR_DELETE/ACCESS, no inherited path operations.
* xFullPathname: bounded pure string copy to reach xOpen, no Win32 access. This
  is NOT secure canonicalization, and must not remain when xOpen accepts files.
* xDlOpen/xDlSym refuse; xDlError empty bounded result; xDlClose no-op.
* xSet/Get/NextSystemCall: absent (optional), cannot change global syscall table.
* xRandomness/xSleep/xCurrentTime/xCurrentTimeInt64/xGetLastError: stock callbacks
  retained; non-path platform utilities only. No database or sidecar IO.
* szOsFile is the true stock struct allocation size, never a forged winFile.
* Registration/name retained for test process lifetime; no unsafe unregister or
  freeing live VFS memory. Only this test tag/module copy exports the experiment.
* Test DSN is constructed internally from a host-created absolute temp filename
  and fixed VFS name/mode. No user-supplied DSN/configuration interface is added.

Runner checks official module version, go.sum/download checksums, selected module,
`go mod verify` before and after copying, and exact SHA256 of sqlite.go plus the
three relevant generated source files in source and copy. It writes one new lib
file with exclusive creation, logs its SHA256, replaces only the temporary modfile
and removes only owned entries. No generated source replacement occurs at RED;
future edits must add exact source-match counts and result hashes. Production
module, driver and shared module cache are untouched.

## Next positive contracts (not implemented by this RED)

1. Real private xOpen reads the held stock WAL without copying or mode change.
2. Stock child begins/read-holds a transaction. Private writer commits a new row;
   old stock snapshot remains old, new stock transaction observes new row.
3. Busy timeout, writer contention, checkpoint with reader held, close/reopen,
   sidecar deletion/recreation, missing/stale SHM reconstruction, committed-WAL
   process death recovery. Verify integrity_check and expected committed rows.
4. Default VFS SQL connection in same process remains stock, including SHM node
   membership, callbacks and system call table. Repetition and two private owners
   exercise cross-connection ownership, locking and drain.
5. Mutation and active-read barriers run concurrently, failing before data IO.
   Refusing unsupported ATTACH, rollback journal, superjournal, subjournal, temp,
   mmap and file-control routes is required until each route has real contracts.

## Mandatory interception inventory before allowing any private IO

Pinned modernc.org/sqlite v1.58.0 sources:

* lib/sqlite_windows.go: _winOpen (125952), _winHandleOpen (125480),
  _winOpenSharedMemory (126134), _winShmMap (126539), _winShmPurge (126657),
  _winCloseSharedMemory (124921), _winHandleClose (125425), _winFileControl
  (124966), _winAccess (124816), _winIsDir (125549), _winGetTempname (125285),
  _winFullPathnameNoMutex (125069), _winMapfile (125785).
* lib/sqlite_g_0000000000060000.go: _winClose (113857), _winDelete (113983),
  _winFetch (114134), _winLockSharedMemory (114506), _winShmUnmap (114792),
  _winUnfetch (114923), _winUnmapfile (115015), _winHandleTruncate (114326).

All handle-relative acquisitions, candidate metadata/data identity checks,
creation, deletion, mapping objects/views and close/unmap failure paths must feed
the private owner registry. Preserve stock byte-range locking, DMS, mapping
alignment, barriers and recovery ordering, not a simplified emulation. DMS can
legitimately truncate SHM after exclusive acquisition; do not ban this recovery.
Unknown close results consume-once and latch terminal refusal; do not inherit
_winClose retries or silent _winHandleClose/_winShmPurge close errors. Audit
WIN32_GET_HANDLE, NULL_IO, SIZE_HINT, MMAP_SIZE, TEMPFILENAME, PERSIST_WAL and
WIN32_AV_RETRY; no raw-handle escape or global-setting mutation.

Stock _winShmNodeList is global and filename-keyed. A private xOpen wrapper alone
is insufficient: private helper variants must use an owner-isolated node list,
with identity-based sharing among approved connections of the same owner, NEVER
stock nodes or another owner. Register the shared DMS handle, EACH connection's
separately opened locking handle (not arbitrary duplicate), mapping handles and
views; reference counts/drain govern release. Reuse real stock winFile layout
and construction in the pinned fork, not ad hoc Go structs/offsets. Keep stock
path-based helpers and global aSyscall untouched for ordinary connections.

## Two distinct security properties

A dedicated runtime directory can form a tool confidentiality exclusion domain,
including externally created sidecars, but is NOT storage namespace integrity.
Retained directory IO guards do not prevent WRITE_ATTRIBUTES junction mutation.
Stock absolute-path opens can follow a changed ancestor and touch outside files,
including ABA restoration between checks. Handle-relative storage acquisition
and identity verification remain necessary under the existing hostile namespace
model. The in-process owner cannot pre-register an external process's SHM create;
this needs a separately proved namespace/tool admission design. Never silently
ban mixed-directory neighbors, move legacy DBs, downgrade WAL, force PERSIST_WAL
or hold permanent no-shareDELETE sidecar handles to hide that gap.
