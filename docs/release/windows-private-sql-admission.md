# Same-binding SQL admission to explicit SHM refusal: RED scaffold

New files only. Previously qualified main, metadata, WAL and permit contracts are
unchanged. Build additionally requires nativeprobe_sqlite_private_shm. The runner
installs base/main/linked/WAL/linked-wal/access/admission overlays in its owned
verified module copy; no cache or production modification.

The new composite authority binds metadata observation and existing main/WAL
resources to the same qualified root and connection. Metadata is not converted
into an open token. Its READWRITE observation additionally reflects the new
binding's readonly policy (false), not merely the underlying file attributes;
this does not change the independent metadata observer's stock-compatible API.

Registration uses the existing prepublication factory. Its prepare callback sets
xAccess and xFullPathname and records the composite binding BEFORE one VFS
registration. No already-published table is modified. Failure never returns a
usable registered name to the caller. Diagnostic maps remain retained for the
experiment process lifetime, like the prior private bridges.

At this RED checkpoint, the actual main URI xOpen recognizes canonical path and
0x141 then explicitly refuses and records Unimplemented. ADMISSION_MAIN_EXPECTED_RED
requires that real callback and an actual SQL failure after fixture qualification.
It is **admission RED**, not evidence that SQL has reached SHM yet. The next GREEN
step must accept qualified admission, route actual xAccess/WAL through the same
binding, and stop specifically at an instrumented SQLITE_IOERR_SHMMAP stub. No
SHM mapping or SQL success may be substituted for this stage.

Only this exact known canonical local fixture filename, or its literal slash
spelling (and Windows file-URI drive-leading slash spelling), can pass the initial
FullPath callback. It does not clean arbitrary paths, case-fold, resolve links,
URI-decode or traverse directories. It emits the unique bound canonical spelling.
Later xOpen must still validate the effective canonical path and actual URI keys.
This is a controlled test entry, NOT a validated arbitrary user URI API. SQLite
already parses URI settings before callbacks. Merely removing the URI bit cannot
neutralize immutable/nolock, alternative VFS selection or discarded raw spelling.
A request selecting a different VFS is outside this private registration's control.

Three fixed top-level contracts, no subtests, three repetitions:
1. TestPrivateSQLAdmissionReachesSHMRefusal: actual database/sql URI selects this
   VFS, main0x141, same-owner xAccess, WAL0x80006 and explicit SHM refusal all must
   be observed. SQL must return extended IOERR_SHMMAP, not success or heap fallback.
2. TestPrivateSQLAdmissionRejectsURIEscapes: valid traversal first, then immutable,
   nolock, duplicate/unknown key, writable mode and foreign canonical path must
   fail before WAL/SHM. Old registrations remain unchanged. This tests the chosen
   effective URI boundary, not every parser normalization or external entry point.
3. TestPrivateSQLAdmissionJournalTruth: valid traversal first, then same-binding
   xAccess reports definite journal absence and actual nonempty journal presence;
   actual journal content xOpen refuses. A synthetic nonempty header fixture must
   not be hidden until unrelated SHM failure or mutated as recovery. It is not a
   general crash-journal recovery implementation.

The child fixture now checkpoints schema, commits a WAL-only value, then begins
and reads a real transaction held until release. This establishes a live snapshot
rather than relying on idle connection state or stale readmarks. Main-only
immutable observation remains an independent fixture control only; private SQL
never uses immutable. Normal stock SQL reads before/after must agree. Default VFS
pointer remains unchanged. Failed fixture/close/setup is never labeled feature RED.

All later negative/SHM branches are beyond the initial refusing main callback and
must actually execute before stage-1 qualification. Full SHM mapping grants,
readonly-index flags, true cross-process lock ranges, SQL pointer lifetime and
external sidecar confidentiality remain subsequent explicit stages.
