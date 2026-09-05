# Implementation Plan

This plan is for a future client implementation. It avoids concrete code and describes work packages, expected outputs, and validation checkpoints.

## Phase 1: Browser Capability Contracts

Define the Browser Tool contract and action taxonomy.

Deliverables:

- Browser action list.
- Browser observation schema.
- Browser result schema.
- Error categories.
- Permission request/decision shape.

Validation:

- Every action maps to a permission level.
- Every result can produce Evidence Bus records.
- No action exposes raw session data.

## Phase 2: Browser Session Manager

Implement local browser session custody.

Deliverables:

- Session creation/attachment rules.
- Profile directory policy.
- Domain allowlist enforcement.
- Expiry policy.
- Reuse policy.
- Redacted session metadata.

Validation:

- Existing profile can be reused when policy allows.
- Expired sessions cannot be reused.
- Session/cookie data is never serialized into model prompts.

## Phase 3: Permission Layer Integration

Route every browser action through Permission Layer.

Deliverables:

- Permission classification.
- Auto-allow policy for read/navigation.
- Confirmation workflow for write/high-risk operations.
- Deny-by-default behavior.

Validation:

- Login submit requires confirmation.
- Payment/delete/send-message actions require elevated permission.
- Read-only operations can run automatically on allowed domains.

## Phase 4: Semantic Automation Mode

Implement preferred element-based automation.

Deliverables:

- Role/text/selector/accessibility/DOM element lookup.
- Element confidence scoring.
- Target metadata redaction.
- Observation after each action.

Validation:

- Actions prefer stable element targets over coordinates.
- Failed lookup falls back only when policy allows.

## Phase 5: Coordinate Fallback Mode

Implement coordinate mouse control as fallback.

Deliverables:

- Screenshot-based coordinate planning.
- Mouse movement/click/scroll operations.
- Coordinate evidence records.
- Fallback reason tracking.

Validation:

- Fallback is auditable.
- Fallback does not bypass permissions.
- Fallback is disabled when policy disallows it.

## Phase 6: Browser Sub-Agent

Add Browser Sub-Agent as a bounded executor for browser task slices.

Deliverables:

- Task slice format.
- Browser Sub-Agent lifecycle.
- Cancellation handling.
- Result reporting into Evidence Bus.

Validation:

- Browser Sub-Agent cannot access raw cookies/session.
- Browser Sub-Agent stops on cancellation.
- Leader can merge browser evidence with RAG/tool evidence.

## Phase 7: Privacy Gateway

Add browser-specific privacy filtering before public LLM calls.

Deliverables:

- DOM sanitizer.
- Visible text sanitizer.
- Screenshot description sanitizer.
- Identifier hashing/masking policy.
- Secret blocking rules.

Validation:

- Cookies/tokens/passwords are blocked.
- Public model receives only redacted summaries.
- Privacy violations fail closed.

## Phase 8: Evidence Bus And Early Stop

Connect browser operations to Evidence Bus and orchestration cancellation.

Deliverables:

- Browser evidence event types.
- Redacted evidence payloads.
- Early-stop signal handling.
- Context cancellation propagation.

Validation:

- Browser operations are auditable.
- In-flight browser tasks stop after early stop.
- Denied permission creates evidence without executing action.

## Phase 9: Configuration And Documentation

Finalize configuration and docs.

Deliverables:

- Browser YAML configuration.
- README updates in English and Chinese.
- Security model documentation.
- Client handoff checklist.

Validation:

- No hardcoded secrets.
- Config can be mounted or replaced without rebuild.
- Documentation matches implemented behavior.

