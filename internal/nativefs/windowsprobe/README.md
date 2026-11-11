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
path-to-root convenience API, drive/UNC/SUBST binding, or approval restoration.

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
metadata object's full ID and metadata before traversal or reading; directory
metadata and I/O guards remain retained throughout the read. All native acquisition uses FILE_OPEN with
no backup intent, inheritance, or sharing relaxation. Errors return no bytes.

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
