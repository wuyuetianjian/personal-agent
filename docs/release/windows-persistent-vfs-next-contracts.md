# Next persistent VFS contracts (design only, not implemented)

Starting point: a fixed-source, nondefault main-method bridge and independent
connection-bound operation permits. Main-method acceptance is not WAL SQL
acceptance. Existing broker guards exclude only its registered three identities;
they do not cover future third-party objects or rollback/super journals.

No format or journal-mode changes, migrations, PERSIST_WAL coercion, fake absence,
default-VFS mutation, cached module edits, or production capability changes are
part of this plan. Each phase gets a separate native RED boundary and exact
manifest; fixture/stock control failure never counts as feature RED.

## 1. Truthful bounded xAccess: smallest next RED

Add an experimental metadata authority operation, not an ambient pathname access.
The VFS binding recognizes exact canonical main and derived WAL/SHM/journal names;
other names/flags fail IOERR_ACCESS. A recognized but unsupported object must not
be reported absent merely because its role is not in the current broker.

Observe through fresh relative metadata handles under retained qualified parent
lineage. Own handle+error immediately. Qualify name, regular single-link object,
volume/identity and lineage; consume observation handle once. Uncertain close,
permission errors, reparse/ADS/wrong object => error, never absence. Only definite
native name-not-found beneath an already-qualified parent means absent; do not
turn a failed ancestor lookup into absence. The observation does not authorize a
later open: xOpen requalifies independently and must bind the registered identity.

Preserve pinned Windows xAccess semantics deliberately: EXISTS treats a real
zero-length regular file as absent; READ checks existence; READWRITE reflects the
read-only attribute in the stock implementation, not a promise of writable ACL
access. Unit/native tests must distinguish zero-length and nonempty WAL/journal.
Do not copy stock GetFileAttributes(path) or its mutable syscall table.

Suggested exact native top-level contracts:
- TestPersistentAccessExistingWALAndJournalTruth: stock-created WAL and a separate
  nonempty synthetic journal both reported present; main bytes unchanged.
- TestPersistentAccessMissingAndEmptySemantics: definite missing journal false,
  empty EXISTS false but READ true, nonempty EXISTS true. Use isolated fixtures.
- TestPersistentAccessRefusesUnqualifiedObservation: bad name/ADS, real reparse,
  denial/unknown open result, cleanup failure never produce successful absence.
- TestPersistentAccessOpenRequalifies: observe then replace an unregistered test
  object; subsequent open cannot reuse observation as authority.

Run real stock connection control before stub dispatch, then require actual
private xAccess callback hit at the designated missing operation. For existing
protected objects an attempted replacement may itself be denied by current
share constraints; that is not evidence of race-safe replacement handling.
Adding metadata observation of journal does NOT make journal content tool-safe.

## 2. Existing WAL file methods, still not WAL SQL

Expand the bound constructor by pinned AST edits for exact supported main/WAL
roles and flags. A per-connection WAL resource must be a fresh independent open,
not DuplicateHandle or a borrow of a broker long-lived handle. Resolve only the
binding's derived filename and expected identity. Publish methods last; no
CREATE/truncate/delete flags accepted in this phase. Preserve role/purpose on the
resource; reject main-as-WAL and cross-connection substitution.

RED: stock child keeps a real WAL database open with committed frames; private
WAL methods read its exact header/size and short-read behavior, while role mismatch
and wrong names refuse before native access. Main and WAL independent lifetime
counts survive closing either resource. A stock fresh connection still reads the
same rows before/after. This does not claim SQLite pager/SHM integration.

## 3. Existing SHM mapping and actual stock-compatible locks

The stock _winOpenSharedMemory directly opens SHM and bypasses xOpen. It cannot be
left reachable. Extract/rewrite the full closure of _winOpenSharedMemory,
_winShmMap/_winShmLock/_winShmBarrier/_winShmUnmap, _winShmPurge,
_winCloseSharedMemory and _winLockSharedMemory, including mapping/lock/close
helpers and every OS target. Close/unmap must be consume-once with propagated
errors, not stock retry or ignored cleanup failures. All foreign filename globals
and mutable _aSyscall references must disappear from the private closure.

An owner-private node registry keyed by qualified SHM identity/generation replaces
stock filename-global _winShmNodeList. Mapping/DMS resources belong to an explicit
owner-owned shared grant; connection lock resources stay connection-private.
Do NOT relax today's connection checks globally. Node membership, mappings and
locks have distinct lifetimes. Mapping view pointers necessarily outlive one
callback: return a tracked mapping grant with bounds/generation and deferred
unmap drain, not an escaped borrow-only file handle. SQL connection/file lifetime
must prevent xShmUnmap while SQLite still uses its returned region pointers.

Retain actual stock byte offsets, lock masks, local sibling bookkeeping and memory
barriers. Owner-private lists must still coordinate via genuine OS locks with
other private owners and stock processes. Per-file callback locks cannot be held
in an order that lazy SHM acquisition reverses. Operation permits retain callback
life without holding registry locks; mapping registry updates are short sections.

Smallest useful RED uses a stock child that actively holds SHM/DMS and committed
WAL, first proving actual stock SQL reads and competing native locks. Private
xShmMap must map the same qualified bytes (not a copy), private xShmLock must
conflict with stock locks and release them correctly, and two private connections
must not unlock each other's claims. Owner close waits operation/view lifetimes.
Faults after mapping creation, partial mapping, unmap and close poison authority.

First SQL acceptance is intentionally only a held-stock-reader fixture: private
SELECT sees a value present only in committed WAL, not main DB; default-VFS
counters prove no private fallback; stock reader remains usable afterward. The
existing held-WAL SQL RED must remain failing until this whole path is real.

## 4. DMS recovery and lifecycle: separate mandatory gate

Pinned _winLockSharedMemory attempts exclusive DMS. Success means first opener
and legitimately truncates stale SHM before shared DMS acquisition. It is not safe
to always skip this, nor to invoke truncate on an unqualified path. Phase 3 may
explicitly refuse this recovery-needed branch, but cannot claim general WAL read
support. Later test exclusive DMS recovery with valid WAL replay, crash leftovers,
read-only SHM refusal and error ownership under exact stock protocol.

Creation must share workspace read admission, drain admitted readers BEFORE the
native create, qualify/register returned identity BEFORE publication, retain all
uncertain residuals and never delete a pathname that may now refer to another
object. Reopen/deletion uses identity/generation-specific authority and actual
SQLite lock state, not merely zero Go references. Neither local refcount zero nor
last connection close proves absence of external readers.

Mandatory interoperability fixtures: private-first then stock reader; stock-first
then private; all connections close normally and stock subsequently reopens;
stock checkpoint/truncate; last reader removes/recreates sidecars; stock process
creates missing SHM while private authority exists; private/stock crash recovery.
Check row values, WAL durability, absence of leaked locks/views/handles and ordinary
neighbor reads. No permanent no-share-delete handle may be advertised as passing
this lifecycle just because it prevented the legitimate stock operation.

## Unresolved confidentiality and namespace boundary

Current broker's long-lived handles disallow share-delete and keep old identities
protected. Replacing them with metadata DuplicateHandle does not remove the
original FILE_OBJECT share constraints. Closing guards to permit ordinary stock
retirement can expose fresh external SHM/WAL identities before registration.
Keeping old identity exclusions helps only the old object; name checks alone do
not cover aliases or hard links to newly externally created objects. Notifications
are after-the-fact and cannot close this race. Directory handle pinning alone also
does not prohibit same-user namespace/reparse mutation.

Therefore full external-create compatibility plus concurrent unrestricted tool
reads is NOT established by these phases. Before production, choose and prove a
security boundary capable of excluding storage content before externally created
objects become tool-readable, including journal families, aliases and surviving
sensitive objects after SQL close. This may require an enforceable separate
storage/tool authority boundary; it cannot be silently obtained by blocking all
neighbor reads, changing ACLs/accounts, moving existing data or changing SQLite
mode. Such consequential design changes need explicit scope/authorization.
Until resolved, experiments are isolated fixtures and production remains disabled.
