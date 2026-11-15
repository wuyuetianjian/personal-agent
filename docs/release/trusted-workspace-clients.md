# Explicit trusted workspace clients

This opt-in is never inferred from platform, an existing session, a project label,
full Files capability, or an unavailable native gate. The host must enable the
separate trusted-workspace service and validate each session admission.

Web offers an unchecked checkbox. The host's explicit trusted_workspace.Enabled
true permits opting in, independently of the full workspace.Files bundle. The
selected mode is retained. A checked request with chat_only or acceptEdits, an
unavailable host, or an existing MCP selection is refused locally without POST;
the user must explicitly change their selection. Read/Search is the entire
capability: no Apply, Undo, Exec or MCP. The warning states that external malicious
replacement and comprehensive historical database-copy detection are outside this
boundary, and Windows non-administrator qualification is still pending.

Terminal CLI: `--new-session --format json --mode plan --trusted-workspace` (or
mode default). Interactive terminal: `/new create confirm plan trusted-workspace`.
No flag modifies resumed sessions. Recovery is GET-only, never a POST retry or
permission upgrade. An unresolved admission's frozen intent includes the opt-in;
server acknowledgements and recovered snapshots must preserve it. Recovery after
reload validates explicit admission evidence against the final snapshot.

Tests include helper/client negative TDD, real CLI argument-to-HTTP transport,
mobile browser default-unchecked and repeat-click behavior, and existing admission
quarantine/recovery regressions. Transport fixtures do not prove native filesystem
safety or host functionality. Native integration is qualified separately.
