# Architecture Docs

Narrative documentation for loom subsystems whose story spans more than one Go
package.

## Purpose and Scope

This directory holds the cross-package architecture pages. A subsystem earns a
page here when understanding it requires holding several packages in mind at
once — layering rules, a request path, a lifecycle. Anything explainable inside
one package belongs in that package's doc comment instead.

That split is deliberate, and it decides where to write and where to look:

| Question | Where it is answered |
|---|---|
| What does this package do? | Its package doc comment — `go doc ./internal/<pkg>` |
| What may this package import, and why? | [Module Layering](module-layering.md) |
| What does this domain term mean, precisely? | `CONTEXT.md` |
| Which overloaded word is meant here? | `docs/loom-glossary.md` |
| Why was this decided, and can it change? | `docs/adr/` |
| What are we building and for whom? | `docs/product/` |
| What was investigated, when? | `docs/design/` (dated notes) |

Every package under `internal/` carries a doc comment, so `go doc ./internal/<pkg>`
is the index of the package layer and nothing here duplicates it. `revive`'s
`package-comments` rule keeps it that way — a new package without a doc comment
fails the build.

One package is invisible to `go doc`: `internal/webui/handlers/connectors` is
test-only, and its doc comment lives on its test file. Read the file directly.

## Pages

- **[Module Layering](module-layering.md)** — how `internal/` is partitioned into
  sdk → infra → web → cli, which of those boundaries `depguard` actually
  enforces, and how to place a new package.
- **[Agent Failure Handling](agent-failure-handling.md)** — how a failed agent
  subprocess is classified, and how one decision table keeps the harness retry,
  the auto-loop, and the daemon supervisor from drifting apart.
- **[Terminal System](terminal-system.md)** — the web terminal: tab management,
  backend selection, session lifecycle, the WebSocket state machine, crash
  recovery, and split view.
- **[Issue Detail View](issue-detail-view.md)** — the issue panel and full view:
  entry points, tabbed interface, terminal integration, inline editing, and deep
  links.

## Conventions

Pages here follow a consistent shape, adapted from the structure Devin's
CodeWiki generates (see
`docs/design/2026-09-03-devin-codewiki-patterns-research.md` for what was copied
and what was deliberately not):

- A collapsed **Relevant source files** block naming what the page was written
  against.
- **Purpose and Scope** first, ending with an explicit fence — what this page
  does *not* cover, and which page covers it instead.
- Topic sections carrying prose, then a table or a diagram where one earns its
  place, then a `Sources:` line citing `path:line-range`.
- A closing **Summary** and a deduplicated **References** list.

Two rules keep this affordable to maintain by hand. Cite selectively —
interfaces, invariants, and anything a reader would otherwise go hunting for,
not every sentence. And prefer pointing at enforced code over restating it: a
`depguard` rule or a test is a better statement of a boundary than a paragraph
claiming it holds.

## Known gaps

Stated rather than silently missing:

- **Agent execution, the success path** — prompt construction
  (`internal/cli/agent`), backend invocation and capability negotiation
  (`internal/cli/backends`), and session finalization (`internal/sessions`,
  `internal/cli/sessionfinalize`). The failure path is covered by
  [Agent Failure Handling](agent-failure-handling.md); the rest is covered today
  only by those packages' own doc comments.
- **Workflow driver runs** — `internal/driver`, `internal/driver/sandbox`, and
  `internal/workflows`, including the await/suspend model and the task bridge.
- **Realtime delivery** — `internal/webui/server/realtime` and
  `internal/webui/subscription`. The framing invariant is recorded in
  `docs/adr/0001-sse-framing-single-writer-seam.md`; the fan-out and resumption
  path is not written up.
- The two UI pages above date from July 2026 and have not been re-checked
  against the code since.
