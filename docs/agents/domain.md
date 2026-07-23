# Domain Docs

How the engineering skills should consume this repo's domain documentation when
exploring the codebase.

This repo predates the `CONTEXT.md` / `docs/adr/` convention and uses its own
equivalents. Use the files named below — do **not** create `CONTEXT.md` or
`docs/adr/`.

## Before exploring, read these

- **`docs/loom-glossary.md`** — the shared dictionary + concept map (request
  lifecycle, object model, the four planes). This repo's `CONTEXT.md`
  equivalent, and mandatory per `AGENTS.md`: `loom`, `flue`, `fleet`, `aether`,
  `codex`, `daytona`, `atlas`, and `claude` all mean something specific here and
  collide with general knowledge. Confirm the loom meaning before acting on an
  overloaded term.
- **`docs/testing-terminology.md`** — the testing vocabulary along four axes
  (depth / realness / provisioning / polarity), the trap words (`local`, `live`,
  `real`, `verify`, `gate`), and the terminology-handshake protocol. Read before
  running anything slow or irreversible.
- **Decision & design records** — this repo's ADR equivalent is dated design
  docs under **`docs/design/`** (e.g.
  `2026-06-07-agent-service-driver-version-proposal.md`), plus architecture
  notes under **`docs/arch/`** and specs under **`docs/product/`**. Read the
  ones that touch the area you are about to work in.

If a referenced file doesn't exist, **proceed silently**. Don't flag its absence
and don't suggest creating it upfront. `/domain-modeling` (reached via
`/grill-with-docs`) adds to these lazily, when terms or decisions actually get
resolved.

## File structure

Single-context repo (one Go module, `github.com/tysonthomas9/loomcli`):

```
/
├── AGENTS.md · CLAUDE.md
├── docs/
│   ├── loom-glossary.md        ← CONTEXT.md equivalent (glossary + concept map)
│   ├── testing-terminology.md  ← testing vocabulary + handshake protocol
│   ├── design/                 ← ADR equivalent: dated design/decision docs
│   ├── arch/                   ← architecture notes
│   ├── product/                ← product specs
│   └── agents/                 ← agent runbooks (this file, issue-tracker.md)
└── internal/                   ← Go source
```

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
