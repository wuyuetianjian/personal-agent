# Same-binding SQL admission to explicit SHM refusal: implementation candidate

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

The RED scaffold was preserved in c167d625 before implementation; its native
attribution is checked independently by the coordinator. The current candidate
accepts exact main 0x101 or URI 0x141 after effective-key validation, then invokes
the old main initializer with only the URI transport bit removed. It calls the
same binding's metadata authority for xAccess and the qualified private readonly
WAL bridge for exact 0x80006. No stock callback supplies private data.

Only this registration's main gets a separate method table with a counted
xShmMap returning NULL plus SQLITE_IOERR_SHMMAP. It validates a live main role,
binding and generation through the existing borrowed-resource gate. It never
maps, returns CANTINIT, constructs a heap index, or claims SQL success. This
stage's eventual GREEN means SQL reached the deliberate SHM refusal, not that
WAL/SHM SQL was implemented.

Only this exact known canonical local fixture filename, or its literal slash
spelling (and Windows file-URI drive-leading slash spelling), can pass the initial
FullPath callback. It does not clean arbitrary paths, case-fold, resolve links,
URI-decode or traverse directories. It emits the unique bound canonical spelling.
The new xOpen validates the effective canonical path and actual URI keys.
It requires exactly mode=ro and vfs=this registration; duplicate or unknown keys
are rejected. The original SQLite filename allocation/URI lifetime is preserved.
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

Negative URI and journal branches were beyond the original refusing main
callback. They must now actually execute before stage-1 qualification. Query and
database-close errors are asserted separately: an expected query refusal cannot
hide an unexpected close failure. Journal SQL must freshly observe the actual
journal, reach its explicit unsupported-content-open rejection, and not increment
SHM counters. An unrelated SHM error cannot satisfy the journal test. Full SHM mapping grants,
readonly-index flags, true cross-process lock ranges, SQL pointer lifetime and
external sidecar confidentiality remain subsequent explicit stages.


Bounds retained from the preceding slice: metadata-to-data snapshot comparison
includes mutable WAL size/timestamps and can conservatively refuse a legitimate
concurrent writer append. The held-stock fixture does not qualify continuous
writer interoperability. Callback admission counts keep handles safe, but a future
SHM pointer may live between callbacks; future SQL/statement/connection lifetime
drain must precede unmap/owner close. The current callback scheme is not proof of
that mapped-pointer lifetime contract.
