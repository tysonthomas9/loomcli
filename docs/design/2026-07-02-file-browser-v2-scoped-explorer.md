# File Browser v2 — Scoped Explorer with Editing

**Date:** 2026-07-02
**Status:** Proposal (rev 3 — no file guards, full CRUD, history exploration)
**Verified against:** commit `75c2fa6c` (feat/workspace-file-browser)
**Supersedes:** the 3-tier roadmap (`loom-file-browser-vscode-roadmap.md`) — Tier 1 of
that doc shipped in `75c2fa6c`; this proposal replaces its Tiers 2–3 with a new
objective.

## Objective

One file browser component that can be **initialized at any of three roots** —
workspace folder, a single repo checkout, or an agent worktree — with **view, edit,
and full file CRUD for any file, no policy guards**, plus VSCode's layer-1
exploration features:

1. Explorer tree — icons, expand/collapse, reveal-active-file, git decorations,
   context-menu CRUD (new file/folder, rename, move, delete)
2. Quick Open (Cmd+P) — fuzzy file-name finder, recency-ranked
3. Global search (Cmd+Shift+F) — text search with include/exclude globs + replace-across-files
4. Find in file (Cmd+F), go-to-line
5. Tabs, split editor groups, "Open Editors" list
6. Breadcrumbs — path segments + symbol trail

…and VSCode's layer-3 **history exploration**:

7. Git gutter (changed-line marks), diff editor, blame
8. Timeline view — per-file history of commits and saves
9. Source-control panel with per-file diffs

**Design stance:** this is a localhost, single-operator tool; the browser behaves
like a plain filesystem editor. No sensitive-file denylist, no `.git` write denial,
no running-agent lock checks, no stale-write (`If-Match`) preconditions. The only
invariants are *structural*: every operation is confined to the resolved scope root,
and symlinks are not followed. Consequences are recorded as accepted risks in §5.

Explicitly **out of scope**: anything requiring language servers (go-to-definition,
references, hover, diagnostics-based error badges), live file watching (recursive
fsnotify), minimap.

---

## 1. Core architecture: scoped roots

Everything hangs off one seam that already exists and was built for this:
`resolveScopeRoot` (`svcimpl/file_service.go`, today workspace-only, with a comment
anticipating exactly this change). It stays a simple resolver — with no per-scope
policy to carry, no descriptor struct is needed:

```go
// resolveScopeRoot gains two cases; signature unchanged.
func (s *fileServiceImpl) resolveScopeRoot(wsID string, scope FileScope, target string) (string, error)
```

| Scope | Target | Root |
|---|---|---|
| `workspace` | — | `ws.Path` (spans all repos + worktrees) |
| `repo` | repo name (validated against the workspace's repo list) | `<ws.Path>/<repo>` |
| `agent` | agent name (validated against the agent registry) | `ResolveAgentWorktree` |

- `target` is validated against workspace state (repo list / agent registry), never
  path-joined from raw input.
- **`.git` is display-hidden, nothing else.** Directory listings omit `.git`
  entries at every scope (the existing `hiddenScopeSegments` listing filter, kept
  for `.git` only) — matching VSCode's default `files.exclude`. This is rendering
  only: reads, writes, and CRUD to `.git` paths by explicit path still work — no
  guard. Everything else, including `node_modules`, is listed. The
  `pathHasHiddenSegment` read/list *refusals* are dropped, as is `node_modules`
  from the FE reveal-skip list. Search and Quick Open apply *default exclude
  globs* for `.git`/`node_modules` — a signal/performance default the user can
  override per query, not a policy.
- The sensitive-file denylist (`isDeniedPath`: keys, certs, `.env*`) is **removed
  from both read and write paths** — `.env` editing is an explicit requirement.

**Endpoint unification.** The scoped family becomes the only family:

```
GET    /api/workspaces/{ws}/files/tree?scope=&target=&path=
GET    /api/workspaces/{ws}/files?scope=&target=&path=
PUT    /api/workspaces/{ws}/files?scope=&target=&path=          (create or update)
DELETE /api/workspaces/{ws}/files?scope=&target=&path=&recursive=1
POST   /api/workspaces/{ws}/files/mkdir?scope=&target=&path=
PATCH  /api/workspaces/{ws}/files/move   {from, to, overwrite?}
GET    /api/workspaces/{ws}/files/index?scope=&target=           (quick-open, new)
POST   /api/workspaces/{ws}/files/search?scope=&target=          (global search, new)
GET    /api/workspaces/{ws}/files/git-status?scope=&target=      (decorations + SCM, new)
GET    /api/workspaces/{ws}/files?scope=&target=&path=&rev=      (content at a git rev, new param)
GET    /api/workspaces/{ws}/files/diff?scope=&target=&path=&from=&to=  (unified diff, new)
GET    /api/workspaces/{ws}/files/history?scope=&target=&path=   (timeline: commits + saves, new)
GET    /api/workspaces/{ws}/files/blame?scope=&target=&path=     (blame, new)
```

The four history endpoints share one net-new helper:
**`resolveContainingCheckout(root, relPath)`** — walks up from a file to its
enclosing repo checkout (`<ws>/<repo>` or `<ws>/worktrees/<repo>/<agent>`), so git
operations run in the right repo regardless of scope. At repo/agent scope it is
trivially the root itself.

The existing agent-name-keyed routes (`/agents/{name}/files*`) become thin delegates
to `scope=agent` and are deprecated. One endpoint family → one service core → one
structural envelope (`ValidatePathWithinDir`, symlink skip).

**Frontend.** One `FileBrowser` component parameterized by `{scope, target}`:

- `FilesPage` hosts it with a scope switcher (workspace / per-repo / per-agent).
- The agent detail panel embeds the *same component* with `scope=agent`, replacing
  `FileEditorPanel`'s bespoke browser over time — the two browsers converge into one.
- A browser instance shows **one scope at a time**; switching scopes swaps the tab
  set. Tab/UI state persists per `(workspace, scope, target)` key. No mixed-scope
  tab strips in v1.

---

## 2. Write path & file CRUD

### Writes

`PUT` on the shared scoped core: `ValidatePathWithinDir` → symlink refusals →
`AtomicWriteFile` (existing perms preserved, 0644 for new files). Create = PUT to a
nonexistent path (parent must exist — the tree's "New File" flows always have one).
Writes are **last-writer-wins**: no lock check, no etag precondition. A save that
races an agent or another save silently takes the file; accepted (§5). The FE still
tracks *its own* dirty state (`useFileEditor`: dirty, Cmd+S, discard guard) — that's
editor UX, not a server guard.

- **Size cap:** the 1 MB edit cap stays (practical, not policy). Larger files return
  a truncated read (`truncated: true` added to the read DTO) and render read-only
  with a banner, replacing today's 413.

### CRUD (net-new endpoints + service methods)

| Op | Semantics |
|---|---|
| `DELETE ?path=&recursive=1` | File: `os.Remove`. Directory: 409 `directory not empty` unless `recursive=1`, then `os.RemoveAll`. |
| `POST /mkdir?path=` | `os.MkdirAll` confined to root; 409 if a file exists at path. |
| `PATCH /move {from, to, overwrite}` | `os.Rename` — both endpoints validated within the same scope root; 409 if destination exists without `overwrite`. Rename = move within the same directory. Cross-scope moves out of scope. |

All three share the structural envelope (within-root, symlink-refusing `Lstat`
checks on the source and destination parents). No other checks.

### Frontend CRUD surface

- **Tree context menu**: New File, New Folder, Rename (F2), Delete (Del/⌫),
  Duplicate, Copy Path. Inline-edit input for new/rename (VSCode-style) rather than
  modal dialogs.
- **Delete confirm** is a standard FE confirmation dialog (with "don't ask again"
  for files) — UX convention, not a server guard. Non-empty directories always
  confirm and send `recursive=1`.
- **Move**: v1 is context-menu "Move to…" with a folder picker; **drag-to-move in
  the tree** ships as the enhancement step (dnd-kit is present, but tree
  drop-onto-folder is a net-new interaction pattern vs. the existing sortable
  lists — budgeted separately in Phase D).
- **Tab bookkeeping**: rename/move retargets any open tab's path (and its MRU
  entry); delete closes affected tabs (dirty tabs prompt once); git decorations and
  the quick-open index refresh after any CRUD op (same pull-based freshness).

---

## 3. Feature designs

### 3.1 Explorer tree (mostly shipped)

Shipped in `75c2fa6c`: icons, expand/collapse, keyboard nav (`role="treeitem"` +
`aria-activedescendant` + type-ahead), filter, jump-to-folder, resizable split.
Remaining:

- **Reveal-active-file**: wire tab activation → `revealPath` (today only on mount).
- **Context-menu CRUD** (§2) — the tree's biggest net-new piece.
- **Git decorations**: color file names by status from the git-status endpoint
  (modified = amber, added/untracked = green, deleted = strikethrough in Open
  Editors), bubble a dot up parent folders.
- **Badges**: VSCode's *error* badges come from LSP diagnostics — out of scope. The
  honest substitute with real signal here: **conflict badges** (`UU` from porcelain),
  which matter in a worktree-heavy product and come free with the status endpoint.

### 3.2 Git status endpoint (net-new, feeds decorations)

`GET .../files/git-status?scope=&target=` → `{ "<path>": "XY", ... }` (root-relative).

- Repo/agent scope: one `git status --porcelain` against the root. The existing
  `getChangedFiles` plumbing strips the XY codes positionally — this needs a new
  parse that **keeps** them; the exec plumbing is reused.
- Workspace scope: enumerate the workspace's repo checkouts + agent worktrees, run
  per-checkout, prefix paths with the checkout's workspace-relative dir. Bounded by
  the workspace's own repo/agent count.
- Refresh triggers (same "cheap freshness" model as the tree, no watching): tree
  load, after save/CRUD, window focus, SSE `onReconnect`.

### 3.3 Quick Open (Cmd+P)

- **Backend**: `GET .../files/index` — recursive `filepath.WalkDir` from the scope
  root (patterns exist in `internal/driver/register.go`), symlink-skipping,
  default-excluding `.git`/`node_modules`, returning relative paths only. Hard
  caps: ~50k entries, walk time budget; over-cap responses set `truncated: true`.
  Short-TTL server cache keyed by scope root, invalidated by this browser's own
  CRUD ops.
- **Frontend**: command-palette overlay; client-side fuzzy subsequence scoring (no
  dependency needed at 50k paths); **recency ranking** from a per-`(ws, scope)` MRU
  list of opened files (extends the existing persisted-tab storage). Enter opens the
  file; the index refetches on palette open if stale.

### 3.4 Global search (Cmd+Shift+F) + replace

- **Backend**: `POST .../files/search` `{query, regex?, include?, exclude?, caseSensitive?}`.
  No hard `rg` dependency — the containerized deployment (`deploy/server/Dockerfile`)
  is distroless with no shell, and the host runtime (`loom serve` / desktop) only
  *sometimes* has ripgrep installed. Baseline is a **bounded Go walk** sharing the
  index endpoint's walk core (an `exec.LookPath("rg")` fast path can be added later
  if search latency demands it): skip binaries via
  `misc.IsBinaryContent` (already cross-package), skip >1 MB files, hard caps on
  files scanned / total bytes / matches / wall time; response carries
  `limitHit: true` when clipped (no silent truncation). Globs via `path.Match` on
  root-relative paths; `.git`/`node_modules` excluded by default, overridable.
  Results: `[{path, matches: [{line, col, preview}]}]`.
- **Frontend**: sidebar search panel (VSCode-style) with results grouped by file;
  click → open tab at line (CM6 dispatch to position).
- **Replace-across-files is client-driven**: FE fetches each affected file, applies
  replacements, shows a per-file diff preview, then PUTs per file. No bulk-write
  endpoint; every write flows through the one shared path. Files are independent —
  no cross-file atomicity promise (matching VSCode). With no preconditions, a file
  that changed between search and replace gets the replacement applied to the
  *fetched* content — the preview step is what keeps this predictable.

### 3.5 Find in file + go-to-line

Already bound in CodeMirror (`searchKeymap`: Mod-F, Mod-Alt-G). Remaining: forward
`searchOpen` through `WorkspaceFilePane`, add a find affordance in the pane header,
and add Ctrl+G/Cmd+G as a familiar go-to-line alias. XS.

### 3.6 Tabs, split editor groups, Open Editors

- **Promote tab state to a store** (vanilla `createStore` + context, the repo's
  Zustand pattern): the state now spans two tab strips, an MRU list, per-tab dirty
  flags, CRUD retargeting, and the Open Editors section — too much for
  component-local `useState`. Persisted schema versioned:
  `{v: 2, groups: [{tabs: [{path}], active}], mru: [...]}` keyed by
  `(ws, scope, target)`; v1 bare-string tabs migrate on load.
- **Split groups**: v1 is exactly two groups. "Split right" opens the active file in
  the second group; each group has its own tab strip + active file; one shared
  `ResizeHandle` between groups; drag-tab-between-groups deferred. Closing the last
  tab of group 2 collapses the split.
- **Open Editors**: a collapsible section above the tree listing open tabs per group
  with dirty dots; click activates; matches VSCode's placement.

### 3.7 Breadcrumbs + symbol trail

- Path-segment crumbs shipped (click reveals folder in tree).
- **Symbol trail without LSP**: CodeMirror already builds a **lezer syntax tree
  client-side** for every highlighted language. Walk the tree at the cursor position
  for named declaration nodes (functions, methods, types, classes) to render the
  trailing crumbs — and the same extraction powers a **Go to Symbol in file**
  (Cmd+Shift+O) palette for free. Ship for go / js-ts / python first; other languages
  gracefully show path-only crumbs. This is the only "symbol" feature that is honest
  without a language server.

### 3.8 History exploration (layer 3)

All of these reuse the exec plumbing in `cli/git` and the containing-checkout
resolver (§1); each server piece is a bounded `git` invocation plus a parse.

**Git gutter (changed-line marks).**
- Base content via the read endpoint's new `rev` param (`rev=HEAD` →
  `git show HEAD:<repo-relative-path>` in the containing checkout; same binary/1 MB
  rules, same `truncated` flag).
- The FE diffs the current buffer against the base **client-side** using
  `@codemirror/merge`'s diff (new dependency, first-party) and renders
  added/changed/deleted gutter markers. Recompute is debounced on edit; the base
  refreshes on save and window focus. No server-side diff computation for the
  gutter at all.
- Client-side is the only design that can mark *unsaved* edits (the live buffer
  exists only in the browser; VSCode's own quick-diff works the same way), and it
  is bounded: base content is held only for **visible** editors (≤2 groups ×
  ≤1 MB, dropped on tab switch), and the diff runs with `scanLimit`/timeout so
  pathological inputs (minified/generated files) degrade to coarse marks instead
  of burning CPU.

**Diff editor.**
- `GET .../files/diff?path=&from=&to=` returns a unified diff
  (`git diff <from>..<to> -- path`; `to` omitted = working tree). Rendered by the
  **existing `DiffFileViewer`** (unified, `codemirror-lang-diff`). Side-by-side via
  `@codemirror/merge` is a later enhancement, not v1.

**Blame.**
- `GET .../files/blame?path=` → `git blame --porcelain` parsed to line-block
  entries `{sha, author, time, summary}`. FE: a toggle in the pane header renders a
  VSCode-style subtle end-of-line decoration on the active line plus hover detail;
  clicking opens that commit's diff for the file. Skipped for files over the edit
  cap or >5k lines (blame cost grows with history).

**Timeline (commits + saves).**
- `GET .../files/history?path=` merges two sources, kind-tagged:
  - **Commits**: `git log --follow -n 100 --format=…` for the file in its
    containing checkout.
  - **Saves (local history)**: on every `PUT` that overwrites an existing file, the
    prior content is snapshotted under `$LOOM_CONFIG_DIR/file-history/<ws>/…`,
    capped at 20 entries per file (oldest evicted). Honest limitation, consistent
    with the no-watching stance: only saves made *through this browser* are
    captured — agent and terminal writes bypass it.
- FE: a collapsible **Timeline** section under the tree (VSCode's placement) for
  the active file. Click a commit → diff vs its parent in the diff editor; click a
  save → diff vs current, with a **Restore** action (an ordinary PUT).

**Source-control panel.**
- Built entirely on the §3.2 git-status endpoint (XY codes). A sidebar panel
  grouping changed files — at workspace scope grouped per repo checkout, then by
  state (merge conflicts / staged / changes / untracked); at repo/agent scope a
  single group.
- Click a file → diff editor (working tree vs HEAD in v1; index-aware
  staged-vs-unstaged split deferred). **View-only in v1**: no stage / commit /
  discard actions — in this product the agents do the committing; the panel
  answers "what changed."

---

## 4. Phasing

**Phase A — Scoped foundation + CRUD (backend, ~1 wk).** repo/agent cases in
`resolveScopeRoot`; endpoint unification + deprecation shims; scoped PUT;
DELETE/mkdir/move service methods + handlers; denylist removal; hidden-segment
refusals dropped (`.git` kept as a listing-only filter); `truncated` read flag
(one DTO pass across Go/OpenAPI/TS).
*Exit: curl can read, write, create, delete, move at all three scopes; within-root
and symlink structural checks covered by tests.*

**Phase B — Editor shell (frontend, ~1 wk).** `FileBrowser` parameterized by scope +
scope switcher; editable pane via `useFileEditor`; tab store v2 + migration; split
groups; Open Editors; context-menu CRUD with inline rename/new + delete confirm +
tab retargeting; find affordance + go-to-line alias; reveal-active-file.
*Exit: edit/save and all CRUD flows work at every scope from the tree; state
survives reload per scope.*

**Phase C — Navigation power (~1 wk).** Shared bounded-walk core; index endpoint +
Quick Open palette with MRU ranking; search endpoint + search panel; open-at-line.
*Exit: Cmd+P and Cmd+Shift+F work at every scope within the documented caps.*

**Phase D — Decorations & finishing (~1 wk).** Git-status endpoint (incl. workspace
fan-out) + tree decorations + conflict badges; client-driven replace-across-files
with diff preview; drag-to-move in the tree; lezer symbol crumbs + in-file symbol
palette (go/ts/py).
*Exit: modified files visibly badged and clearing on commit; replace runs with
preview; drag-move works; symbol crumbs render for the big three languages.*

**Phase E — History exploration (~1.5 wk).** Containing-checkout resolver; `rev`
read param + diff/history/blame endpoints; local save-history store (capped) wired
into PUT; SCM panel on the Phase-D status endpoint; git gutter via
`@codemirror/merge`; Timeline section with restore; blame toggle.
*Exit: SCM panel lists per-repo changes with per-file diffs; edited lines show
gutter marks against HEAD; Timeline shows commits + browser saves with restore;
blame toggles on eligible files.*

Phases are sequential by dependency (B needs A's writes; C–E reuse A's scope
plumbing and B's tab store; E additionally builds on D's git-status endpoint —
if history matters more than search, D+E can run before C). C and D are internally
independent and can swap by appetite.

---

## 5. Accepted risks (explicit, by decision)

Recorded once, without mitigation plans — these follow from the no-guards stance and
are accepted:

- **`.git` is fully writable.** Overwriting a linked worktree's `.git` pointer file
  breaks that worktree's git ops (status, diff, reset, patch-back); editing a repo's
  `.git/config` can execute commands next time git runs. Deleting `.git`
  recursively de-versions a checkout.
- **Writes race running agents.** Last-writer-wins on saves into a worktree an agent
  is actively editing; no lock check, no stale-write rejection.
- **Secrets are served over HTTP.** `.env*`, keys, and certs are readable and
  writable through the API. Acceptable only while the UI remains localhost-only;
  any future remote-exposure work must revisit this section first.
- **Recursive delete is a real `rm -rf`** within the scope root, behind an FE
  confirm only.

Engineering risks (mitigated in-design):

- **Search/index cost on big trees** — hard caps + `limitHit`/`truncated` flags are
  part of the contract. Freshness is pull-based, bounding cost to user actions.
- **Tab-store migration** — v1 persisted bare-string tabs exist on this branch; the
  v2 loader migrates, not drops.
- **Tree rendering scale** — no virtualization; with `node_modules` now listed,
  huge directories render fully. Acceptable v1; virtualize if profiling says so.

## 6. Explicitly cut (and why)

- **LSP features** (go-to-def, references, hover, real error badges): requires
  per-language servers server-side; different product.
- **Recursive fsnotify watching**: descriptor limits + agent-write event storms +
  no SSE replay for direct broadcasts; pull-based freshness covers the need.
- **SCM write actions** (stage / commit / discard): the panel is view-only; agents
  own the commit lifecycle in this product.
- **Side-by-side diff view, index-aware staged/unstaged diff split, blame on huge
  files**: deferred; unified diff + working-vs-HEAD covers the review job.
- **Local history for non-browser writes**: capturing agent/terminal saves would
  require the file watcher this proposal deliberately avoids.
- **Mixed-scope tabs, drag-tab-between-groups, minimap, streaming large files**:
  deferred until demand.

## Effort legend

Per-phase ≈ 1 developer-week (E ≈ 1.5), excluding review/QA; A and B are the
confident estimates, C–E lean conservative because the walk core, workspace status
fan-out, and history endpoints are net-new.
