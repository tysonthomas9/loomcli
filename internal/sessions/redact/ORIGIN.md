# Origin

This package is a port of `redact/` from the
[entireio/cli](https://github.com/entireio/cli) project.

## Upstream

- Repository: https://github.com/entireio/cli
- Path: `redact/redact.go`, `redact/redact_test.go`
- License: MIT (Copyright (c) 2026 Entire Inc.) — see ../transcript/LICENSE.upstream
- Ported: 2026-04-17

## Scope

Secret redaction for captured agent transcripts. Combines:

1. **Entropy-based detection** — flags alphanumeric/base64-ish segments
   with Shannon entropy above 4.5 (empirically catches API keys, JWTs,
   Bearer tokens).
2. **Pattern-based detection** — gitleaks' default ruleset (180+ known
   secret formats: AWS, GitHub, Stripe, Slack, etc.).

A substring is replaced with `REDACTED` if either method flags it.

## Public API

- `String(s)` / `Bytes(b)` — redact plain text
- `JSONLContent(s)` / `JSONLBytes(b)` — redact JSONL, walking parsed
  objects and skipping structural fields (`filePath`, `cwd`, `*id`,
  `*ids`, `signature`, image content blocks)

## Local changes vs upstream

- None beyond package-level doc comment pointing to loom's capture path.

MIT license text is reproduced in `../transcript/LICENSE.upstream`.
