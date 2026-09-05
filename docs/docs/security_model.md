# Browser Automation Security Model

## Trust Boundaries

The browser introduces a strong trust boundary because it can access authenticated web sessions, personal data, internal tools, and write-capable pages.

The system separates:

- Local Browser Runtime: trusted local execution.
- Browser Session Manager: trusted local session custody.
- Browser Tool: governed action interface.
- Permission Layer: policy and user confirmation gate.
- Evidence Bus: redacted audit trail.
- Privacy Gateway: sanitization boundary for public models.
- Public LLM: untrusted for secrets and raw session data.

## Local-Only Session Data

The following data must stay local:

- Cookies.
- Session storage.
- Local storage.
- Browser profile files.
- Authorization headers.
- Saved passwords.
- MFA tokens.
- CSRF tokens.
- OAuth refresh/access tokens.

Only redacted metadata may leave the local runtime.

## Public Model Restrictions

Public LLMs can help with high-level planning only after Privacy Gateway filtering.

Allowed public model input:

- Page purpose summary.
- Redacted visible text.
- Redacted screenshot description.
- Sanitized element list.
- Sanitized accessibility outline.
- Proposed high-level next action.

Forbidden public model input:

- Raw cookie values.
- Raw token values.
- Raw password values.
- Raw form secret values.
- Full browser profile paths.
- Sensitive raw DOM.
- Authorization headers.

## Permission Levels

### Level 0: Read

Examples:

- Read page title.
- Read URL.
- Read visible text.
- Read sanitized DOM.
- Read accessibility tree.
- Capture screenshot.

Default: can be automatic on allowed domains.

### Level 1: Navigation

Examples:

- Navigate to allowlisted URL.
- Open a new tab.
- Go back or forward.
- Reload.

Default: can be automatic on allowed domains.

### Level 2: Low-Risk Interaction

Examples:

- Scroll.
- Move mouse.
- Focus input without submitting.
- Expand dropdown.
- Click non-destructive navigation element.

Default: policy-dependent.

### Level 3: Write Interaction

Examples:

- Submit login.
- Send message.
- Post comment.
- Save settings.
- Upload file.
- Submit form.

Default: requires confirmation or elevated policy.

### Level 4: High-Risk Transaction

Examples:

- Payment.
- Purchase.
- Delete data.
- Close account.
- Grant OAuth consent.
- Accept legal terms.

Default: requires explicit user confirmation.

## Evidence Redaction Rules

Evidence Bus must store:

- Action type.
- Permission decision.
- Redacted target metadata.
- Sanitized observation summary.
- Screenshot reference if allowed.
- Session ID, not raw session data.

Evidence Bus must not store:

- Raw secrets.
- Raw passwords.
- Raw tokens.
- Raw cookies.
- Sensitive form values.

## Failure Policy

Browser operations must fail closed when:

- Permission decision is unavailable.
- Domain is not allowlisted in locked mode.
- Privacy Gateway detects blocked secret material.
- Session has expired.
- User denies confirmation.
- Browser state cannot be safely observed.

