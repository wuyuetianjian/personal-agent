# Web workspace-mode guidance

## Approved narrow design

When status explicitly reports `workspace.Files.Enabled === false`, Web labels
three workspace modes as unavailable and explains the server's original Reason.
The modes remain selectable so the choice is never silently changed. Clicking
create shows the explanation before admission or navigation operations and sends
no POST. Only the user selecting chat_only changes that intent. Missing or
malformed status is not a capability conclusion; the server still decides.

The alternative of silently selecting chat_only was rejected. Reclassifying the
409 as pre-claim is also incorrect: the backend already records an admission.
Existing quarantine, GET recovery and explicit failed-claim acknowledgement stay
unchanged, including when formerly enabled status has become stale.

## Implementation and tests

A Web-only pure helper is shared by option labels, inline explanation and create
preflight. The existing admission-lock check runs first. No backend, SDK recovery
or native gate changes. Unit tests join the default test command. Browser tests
use the real compiled Web and client: explicit false blocks repeated clicks for
all three modes, preserves choice and creates no recovery record; explicit chat
still creates normally. Unknown/malformed/stale-enabled status tests use labelled
HTTP response fixtures to verify that real client quarantine and GET recovery are
unchanged, not to claim backend native behavior.

TDD: two pure contracts failed against an empty-result stub. The existing compiled
Web browser test failed because `默认审批` lacked the unavailable label, after
setup was corrected to embed the built Web. Initial Chromium socket/setup errors
were not counted as feature RED. Existing headless-shell Chromium is used locally.
No browser or native capability is inferred solely from static/type checks.
