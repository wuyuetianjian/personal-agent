# Experimental Windows read probe

Disconnected code only. No production command imports this package; this does
not enable Files, Exec, MCP, editing, undo, execution, or persisted approvals.

## Authority and scope

`NewRoot` accepts an **already-open trusted local NTFS directory handle**. The
caller is responsible for initial namespace trust and no-delete sharing. It
must supply FILE_LIST_DIRECTORY, FILE_READ_ATTRIBUTES and SYNCHRONIZE access;
the probe verifies these granted rights, but cannot verify the original share
mode. A metadata-only root or root with DELETE access is rejected. The caller must not pass a handle
opened with backup privilege bypass. The probe duplicates
and owns it; duplication does not tighten sharing. There is intentionally no
unrestricted path-to-root convenience API, forwarded/UNC root binding, or
approval restoration. The restricted initial acquisition below is separate.

`OpenDirectory` derives a directory root using individual exact components,
not an absolute path. It duplicates the full inherited I/O-handle ancestry and
uses new directory I/O guards for traversal only after metadata and full-identity
checks. Opening an I/O handle necessarily precedes checking its identity.
The result survives closing its parent; closing it releases its own lineage.
The total derived depth from the original trusted root is bounded to 32, even
across repeated derivations. Ordinary ancestor rename guards stay held, but this
does not establish persistent pathname authorization or exclude POSIX renames.
It does not solve initial root acquisition or strengthen caller sharing modes.

`Read` accepts up to 32 individual components, not a slash-delimited path. It
pins each parent and opens metadata first, rejecting unsupported attributes,
reparse tags, case-sensitive directories, nonexact normalized long names,
remote/non-NTFS storage, different volumes, and observed file link counts other
than one. Metadata-only handles do not participate in ordinary I/O sharing
checks. A separate FILE_READ_DATA/FILE_LIST_DIRECTORY I/O guard must match the
metadata object's full ID and applicable consistency checks before traversal or reading; directory
metadata and I/O guards remain retained throughout the read. All native acquisition uses FILE_OPEN with
no backup intent or inheritance. Sharing is selected before the open as described below, never retried more permissively. Errors return no bytes.

Microsoft's [ReOpenFile contract](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-reopenfile)
requires a CreateFile-origin handle. Since descendant metadata handles here
originate with NtCreateFile, the experiment instead reopens the exact basename
relative to its retained, qualified I/O parent. A retained metadata handle
keeps the original object alive but does not prevent renaming it. Full identity
comparison rejects swaps before using the newly acquired I/O guard. It does not silently substitute a path
open or invoke undocumented directory/open-reparse flag combinations.

## Evidence and limitations

Portable component tests can run on Linux. `GOOS=windows GOARCH=amd64 go test -c`
only proves compilation, **not** native behavior. Native tests must actually run
on local NTFS. Symlink/junction fixture failure is fatal, not a passing skip.

Tests cover bounded empty/limit/over-limit reads, caller/root handle ownership,
case and guessed 8.3 alias refusal, known hardlink refusal, ordinary writers,
cancellation already requested, symlink/junction refusal, conventional
rename/delete while a guard is held, same-object metadata/data identity, and
leaf guard release on normal/error paths. The guessed 8.3 case is not proof
that short-name generation was enabled; actual 8.3 fixture qualification remains
open. Handle release checks include warmed, serialized exact process-handle counts
across repeated nested success/failure paths and root duplicates. The replacement barrier test exercises the lower-level native
primitives, not every point in the top-level read sequence. A deterministic
metadata-only rename/replacement test calls the same acquireIOGuard helper as
Read and verifies that no usable handle for the replacement is returned.

Open qualification gaps include modern POSIX rename/delete, mapped writers,
cloud recall and filter behavior, actual enabled 8.3 aliases, case-sensitivity
mutation, remote/alternate-filesystem fixtures, volume transitions, concurrent
hardlink creation, hostile root binding and restart/approval restoration.
Pre/post metadata is not a snapshot or global hardlink proof. Synchronous calls
cannot enforce hard cancellation or guarantee zero filter side effects. The
absence of a flag or a passing ordinary-file test cannot establish these
properties. Creation time is not a permanent anti-reuse token.

The native volume query relies on Microsoft's documented final-completion
contract: [NtQueryVolumeInformationFile](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/nf-ntifs-ntqueryvolumeinformationfile)
returns STATUS_SUCCESS or an error, with final completion and returned byte
count in IO_STATUS_BLOCK. The probe requires exact STATUS_SUCCESS, final success
and the full device-information size; it does not accept informational statuses.

## Corrected native finding

The first actual Windows run (42949760581) failed the leaf and intermediate
rename-refusal assertions: ordinary Go 1.26.4 os.Rename succeeded while only a
metadata handle was held. Go's os.Rename in that version uses
MoveFileExW(MOVEFILE_REPLACE_EXISTING), not POSIX rename. The original assumption
that READ_ATTRIBUTES|SYNCHRONIZE plus no-delete sharing was a rename guard was
wrong. Microsoft's [sharing algorithm](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-fsa/8c0e3f4f-0729-49f4-a14d-7f7add593819)
conditions sharing checks on participating read/write/delete access, excluding
metadata-only opens. The revised architecture adds identity-qualified I/O
guards, retains the conventional rename/delete refusal assertions on those
guards, and separately reproduces the metadata-only swap to require rejection.
The revised Windows runtime result must be recorded by the native CI gate;
compilation alone is not confirmation that this correction works.

## Derived-root qualification

The test-only revision `836e869` ran on the Windows kernel in
[run 42972017436](https://github.com/wuyuetianjian/personal-agent/actions/runs/42972017436)
and all seven new derived-root tests failed explicitly because OpenDirectory
was not implemented. The implementation must pass those same tests on Windows;
cross-compilation is not a green result. Coverage includes scope restriction,
parent/child and sibling close order, independently retained ancestors, exact
names, reparse refusal, cumulative depth and warmed handle-count checks.
The existing deterministic metadata swap test exercises the shared I/O-guard
helper; it is not a scheduler-controlled race injection into OpenDirectory.

## Bounded descendant observations

`ObserveEntries(ctx, components)` lists one descendant directory without opening
its individual entries. It requires at least one component, so it does not scan
the original caller root. Each call uses OpenDirectory's freshly opened final
NT file object: DuplicateHandle alone would share a directory scan cursor and
the first search expression, which a per-Root mutex cannot isolate.

The NT class-60 query uses an aligned 64 KiB buffer and parses only the actual
IO_STATUS_BLOCK byte count. Bounds apply across all pages: 16 system calls,
1,024 raw records (including dot entries), and 256 returned observations. Only
literal dot/dot-dot entries are omitted. Unsupported names or reparse/offline/
device/recall attributes refuse the entire operation; errors and cancellation
return no partial list. Malformed linked records and invalid UTF-16 are rejected.
ReparseTag is ignored on non-reparse records because that field is undefined.

Returned names, directory flags and 128-bit IDs are observations on the root's
volume, not permission to read, a single-hardlink proof, or a persistent identity.
No per-entry content handles are acquired. The exclusive-leaf test establishes
that success does not depend on obtaining leaf I/O access; the no-leaf-open
property is also an implementation constraint. Concurrent scan tests exercise
independent calls, not deterministic adversarial scheduling. Pre/post directory
metadata can detect some mutations, but results are not an atomic snapshot or a
complete enumeration guarantee under concurrent directory changes. Sync calls
still have no hard cancellation deadline. Filters may participate in queries.

Portable record tests completed RED→GREEN. Test-first revision `ed54287` ran in
[Actions 42975484187](https://github.com/wuyuetianjian/personal-agent/actions/runs/42975484187)
and the native observation contracts failed because ObserveEntries was absent.
The implementation still requires the native no-skip gate; compilation alone
is not evidence of NT query behavior. Native tests cover empty/Unicode entries,
multiple pages, repeated/concurrent fresh scans, independently held ancestor
roots, exclusive leaf access, reparse/protected-name refusal, the 256/257 bound,
cancellation already requested, closure and handle release. Abnormal NT return
statuses, forced query-budget exhaustion and in-flight cancellation are not
fault-injected by these tests.

API reference: [NtQueryDirectoryFileEx](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/nf-ntifs-ntquerydirectoryfileex).

Synchronous query completion follows the routine's nonpending NTSTATUS, not an
assumed equality with IO_STATUS_BLOCK.Status. Only STATUS_SUCCESS consumes the
reported byte count; STATUS_NO_MORE_FILES terminates without reading the buffer.
Pending, overflow and zero-progress success are rejected. This distinction is
covered by a portable completion-classification test and follows Microsoft's
[IO_STATUS_BLOCK contract](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/wdm/ns-wdm-_io_status_block).

## Restricted initial local root acquisition

`OpenLocalRoot` accepts an exact long-name absolute DOS path, not UNC, device
syntax, alternate streams, dot-dot or forwarded paths. It queries the drive's
current first mapping, requires an exact `\Device\HarddiskVolume` plus digits,
and opens that NT device's filesystem root directory directly. It never
re-resolves the DOS letter after the query and does not try historical mappings.
The device name is **nonpersistent**, not a volume identity. This relies on the
trusted OS device namespace and authorizes no path string: the result owns only
the actual qualified local NTFS directory handles. Administrative device remap,
forced dismount or hardware removal are not excluded. No string-based recovery
or fallback open occurs. A direct manually-created drive alias to a supported
volume cannot be distinguished from a system drive letter and is not claimed
rejected; SUBST/path forwarding and network targets are not resolved.

The trailing separator selects the physical filesystem root. The existing
type-neutral no-reparse/no-recall pattern acquires it with normal list, attributes
and synchronize access; NewRoot then requires an actual directory. There is no
backup intent, delete access or share-delete. Each initial component uses metadata followed by an identity-
qualified directory I/O guard. Ancestor junctions/reparse objects and nonexact
long names fail instead of being corrected and retried. Requiring list access
at every ancestor is stricter than traverse-only Windows access.

Initial host-selected components use namespace syntax checks, not the model's
relative protected-name policy: selecting a `.foo` or `data` project directory
does not permit relative tools to access protected names. Initial acquisition
allows 32 components; the selected root starts a separate 32-component relative
derivation budget while retaining initial ancestry. One full lineage can own
65 I/O handles; acquisition temporarily owns duplicates and metadata handles.

This constructor is not an approval mechanism. Production integration still
requires host-owned epoch/lease lifetime, old-token refusal after restart,
transaction-failure cleanup, and object-based exclusion of private runtime/DB
storage. Windows must not call common `EvalSymlinks` before this acquisition,
which would traverse unsupported namespace paths before refusal.

Native contracts include a child with administrator group disabled and no
enabled privilege except SeChangeNotifyPrivilege. The test prefers a linked
non-elevated token; otherwise it explicitly reports `restricted-not-standard-user`.
The latter is not a standard-user-account qualification. There is no elevation,
privileged retry, or host security-setting change. Test-first run
[42986302018](https://github.com/wuyuetianjian/personal-agent/actions/runs/42986302018)
confirmed the reduced child launches and reaches the still-refusing acquisition
stub; the implemented constructor must pass the subsequent native gate.

## Parent flush qualification experiment

The test-only `TestNativeDurableParentQualification` compares directory sharing
and minimum write/append rights against normal NTFS flush. It does not change
the reusable directory/leaf guard policies, implement Replace/Undo, or establish
power-loss durability. The current-token result is not reduced-token or standard
user evidence. Reparse mutation and reduced-token qualification must precede
any reusable directory sharing relaxation. Names and file contents are checked
after fixture handles close; flush itself may persist already-dirty metadata.

A separate test-only junction-mutation experiment establishes a real outside
sentinel target by metadata identity before contrasting a conventional pathname
open with a held-directory relative open after mutation. It tests both write-data
and write-attributes access under the existing and proposed directory sharing.
No outside contents are read through the tested root. Failure to establish the
attack is not containment success; unexpected relative-open success fails.
Reparse removal precedes nonrecursive fixture cleanup; failed removal is reported.

Handle-count diagnostics additionally query ObjectTypeInformation for values
that GetFileType cannot classify. Only bounded object type labels are read, never
object paths. Event/File controls validate this interpretation. These are live
value observations after PSS, not snapshot object identities. Neither the exact
total-count assertion nor the disk-handle-set assertion is relaxed.

The first junction experiment (1981604) confirmed that even the existing
SHARE_READ guard permits WRITE_ATTRIBUTES-based reparse mutation. The full-path
metadata positive control reached the intended outside sentinel. The held-root
relative open instead failed with a resolution error that the original test's
explicit refusal list did not recognize. The follow-up logs the exact NTSTATUS
and additionally accepts only STATUS_REPARSE_POINT_NOT_RESOLVED; unknown errors
and any successful relative open still fail. This measures a specific rejection
behavior, not immutable reparse metadata or a universal race-free guarantee.

## Directory traversal versus observation consistency

Native diagnostics reproduced initial-root refusal when an unrelated change in
a shared temporary ancestor changed only Write/Change time, not directory ID,
Creation, attributes, type or tag. Traversal now compares qualified directory
identity/safety state separately from mutable content-associated metadata. It
keeps Creation and all attributes/tag checks, current inspection and exact names.
File metadata-to-I/O and before/after read checks remain strict, as does the final
ObserveEntries endpoint comparison. Directory sharing policy is unchanged.

This explicitly gives up extra time/allocation-based mutation detection for
ancestor traversal; it does not prove absence of changes that are later restored.
The test-first revision 2e34ba2 failed the new positive directory predicate and
sibling-mutation acquisition expectations on native Windows. Controlled fixtures
set LastWrite explicitly after actual sibling changes because natural NTFS time
updates were not consistently visible; no retry or timing tolerance hides this.
The implementation requires new exact-head native evidence before qualification.

## Directory-only write sharing

A qualified directory I/O candidate now opens with SHARE_READ|SHARE_WRITE, still
without SHARE_DELETE or DELETE access. This allows a separately ACL-authorized
narrow writer handle to flush the same parent; the guard itself gains no write
rights. Ordinary file I/O guards remain SHARE_READ only. Selection uses the
expected metadata type, and actual identity/type/attributes/name are checked
before any traversal, read or flush. A swapped candidate is not used. Existing
caller root sharing cannot be strengthened or relaxed by duplication.

This policy follows the explicit flush and reparse-mutation experiments. It
does not claim that directory metadata is immutable, exclude every filesystem
mutation primitive, qualify standard-user operation, or implement Replace/Undo.
Test-first f5c4636 failed native directory writer opens with sharing violation
while file-writer and directory DELETE/rename refusal tests passed. The new
implementation still needs its exact-head native GREEN result.

## Explicit main-object identity experiment

Only the `nativeprobe_sqlite_identity` build exposes `ExperimentalReadExcluding`.
Its caller supplies 1..16 currently owned protected file handles, never a saved
path or stored identity. The method owns metadata-only, non-inheritable copies
throughout the shared read path, rejects invalid/directory inputs, and refuses a
matching full volume/file ID before the leaf I/O open and again before reading.
Failure to close a protected duplicate also returns no content. This does not
strengthen the shared read path's existing temporary-handle cleanup behavior.
Ordinary `Read` supplies no exclusions;
this experiment does not silently change its existing contract.

The rootlease experiment obtains a duplicate of SQLite's actual main handle
inside the same borrowed SQL connection's Raw callback. The driver extension
exists only in a checksummed temporary module copy used by a dedicated test job.
It is not a production driver fork or complete database protection: WAL, SHM,
journals, attached/temporary databases, directory ancestry and other connection
lifetimes remain outside this slice. Production commands remain disconnected.
The initial missing behavior failed explicitly at f513613 in run 43002158984;
require native evidence for the implementation's exact commit before claiming
these targeted tests passed.
