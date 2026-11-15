# Explicit trusted workspace core integration

This is a new, explicit immutable session policy, not an upgrade of saved sessions
or a claim that earlier experimental lease/VFS isolation is production-ready.
`TrustedWorkspace` is absent/false for older JSON. The default native capability
and full Files bundle retain their existing meaning. A trusted policy selects
only platform Read/Search. Replace, Undo, Exec and MCP remain unavailable.
Linux/other targets have a refusing trusted adapter fallback; existing Linux
non-trusted workspace behavior is unchanged.

PrepareWorkspace canonicalizes the selected root and main DB paths, refuses a
root containing the agent DB, and overwrites caller-supplied ProtectedPaths using
PRAGMA database_list. Each main path reserves the main, -wal, -shm and -journal
names, including sidecars not yet present. The shared protection check compares
absolute cleaned names case-insensitively and os.SameFile against currently known
objects. Ordinary neighboring files are not denied as a directory-wide shortcut.
This is not identification of historical DB copies or protection from another
program maliciously swapping objects; the user explicitly selected that narrower
trusted-workspace contract. Platform readers retain responsibility for their
own containment, metadata, size, cancellation and cleanup checks.

Context tool definitions, proposal/execute permissions, root identity checks,
scoped knowledge ingestion and run/session revalidation use the stored policy.
The host-facing TrustedWorkspaceCapability is separate from Files. API/CLI/Web
host/session opt-in is implemented separately, not inferred by this core.

TDD: the initial portable contracts failed for unsafe path acceptance, known
object leakage and capability escalation. The new helper/policy implementation
passes those contracts. Real hardlink identity, reserved absent sidecar names,
normal neighbor access, preparation-derived paths and old JSON defaults are
covered. Linux tests and cross-compilation do not qualify Darwin/Windows native
readers or a Windows non-administrator account; native CI remains required.

## Current-host authorization (restart boundary)

`NewStore` defaults to host-off. `NewStoreWithOptions` accepts immutable
`StoreOptions{TrustedWorkspace, TrustedWorkspaceRoot}`; the selected host root is
canonicalized once. Invalid/empty roots do not grant authority. Session JSON only
records intent. Every workspace or transactional policy load derives an unexported
runtime grant from the current Store and matching canonical root. No runtime grant
is serialized. Loading history still returns the old binding; host-off or a host
now selecting a different root gives no file tools or operational identity grant.
Preparation rederives authority rather than trusting a copied policy. Transaction
helpers for knowledge, skill execution and job validation explicitly receive the
owning Store; omission means no trusted grant. Branch/queue validations rederive
the current host grant too. Existing per-operation root identity checks remain.

The portable restart contract writes a previously granted policy to SQLite and
loads it with default host-off and a different-root host. It verifies readable
historical bindings, no runtime grant, no context tools, denied identity and
Prepare, and denied knowledge/skill authority. A compiling permissive stub failed
this contract before the repair; compile failures were not counted as behavior
RED. Native platform acceptance and end-to-end host flags remain separately tested.

Reserved names also compare case-insensitive basenames plus existing parent
`os.SameFile` identity, so a directory alias does not expose a currently absent
sidecar name. A real Unix parent symlink fixture first qualifies the shared
parent identity, then checks absent SHM-name refusal and a normal neighbor.
This portable contract does not claim to exhaust native Unicode name semantics.
