# P1 Model And Privacy Foundation

## Scope

P1 adds the model and privacy library foundation required by later agent phases. It does not change the P0 CLI execution path, which remains a local no-op task runner.

## Requirements

- Add model trust levels, capabilities, provider metadata, model metadata, and registry validation.
- Add model policy selection for Leader and Sub-Agent roles without sharing Leader configuration into Sub-Agent selection.
- Add mockable provider interfaces for chat, embeddings, and reranking.
- Add an OpenAI-compatible HTTP provider boundary with injectable transport for network-free tests.
- Add a global Privacy Gateway for HMAC-SHA256 pseudonymization, secret redaction, credential dump blocking, and audit metadata.
- Enforce that public remote provider calls fail closed unless a Privacy Gateway is available and the sanitized payload is not blocked.

## Design

The `internal/model` package owns registry, policy, and provider contracts. A registry is built from `config.ModelsConfig`, validates duplicate model IDs, checks provider references, parses trust levels and capabilities, and exposes model lookup. Model policy receives explicit role settings, so Leader and Sub-Agent roles select independently.

The OpenAI-compatible provider accepts an `http.RoundTripper` for tests. Public remote providers sanitize outbound text through a narrow privacy gateway interface before any request is serialized. If the gateway is missing or blocks the payload, the call returns an error before reaching transport.

The `internal/privacy` package owns data-leaving-local-boundary filtering. It pseudonymizes emails, hostnames, usernames, and internal IDs with HMAC-SHA256; redacts secrets such as passwords, tokens, API keys, cookies, authorization headers, database URLs with credentials, and private key material; and blocks credential dumps such as private key dumps, cookie jars, `.env` files with secrets, SSH key material, and cloud credential files.

## Validation

- HMAC pseudonymization must be stable for the same secret and value.
- Redaction fixtures must remove raw passwords, tokens, API keys, cookies, auth headers, and credentialed database URLs.
- Blocking fixtures must prevent private key dumps, raw cookie jars, `.env` secret dumps, SSH keys, and cloud credential files from leaving local scope.
- Model registry and policy tests must cover duplicate IDs, missing capabilities, trust filtering, and independent Leader/Sub-Agent role selection.
- Provider enforcement tests must prove public remote calls cannot bypass Privacy Gateway.
