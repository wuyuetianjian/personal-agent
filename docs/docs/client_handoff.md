# Client Handoff

## Purpose

This package is intended for a future client-side implementation of Browser Automation in the local-first parallel Agent system.

It contains:

- Architecture design.
- Requirements.
- Security model.
- Implementation phases.
- YAML configuration structure.
- README updates.
- Go contracts and safety skeletons for browser policy, session metadata, privacy redaction, and evidence records.

It does not contain:

- Concrete Go code.
- Browser driver implementation.
- Playwright/CDP client code.
- Production secrets.
- Real browser profiles.
- Real cookies or sessions.

## Client Implementation Responsibilities

The client implementation should add:

- Browser Session Manager.
- Browser Tool.
- Browser Permission integration.
- Browser Sub-Agent.
- Browser runtime adapter.
- Privacy Gateway browser sanitizers.
- Evidence Bus browser event writers.
- Cancellation propagation.
- Tests for permission and privacy rules.

## Suggested Runtime Choices

The design does not require a specific browser runtime. A future implementation may use:

- Chrome DevTools Protocol.
- Playwright.
- Browser-use style semantic automation.
- Native OS mouse/keyboard automation only as controlled fallback.

The preferred implementation should use semantic browser automation when possible and coordinate mouse control only when needed.

## Required Safety Checks Before Release

- Confirm public model prompts do not include cookies, tokens, passwords, or raw secrets.
- Confirm high-risk browser actions require user confirmation.
- Confirm domain allowlist is enforced.
- Confirm evidence records are redacted.
- Confirm session expiry is honored.
- Confirm cancellation interrupts browser work.
- Confirm Browser Sub-Agent cannot bypass Permission Layer.
