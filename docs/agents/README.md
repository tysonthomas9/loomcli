# Agent Runbooks

> **Status:** Current index · *written 2026-07-23*.

Runbooks for coding agents working in this repo. `CLAUDE.md` points here for
both files below; `AGENTS.md` carries the instructions that apply to every
agent regardless of CLI.

| Doc | Purpose | Status |
|---|---|---|
| [`domain.md`](domain.md) | Which domain docs to read before exploring the codebase, why this repo has no `CONTEXT.md` or `docs/adr/`, and how to flag a decision conflict instead of silently overriding it. | Current |
| [`issue-tracker.md`](issue-tracker.md) | The `loom data` runbook: the two fleet-db backends (server vs local), which one holds product work, and how not to mix them. | Current |

## Related

- `AGENTS.md` (repo root) — shared instructions: the mandatory glossary read,
  the terminology handshake, generated-file rules, gate environment, and the
  driver-runtime/sandbox deploy notes.
- [`../README.md`](../README.md) — index of the whole `docs/` tree.
- [`../loom-glossary.md`](../loom-glossary.md) and
  [`../testing-terminology.md`](../testing-terminology.md) — the two mandatory
  reads named by `domain.md`.
