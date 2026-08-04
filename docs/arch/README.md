# Architecture Docs

> **Status:** Current index · *written 2026-07-23*. Per-doc status is in each
> doc's own banner.

As-built architecture for shipped web-UI subsystems: what the components are,
which handler serves them, and how data flows between the two. These are
descriptive. When one of them disagrees with a proposal in
[`../design/`](../design/README.md), the proposal is the thing that is wrong —
unless the design doc explicitly claims canonicality for that subject (see the
file-explorer split below).

| Doc | Purpose | Status |
|---|---|---|
| [`terminal-system.md`](terminal-system.md) | The tabbed xterm.js surface relayed over a WebSocket to a server-side PTY. Documents the **two independent terminal paths** with different lifetimes and handlers — confusing them is the most common mistake in this subsystem. Not tmux-backed. | Current · rewritten 2026-07-23 against the post-tmux tree |
| [`file-explorer.md`](file-explorer.md) | The one `components/FileExplorer/` tree that renders every file surface (Files page, Agents "files" tab, agent detail panel), the Go service behind it, and the `/api/workspaces/{ws}/files/*` route family. | Current |
| [`issue-detail-view.md`](issue-detail-view.md) | Issue details in its two modes — slide-out panel and full-page — and the data hooks and sub-components both share. | Current |

## Canonicality split for the file explorer

Three docs cover this subsystem and each owns a different question:

| Question | Doc |
|---|---|
| What are the components and how does data flow? | [`file-explorer.md`](file-explorer.md) |
| What is allowed, and how are scopes and paths contained? | [`../design/workspace-file-browser-security.md`](../design/workspace-file-browser-security.md) |
| What is the intended information architecture? | [`../design/2026-07-07-file-explorer-v3-unified-tree.md`](../design/2026-07-07-file-explorer-v3-unified-tree.md) |

[`../design/2026-07-02-file-browser-v2-scoped-explorer.md`](../design/2026-07-02-file-browser-v2-scoped-explorer.md)
is superseded and is not a behaviour reference.

## Related

- [`../loom-glossary.md`](../loom-glossary.md) — read first; `terminal`,
  `session`, `agent`, and `workspace` all have loom-specific senses.
- [`../api.md`](../api.md) — generated endpoint reference for the routes these
  subsystems call.
- [`../design/README.md`](../design/README.md) — proposals and decision records.
