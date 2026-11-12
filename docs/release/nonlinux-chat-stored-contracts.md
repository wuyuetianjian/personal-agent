# Non-Linux usable chat and stored metadata contracts

Three additional API contracts run on the actual non-Linux implementation:
`TestUnavailableNativeChatStreamAndHistory`,
`TestUnavailableNativeStoredSkillOwnership`, and
`TestUnavailableNativeRequestsFailBeforeEffects`.
They do not replace or skip any existing workspace-positive test or turn on any
native capability. The prior full macOS regression remains a separate result.

Explicit chat_only admission must succeed before decoding a nonempty SessionView.
A real fixture provider Stream completes a message with no tool definitions;
HTTP snapshot/history retain its Unicode response. Stored-only skill validation,
preview, confirmed import and version/request recovery use the real API/SQLite,
remain disabled/not-evaluated, and preserve operation ownership across two real
chat-only sessions. These tests use synthetic input and no live model/service.

Workspace-required modes must fail with exact 409
workspace_unavailable_choose_chat_only before a SessionView is constructed.
Valid MCP configuration must fail at service construction with its exact current
capability error and no child marker or published MCP admission. A new explicit
chat_only request remains usable. Rejected admission/audit records are allowed;
no false promise of zero audit writes is made. Native domain session/workspace/run
publication is checked separately.

This addresses misleading diagnostics in older fixtures: decoding a 409 error as
a session produces an empty ID and unrelated later 400/conflict/timeouts. Likewise,
waiting for an MCP marker after setup already returned capability unavailable
mislabels an expected refusal as a concurrency timeout. No old fixture is changed
by this additive slice. Linux execution is not relabeled macOS evidence; local
cross-compilation checks syntax only, and actual macOS execution is required.
