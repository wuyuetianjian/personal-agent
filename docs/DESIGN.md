# Local-First Parallel Agent With Browser Automation

## 1. Design Goal

This design extends the local-first parallel personal agent architecture with controlled Browser Automation. The browser is treated as a governed tool, not as an unrestricted remote-control capability. The system must support semantic browser automation first, coordinate-based mouse control as fallback, local browser session reuse, strict privacy isolation, evidence capture, early stop, and cancellation.

Concrete browser runtime implementation will be done later by a client. This package contains planning files, Go contracts, safety policy skeletons, security rules, tests, and configuration structure. It intentionally does not include a browser driver, Playwright/CDP adapter, cookie reader, browser profile reader, password reader, or remote model caller.

## 2. Core Principles

- Local-first retrieval and local evidence storage remain the default path.
- Browser session, cookie, token, and profile data stay on the local machine.
- Public LLMs never receive raw cookies, browser profiles, tokens, passwords, or raw sensitive DOM.
- Browser automation must pass through the Permission Layer before execution.
- Semantic/browser automation is preferred over coordinate control.
- Coordinate mouse control is allowed only as a fallback when semantic targeting fails or is unavailable.
- Browser operations are written to the Evidence Bus.
- Browser tasks can be cancelled when the Leader reaches an early-stop threshold.
- Browser Sub-Agent receives task slices, not unrestricted browser ownership.

## 3. High-Level Architecture

```text
User Request
  |
  v
Request Gateway
  |
  v
Leader Agent
  |
  +--> Local RAG / Memory / Tools
  |
  +--> Task Slicer
          |
          +--> Browser Sub-Agent
                    |
                    v
              Browser Tool
                    |
                    v
              Permission Layer
                    |
                    v
              Browser Session Manager
                    |
                    v
              Local Browser Runtime
                    |
                    v
              Evidence Bus
```

The Browser Tool is exposed to the Agent as a controlled tool with explicit action types. It does not expose raw session data to the Agent. The Browser Session Manager owns profile directories, cookie stores, domain allowlists, session expiry, and local-only persistence.

## 4. Browser Automation Capabilities

The Browser Tool must support:

- Page navigation.
- Element location by role, text, selector, accessibility tree, or DOM query.
- Mouse move, click, double click, drag, and scroll wheel.
- Keyboard text input.
- Keyboard shortcuts.
- Screenshot capture.
- DOM read.
- Accessibility tree read.
- Page title, URL, visible text, and selected element metadata.
- Semantic action execution against target elements.
- Coordinate fallback when semantic targeting is unavailable.

The preferred execution path is:

```text
Task intent
  -> semantic target lookup
  -> permission decision
  -> browser action
  -> observation capture
  -> evidence append
```

The fallback path is:

```text
Task intent
  -> screenshot / accessibility summary
  -> coordinate proposal
  -> permission decision
  -> coordinate mouse action
  -> observation capture
  -> evidence append
```

## 5. Browser Task Delegation

The Leader or a Sub-Agent coordinator can split browser work into Browser Sub-Agent tasks:

- Open a page and summarize visible state.
- Locate a field, button, link, or table.
- Extract visible facts from a page.
- Capture evidence screenshot.
- Compare DOM state before and after an action.
- Prepare a proposed write action for user confirmation.

Browser Sub-Agent must not receive:

- Browser cookies.
- Session tokens.
- Raw saved passwords.
- Full local profile data.
- Permission bypass capability.
- Direct file-system access to profile directories.

## 6. Permission Model

Browser permissions are action-sensitive and context-sensitive.

### Auto-Allow Candidates

These actions may be automatically allowed when domain policy permits:

- Read visible page content.
- Read DOM summary.
- Read accessibility tree summary.
- Capture screenshot.
- Navigate to allowlisted domain.
- Scroll.
- Move mouse.
- Locate elements.

### Confirmation or Elevated Permission Required

These actions require higher permission or explicit user confirmation:

- Login form submission.
- Payment or checkout.
- Account deletion.
- Data deletion.
- Sending messages, emails, comments, posts, or forms.
- Changing settings.
- Uploading files.
- Downloading sensitive files.
- Granting OAuth permissions.
- Accepting legal terms.
- Any action involving password, MFA code, token, private key, or secret.

### Deny by Default

The following must be denied unless explicitly configured:

- Sending raw cookies or session data to any model.
- Sharing raw passwords.
- Navigating to non-allowlisted domains in locked mode.
- Running arbitrary browser extension code.
- Persisting session data outside approved local profile directories.

## 7. Browser Session Manager

The Browser Session Manager owns all browser identity and persistence concerns.

Required fields:

- `session_id`: Logical session identifier.
- `profile_dir`: Local profile directory path.
- `domains`: Allowlisted domains for this session.
- `expires_at`: Expiry timestamp or duration policy.
- `reuse_policy`: Whether existing profile/session may be reused.
- `isolation_policy`: Whether sessions are shared, per-domain, or per-task.
- `storage_policy`: Local-only storage rules.

Session Manager responsibilities:

- Create or attach to a browser profile.
- Reuse existing profile/cookies when allowed.
- Enforce domain allowlists.
- Expire sessions.
- Prevent session material from leaving local storage.
- Produce redacted session metadata for logs and models.

## 8. Privacy Gateway

The Privacy Gateway sits between local browser observations and any public LLM.

Allowed to public LLM:

- Redacted page summary.
- Redacted visible-text summary.
- Screenshot description generated locally or by trusted local model.
- Sanitized DOM outline.
- Sanitized accessibility tree outline.
- High-level action plan without secrets.

Blocked from public LLM:

- Cookies.
- Session tokens.
- Authorization headers.
- Raw password values.
- MFA codes.
- Private keys.
- Full raw DOM when it contains sensitive data.
- Local profile paths when they reveal usernames or organization names.

Identifier handling:

- Username: HMAC or redact.
- Email: HMAC or mask.
- Hostname: HMAC or mask.
- IP address: mask.
- Tokens and secrets: redact or block.

## 9. Evidence Bus

Every browser operation must append evidence.

Evidence event categories:

- `browser.navigation.requested`
- `browser.navigation.completed`
- `browser.read.dom`
- `browser.read.accessibility`
- `browser.screenshot.captured`
- `browser.element.located`
- `browser.action.proposed`
- `browser.action.approved`
- `browser.action.denied`
- `browser.action.executed`
- `browser.action.failed`
- `browser.session.attached`
- `browser.session.expired`
- `browser.cancelled`

Evidence records must include:

- Task ID.
- Agent ID.
- Session ID.
- Domain.
- Action type.
- Permission decision.
- Redacted target metadata.
- Redacted observation summary.
- Timestamp.
- Cancellation state.

Sensitive values must not be written into Evidence Bus in raw form.

## 10. Early Stop And Cancellation

Browser tasks are cancellable. The Leader may cancel in-flight browser work when:

- Required evidence coverage is reached.
- Confidence threshold is reached.
- A safer local/RAG answer is already sufficient.
- The user cancels the request.
- A high-risk permission request is denied.
- Session expires.
- Domain policy blocks further work.

Cancellation must propagate through:

- Leader Agent.
- Task Slicer.
- Browser Sub-Agent.
- Browser Tool.
- Browser runtime action loop.

## 11. Configuration Design

The browser configuration should be a mountable YAML file or service-level configuration. It must not contain raw secrets.

Configuration sections:

- Browser runtime provider.
- Default execution mode.
- Session reuse policy.
- Profile storage policy.
- Domain allowlist.
- Permission policy.
- Privacy gateway policy.
- Evidence policy.
- Cancellation and timeout policy.

See `configs/browser.yaml` for the planning-level configuration schema.

## 12. Project Structure

```text
agent/
  README.md
  go.mod
  docs/
    DESIGN.md
    README.md
    configs/
      browser.yaml
    docs/
      browser_automation_requirements.md
      implementation_plan.md
      security_model.md
      client_handoff.md
      package_completion.md
    superpowers/
      specs/
        2026-09-05-browser-automation-code-skeleton-design.md
  internal/
    browser/
      evidence.go
      permissions.go
      privacy.go
      session.go
      tool.go
      types.go
```

Future client implementation may add:

```text
internal/browser/playwright/
internal/browser/cdp/
internal/browser/subagent/
```

Concrete browser runtime adapters and sub-agent executors are intentionally not generated in this package.
