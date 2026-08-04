# Domain Docs

> **Status:** Current · *audited 2026-08-03*

How the engineering skills should consume this repo's domain documentation when
exploring the codebase.

This repo predates the `CONTEXT.md` / `docs/adr/` convention and uses its own
equivalents. Use the files named below — do **not** create `CONTEXT.md` or
`docs/adr/`.

## Before exploring, read these

- **`docs/loom-glossary.md`** — the shared dictionary for this repo's overloaded
  terms: core objects, role kind, prompt selection, deployment/runtime, the
  workflow platform, and the words that collide with general knowledge. This
  repo's `CONTEXT.md` equivalent, and mandatory per `AGENTS.md`: `loom`, `flue`,
  `fleet`, `aether`, `codex`, `daytona`, `backend`, `session`, `worker` and
  `stack` all mean something specific here. Confirm the loom meaning before
  acting on an overloaded term. It is a dictionary — it does **not** contain a
  request lifecycle or an architecture narrative; for those read `docs/arch/`
  and `docs/design/`.
- **`docs/testing-terminology.md`** — the testing vocabulary along four axes
  (depth / realness / provisioning / polarity), the trap words (`local`, `live`,
  `real`, `verify`, `gate`), the three evidence classes, and the
  terminology-handshake protocol. Read before running anything slow or
  irreversible.
- **Decision & design records** — this repo's ADR equivalent is dated design
  docs under **`docs/design/`** (e.g.
  `docs/design/2026-06-07-agent-service-driver-version-proposal.md`), plus
  architecture notes under **`docs/arch/`** and specs under **`docs/product/`**.
  Read the ones that touch the area you are about to work in.

The two files named above are **mandatory reads**. If either is missing, say so
— a dangling mandatory reference is a defect worth reporting, not routing
around. For everything else (the optional `docs/arch/` and `docs/product/`
pointers, and any per-area doc a skill suggests), **proceed silently** when a
referenced file doesn't exist; don't flag its absence and don't suggest creating
it upfront. `/domain-modeling` (reached via `/grill-with-docs`) adds to these
lazily, when terms or decisions actually get resolved.

## File structure

Single-context repo (one Go module, `github.com/tysonthomas9/loomcli`):

```
/
├── AGENTS.md · CLAUDE.md
├── docs/
│   ├── README.md               ← front door: directory map + status vocabulary
│   ├── loom-glossary.md        ← CONTEXT.md equivalent: dictionary of overloaded terms
│   ├── testing-terminology.md  ← testing vocabulary + handshake protocol
│   ├── api.md                  ← GENERATED WebUI HTTP API reference — never hand-edit
│   ├── api.preamble.md         ← hand-written prose spliced into the top of api.md
│   ├── api.appendix.md         ← hand-written prose spliced into the bottom of api.md
│   ├── security.md             ← auth modes, IPC, subprocess env policy, SSRF analysis
│   ├── design/                 ← ADR equivalent: dated design/decision docs (README index)
│   ├── arch/                   ← architecture notes, as-built subsystems (README index)
│   ├── product/                ← product specs (README index)
│   ├── testing/                ← test-suite runbooks + E2E plans (README index)
│   ├── observability/          ← tracing contract, ops how-to, decision record (README index)
│   ├── epics/                  ← large multi-PR epic plans (README index)
│   ├── agents/                 ← agent runbooks (this file, issue-tracker.md; README index)
│   └── reference/              ← GENERATED env-var / CLI / architecture reference — never hand-edit (README index)
└── internal/                   ← Go source
```

Machine-checked API contract: `api/openapi.yaml` (gated by
`make check-go-api-staleness`). `docs/api.md` is generated from that spec by
`make gen-api-docs` and gated by `make check-api-docs-staleness` — change the
spec or the preamble/appendix and regenerate; never hand-edit `docs/api.md`.

`docs/reference/{env-vars,cli,architecture}.md` are generated the same way, but
from the Go source itself — `make docs-gen`, gated by
`make check-loomdoc-staleness` (a `make check-go` step). Edit the matching
`*.preamble.md` and regenerate; never hand-edit the generated pages.

## Use the glossary's vocabulary

When your output names a domain concept (an issue title, a refactor proposal, a
hypothesis, a test name), use the term as defined in `docs/loom-glossary.md`.
Don't drift to synonyms the glossary explicitly avoids — it flags the overloaded
words precisely because drift is dangerous here.

If the concept you need isn't in the glossary yet, that's a signal — either
you're inventing language the project doesn't use (reconsider), or there's a
real gap (note it for `/domain-modeling`).

## Flag decision conflicts

If your output contradicts an existing design doc (`docs/design/`) or
architecture note (`docs/arch/`), surface it explicitly rather than silently
overriding:

> _Contradicts `docs/design/epic-runner-lead-control.md` — but worth reopening
> because…_

## Related

- [../README.md](../README.md) — index of the whole `docs/` tree
- [../loom-glossary.md](../loom-glossary.md) — the domain dictionary
- [../testing-terminology.md](../testing-terminology.md) — testing vocabulary and
  the handshake protocol
- [issue-tracker.md](issue-tracker.md) — `loom data` runbook and the fleet-db
  backend split
- [../testing/README.md](../testing/README.md) — index of the testing docs
- [../observability/README.md](../observability/README.md) — index of the tracing
  docs
