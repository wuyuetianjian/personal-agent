# Native filesystem foundation: experimental, disconnected

## Release status

macOS and Windows Read, Search, Replace, Undo, Exec and MCP stdio remain
**disabled**. The experiments under `internal/nativefs` are not imported by the
application. They do not supply a workspace binding or restore stored approval.
Passing their tests must not be interpreted as a production capability gate.

The previously verified baseline is commit
`a819715c1f35a84eef72fde9535c0102aa505441`, with target-kernel results in
[Actions run 42946625699](https://github.com/wuyuetianjian/personal-agent/actions/runs/42946625699).
Its refusal contracts passed on both platforms. Full native regression suites
still failed where they expect unavailable workspace and MCP capabilities.
Those failures remain visible; this stage does not remove their assertions.

## Scope of this stage

The macOS experiment starts from an **already-open directory supplied by the
caller**. Windows additionally has a restricted local-volume acquisition route;
all results own qualified handles. They do not claim that an arbitrary path,
drive alias, symlink, mount or restarted process has acquired an authorized root.
There is no enabled application call site for these experiments today.

### macOS / APFS

Observe a pinned directory and descend through individual no-follow directory
opens. Inspect leaf metadata without opening the leaf or returning file bytes.
Reject symlinks, non-regular objects, hardlinked leaves, oversized leaves and
observed volume changes. Handle identities describe observations during the
process lifetime, not durable authorization tokens.

This is deliberately not a file-read adapter. Apple's `O_EVTONLY` still reaches
filesystem/device open operations and is not Linux `O_PATH`. `/dev/fd` cannot
safely upgrade a metadata-only descriptor to read access. `openbyid_np` requires
a private Apple entitlement, does not accept an expected regular-file type or
independent expected generation, and is not an acceptable ordinary-app fallback.
A `stat` followed by a pathname `open` does not close the special-file race.

A clone-based alternative was evaluated against Apple XNU `xnu-11215.81.4`.
`clonefileat` rejects device/FIFO vnode types before source open and can clone a
symlink without following it, but it does not return or bind the prechecked
source identity/link count. A temporary clone's new inode and single link do
not attest the source. It also accepts directories and can clone a hierarchy,
so a file-to-directory race can exceed the read budget before a post-check.
Copied flags can complicate cleanup. This does not qualify strict production
Read and is not used as a fallback. See Apple's
[implementation](https://github.com/apple-oss-distributions/xnu/blob/xnu-11215.81.4/bsd/vfs/vfs_syscalls.c)
and [manual](https://github.com/apple-oss-distributions/xnu/blob/xnu-11215.81.4/bsd/man/man2/clonefile.2).

No atomic Linux-style `RESOLVE_BENEATH | RESOLVE_NO_XDEV` claim is made. Observing
a volume mismatch after a directory open does not prove no automount occurred.
A pinned directory can be renamed outside its former ancestry. APFS Unicode and
case aliases, anti-reuse across restart and protected-name authorization require
further design. Metadata observations do not authorize content access.

### Windows / local NTFS

Experiment with owned directory handles, single-component relative NT opens,
full file IDs, restrictive share modes, reparse/placeholder rejection and bounded
reads from checked handles. The trusted root must have directory I/O access, not
only attribute access. After metadata inspection, each child directory needs a
separate I/O-access handle whose full identity is checked before traversal. Path validation rejects ADS, DOS-device names and
ambiguous components. Exact normalized long-name checks intentionally reject
case and short-name aliases instead of treating string folding as identity.

The disconnected `Root.OpenDirectory` experiment derives a child from an existing
trusted root. It retains independently duplicated ancestor I/O guards, uses the
same identity-checked metadata-to-I/O boundary, and caps cumulative derivation
at 32 levels. Parent closure does not invalidate the child's owned handles.
This is not an arbitrary-path root constructor, durable namespace binding or
production capability. Native tests cover close ordering, ancestry guards,
refusals and leaked handles; the initial trust-anchor problem remains open.

A bounded descendant-directory observation experiment uses a freshly opened
file object rather than a duplicated shared scan cursor. It returns at most 256
names/type/ID observations, never opens listed file contents, and makes no
snapshot, link-count or authorization claim. Unsafe records and exhausted
query/record budgets return no partial results. This is preparation for further
Search design, not an enabled Search adapter.

The first actual NTFS run exposed a mistaken sharing assumption: handles opened
only for attributes did not prevent ordinary `MoveFileEx` renames, even with no
`FILE_SHARE_DELETE`. These metadata handles pin objects, not names. They cannot
be described as no-delete guards. Qualification must exercise the I/O-access
handles and reject a different object inserted between metadata and I/O opens.
The first-run failures remain recorded in
[Actions run 42949760581](https://github.com/wuyuetianjian/personal-agent/actions/runs/42949760581).

This does not establish a universal no-side-effect open primitive. Filesystem
filters can participate in opens; `FILE_OPEN_NO_RECALL` is not a universal no-I/O
guarantee. Synchronous I/O has bounded returned bytes but no hard cancellation
deadline. Ordinary sharing restrictions do not establish a universal snapshot
or exclusion of modern POSIX replacement, preexisting memory mappings or all
concurrent hardlink changes. Creation time can change and is not a permanent
anti-reuse token.

## Qualification and rollout sequence

1. Test the disconnected foundations on actual selected kernels/filesystems.
   Keep explicit failures and untested boundaries. Cross-compilation is only
   build evidence.
2. Resolve root acquisition, aliases, restart identity, private runtime storage,
   Windows DACLs and the remaining read-race contracts. macOS needs a defensible
   leaf-acquisition design before implementing content reads.
3. Design and test replacement and undo separately: private staging, metadata
   policy, identity/preimage checks, crash points, durability, approval lineage
   and unknown effects. Do not claim filesystem compare-and-swap against external
   writers.
4. Qualify executable identity, launch, cancellation and direct-child cleanup.
   Test MCP transport and its independent admission conditions separately.
5. Review actual evidence and the public interface before considering enablement.
   The existing Files capability requires Read + Search + Replace + Undo; a
   read-only experiment cannot enable that bundle.

Actions uses pinned official setup actions, a read-only repository token,
synthetic fixtures and no privilege/configuration changes. Hosted-runner token
privileges and filesystem details must be recorded: a hosted runner does not by
itself prove behavior under a restricted end-user account. No release, main
merge, workflow write token or secret is needed.

## Primary API references

- [Apple open implementation](https://github.com/apple-oss-distributions/xnu/blob/xnu-10063.121.3/bsd/vfs/vfs_vnops.c)
- [Apple open-by-ID entry and syscall implementation](https://github.com/apple-oss-distributions/xnu/blob/xnu-10063.121.3/bsd/vfs/vfs_syscalls.c)
- [Apple public open flags](https://github.com/apple-oss-distributions/xnu/blob/xnu-10063.121.3/bsd/sys/fcntl.h)
- [NtCreateFile](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntcreatefile)
- [ReOpenFile](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-reopenfile)
- [Windows file ID information](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_id_info)
- [Windows rename semantics](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_file_rename_information)
- [Windows sharing-access algorithm](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-fsa/8c0e3f4f-0729-49f4-a14d-7f7add593819)
