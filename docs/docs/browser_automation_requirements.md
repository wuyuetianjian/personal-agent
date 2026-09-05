# Browser Automation Requirements

## Scope

Add Browser Automation as a governed Agent tool. The browser capability must support local session reuse, semantic browser automation, coordinate fallback, permission checks, privacy filtering, evidence recording, early stop, and cancellation.

## Functional Requirements

### BR-001 Browser Tool Integration

Browser Tool must be exposed as a controlled tool available to the Agent runtime. It must support action planning, permission evaluation, execution, observation, and evidence emission.

### BR-002 Local Session Reuse

The system must support reusing existing browser sessions, profiles, and cookies when policy allows. Session/cookie/profile data must remain local and must never be directly sent to a public model.

### BR-003 Input Control

Browser Tool must support:

- Mouse click.
- Mouse move.
- Mouse wheel.
- Keyboard text input.
- Keyboard shortcut.
- Page navigation.

### BR-004 Page Understanding

Browser Tool must support:

- Element location.
- Screenshot capture.
- DOM reading.
- Accessibility tree reading.
- Visible text extraction.
- URL and title reading.

### BR-005 Dual Control Mode

The default mode is semantic browser automation using DOM, role, selector, text, and accessibility targets.

Fallback mode is coordinate mouse control using screen coordinates. Fallback mode must be recorded in evidence and should be used only when semantic targeting is unavailable, unreliable, or blocked.

### BR-006 Browser Sub-Agent Delegation

Leader/Sub-Agent orchestration must be able to slice browser tasks and delegate them to Browser Sub-Agent. Browser Sub-Agent receives bounded task objectives and cannot bypass permissions.

### BR-007 Permission Layer

Every browser operation must pass through Permission Layer.

Read/navigation actions may be automatic when policy allows.

Sensitive write actions require elevated permission or user confirmation, including login submit, payment, delete, send message, post content, change settings, upload files, OAuth grants, and legal acceptance.

### BR-008 Browser Session Manager

Browser Session Manager must manage:

- `session_id`
- `profile_dir`
- `domain allowlist`
- `expiry`
- `reuse policy`
- `local-only cookie/session storage`

### BR-009 Evidence Bus

Browser operations must be written to Evidence Bus with redacted metadata and observations. Evidence must support audit, replay reasoning, early stop, and cancellation.

### BR-010 Public LLM Privacy

If a public LLM participates in browser planning, it may only receive Privacy Gateway output. It must not receive cookie, session, token, raw password, authorization header, or sensitive raw DOM.

### BR-011 Browser YAML Configuration

The project must include browser configuration design in YAML form. The YAML file describes policy and runtime settings only and must not contain secrets.

### BR-012 Go Interface And Safety Skeleton

The repository should include Go contracts and safety skeletons for:

- `browser/session.go`
- `browser/tool.go`
- `browser/permissions.go`
- privacy redaction
- evidence event building

The repository must not include concrete browser driver code, cookie readers, profile readers, password readers, or remote model callers.

## Non-Functional Requirements

- Local-first by default.
- Deny-by-default for high-risk write actions.
- Redaction before remote model usage.
- Auditable evidence trail.
- Cancellable task execution.
- Configurable domain restrictions.
- No hardcoded secrets, URLs, credentials, or environment-specific values.

## Acceptance Criteria

- Design explains how Browser Tool enters Agent runtime.
- Design explains session reuse without public leakage.
- Design lists supported browser actions.
- Design separates semantic mode from coordinate fallback.
- Design describes Browser Sub-Agent task slicing.
- Design defines browser permission categories.
- Design defines Browser Session Manager fields.
- Design defines Evidence Bus events.
- Design defines Public LLM privacy rules.
- Design includes planning-level YAML configuration.
- Design updates README in English and Chinese.
- Go contracts and safety skeletons are covered by tests.
- No concrete browser runtime, cookie, profile, password, or remote model implementation code is generated.
