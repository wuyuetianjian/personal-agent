# Experimental Windows read probe

Disconnected code only. No production command imports this package; this does
not enable Files, Exec, MCP, editing, undo, execution, or persisted approvals.

## Authority and scope

`NewRoot` accepts an **already-open trusted local NTFS directory handle**. The
caller is responsible for initial namespace trust and no-delete sharing. It
must not pass a handle opened with backup privilege bypass. The probe duplicates
and owns it; duplication does not tighten sharing. There is intentionally no
path-to-root convenience API, drive/UNC/SUBST binding, or approval restoration.

`Read` accepts up to 32 individual components, not a slash-delimited path. It
pins each parent and opens metadata first, rejecting unsupported attributes,
reparse tags, case-sensitive directories, nonexact normalized long names,
remote/non-NTFS storage, different volumes, and observed file link counts other
than one. A separate data handle must match the pinned object's full ID and
metadata before any bytes are read. All native acquisition uses FILE_OPEN with
no backup intent, inheritance, or sharing relaxation. Errors return no bytes.

Microsoft's [ReOpenFile contract](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-reopenfile)
requires a CreateFile-origin handle. Since descendant metadata handles here
originate with NtCreateFile, the experiment instead reopens the exact basename
relative to its retained parent while the no-delete metadata guard is held,
and compares identity before reading. It does not silently substitute a path
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
primitives, not every point in the top-level read sequence.

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
