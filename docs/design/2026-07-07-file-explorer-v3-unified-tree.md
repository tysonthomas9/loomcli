# File Explorer v3 — Unified Tree, Two Lenses

> **Status:** Shipped (2026-07-13, `ccf477157`) — canonical for file-explorer
> *information architecture* · *audited 2026-08-03*
>
> The security / containment / concurrency contract is NOT in this doc and NOT
> what §1.2 item 5 below says: see
> [workspace-file-browser-security.md](workspace-file-browser-security.md),
> which post-dates this doc and reverses v2's no-guards stance. Component-level
> architecture: [docs/arch/file-explorer.md](../arch/file-explorer.md).

Status: approved direction (Tyson, 2026-07-07). Supersedes the *information
architecture* of `2026-07-02-file-browser-v2-scoped-explorer.md`; v2's backend
write-path rules, editor internals, search, and history plumbing carry forward
unchanged unless explicitly amended here — except for v2's security stance, which
was reversed after this doc was written (see banner).

## Objective

v2 shipped VS Code's feature list without its information architecture — and
without Loom's. Usage verdict: scope switching is friction, the side panel
hierarchy is inverted (the tree renders below Open Editors / Source Control /
Timeline / two filter inputs), Timeline is unrecognizable as "history of the
active file", and the visuals read as a different app than the Kanban board.

v3 keeps every capability (browse, edit, search, diff, history, split groups)
and rebuilds the frame around the two questions this product actually gets
asked: **"where is this file?"** and **"what did the agent change?"**

Decisions locked with Tyson (2026-07-07):
- Split editor groups: **keep**.
- Drag-to-move: **cut**; replaced by an explicit "Move to…" context-menu
  action. The dnd-kit sensor leaves the tree entirely.
- Changes lens: **converge** with the Agents page — the same explorer renders
  agent-rooted there, retiring `FileEditorPanel`.
- Single-repo agents: **flatten** (files directly under the agent node; repo
  name as dim secondary text on the agent row).

## 1. Core architecture: one tree, checkout-addressed

### 1.1 The checkout is the unit

A **checkout** is one working directory the workspace knows about:

| kind  | identity            | path                          |
|-------|---------------------|-------------------------------|
| agent | (agent, repo)       | `<ws>/worktrees/<repo>/<agent>` |
| repo  | (repo)              | `<ws>/<repo>` (shared checkout) |
| workspace | —               | `<ws>` root (system files)    |

`ops.WorkspaceAgentInfo` already carries `Repos []string`, `RepoGroups`,
`CrossRepo`. v2's agent scope collapses that to one worktree
(`selectAgentRepo` → first allowed repo), making a cross-repo agent's other
worktrees unreachable. v3 fixes this: **file identity becomes
(scope, target, repo, path)** with `repo` optional and defaulting to the
agent's sole/primary repo (exactly today's `selectAgentRepo` result), so all
existing v2 addresses keep working.

### 1.2 Backend deltas (Phase A)

1. **`repo` qualifier on agent scope.** Every scoped file endpoint (list,
   read, write, create/mkdir/rename/delete/move, search, index, git-status,
   history, blame, diff, file-at-rev; the planned save-history endpoint was cut
   along with save snapshots, see §2.2) accepts an optional
   `repo` query/body field, honored only when `scope=agent`. Resolution:
   - `repo` present → validate it's in the agent's allowed set (Repos ∪
     RepoGroups expansion, same logic as `selectAgentRepo`'s allow-map);
     resolve `worktrees/<repo>/<agent>`; 404 if not checked out, 400 if not
     allowed.
   - `repo` absent → current behavior (`selectAgentRepo` first-allowed).
2. **Checkout enumeration + change counts.** New endpoint
   `GET /api/workspaces/{ws}/files/checkouts` (shipped at
   `internal/webui/handlers/misc/module.go:40`, handler
   `handlers/misc/files.go:268` — there is no `/api/v1` prefix anywhere in the
   webui mux) returning, per checkout:
   `{ kind: "agent"|"repo", agent?: string, repo: string, exists: bool,
   branch?: string, change_count: int }`. Reuse the workspace git-status
   fan-out machinery (`file_git_status.go` already enumerates checkouts and
   prefixes paths); `change_count` = number of porcelain entries. Missing
   local checkouts report `exists:false, change_count:0` — never an error.
   File browsing is independent of git health: a checkout whose working
   directory exists remains browsable/readable/editable even when git metadata
   is unreadable. Only git overlays (status decorations, Changes lens,
   history, blame, diff) degrade.
3. **Checkout repair.** Also shipped, unplanned by any design doc:
   `POST /api/workspaces/{ws}/files/checkouts/repair`
   (`internal/webui/handlers/misc/module.go:41`, handler
   `handlers/misc/files.go:282`, body `internal/webui/service/file.go:226`),
   which repairs or provisions a known checkout (`internal/ops/fileops.go:108-112`).
   Frontend counterpart: `components/FileExplorer/checkoutAvailability.ts`.
4. **DTO/codegen**: `make gen-go-api`, `npm run generate:types`,
   `check:generated` as in v2.
5. No other backend changes.

   > SUPERSEDED: three of the six invariants this item carried forward from v2
   > were reversed after this doc was written. Policy guards and a sensitive
   > denylist exist (`internal/webui/fileaccess/access.go:42-77`, reached from
   > `svcimpl/file_walk.go:348,376` via `service.IsSensitiveFilePath`);
   > `.git` is not writable
   > (`svcimpl/rooted_file_store.go:141`); conditional writes / etags exist
   > (`handlers/misc/files.go:347,352-354,375`,
   > `svcimpl/file_service.go:324-345`). "no LSP" and "no file watcher" still
   > hold unchanged. **Last-writer-wins survives, but narrowed**: an ordinary
   > editor Save still carries no precondition and still wins
   > (`svcimpl/file_service.go:324-345` enforces `If-Match`/`If-None-Match`
   > only when the client sends them); create, duplicate, delete, move,
   > replace and restore now require one. Authority:
   > [workspace-file-browser-security.md](workspace-file-browser-security.md).

   v2 invariants stand: structural validation only
   (no policy guards, no denylist), `.git` writable, last-writer-wins, no
   etags, no LSP, no file watcher.

### 1.3 Frontend information architecture (Phase B)

`FilesPage` drops the scope `<select>` and no longer remounts anything. One
`WorkspaceFileBrowser` instance, one tree with three semantic root groups:

```
AGENTS
  ⬤ local-coder · source-repo     [3 changes]   ← single-repo: FLATTENED,
      README.md ●                                  files directly under agent,
      Makefile                                     repo as dim secondary text
  ⬤ local-planner                 [2 repos · 1 change]
      ▸ source-repo ●                            ← cross-repo: one child per
      ▸ docs-repo                                  checkout (agent × repo)
REPOS
  ▸ source-repo ●                                ← shared checkouts
  ▸ docs-repo
WORKSPACE FILES                                  ← collapsed + dimmed by
  ▸ .loom   ▸ sessions   daemon.lock  …            default; system noise
```

- Roots derive from workspace context (agents/repos already in
  `useWorkspaceContext`) + the checkouts endpoint for badges/existence.
  Checkouts with `exists:false` render disabled with a "not on this machine"
  tooltip.
- Each root node lazy-loads via the existing scoped list endpoint with its
  own (scope, target, repo) triple. Expansion state, selection, and tabs are
  keyed by the full checkout ref — switching "scope" is now just scrolling.
- Agent badges roll up change counts across the agent's checkouts; repo
  children show the per-checkout split.
- Tree typography: app sans (`--font-main`), NOT mono. Folder/file icons and
  row styling follow the WorkspaceTree sidebar. Git-status coloring stays but
  calmer: dot + name tint for modified/added only (no rainbow of language
  colors in the tree).
- Sidebar top: segmented lens toggle `[ Files | Changes (n) ]` (Loom pill
  style) + a single "Go to file… ⌘P" affordance (button styled as input that
  opens Quick Open). The v2 "Jump to folder"/"Filter files" inputs are
  deleted.
- **Deleted sections**: Open Editors, Source Control, Timeline. The sidebar
  is the tree (or the Changes list), full height, nothing else.
- **Move to…**: context-menu action opening a folder picker (same tree data,
  folders only) targeting the same checkout. Replaces drag-to-move; reuses
  v2's move endpoint + tab-retarget logic. dnd-kit imports removed from
  FileTree.

### 1.4 Tab/store schema (Phase B)

Tab store becomes workspace-keyed with checkout-refs per tab:
`{ v: 3, groups: [{ tabs: [{ ref: {scope, target, repo?}, path }], active }],
mru: [...] }`. Split groups keep v2 semantics (drag between groups may stay in
the TAB BAR only — the tree has no drag).

> NOT SHIPPED: the planned v2 migration (fold each `{v:2, ...}` scope store,
> keyed per (ws,scope,target), into the unified store tagging tabs with the
> scope's ref, dropping entries whose checkout no longer exists) was never
> implemented. Every key the store reads is a `file-browser-tabs:v3` key —
> the workspace-mode default
> (`internal/webui/frontend/src/stores/fileBrowserStore.tsx:27`) or a per-agent
> `…:v3:agent:<name>` variant passed in as the `storageKey` prop (`:81-82`) —
> and
> `parsePersistedV3` returns null unless `parsed?.v === 3` (`:261-279`), so a
> v2 payload is silently discarded and the store falls back to empty state
> (`loadFileBrowserTabs`, `:281-290`). The only v2-key helper,
> `legacyScopeStorageKey`
> (`internal/webui/frontend/src/utils/fileExplorerRefs.ts:62`), has no callers.

## 2. Feature designs

### 2.1 Changes lens (Phase C)

- Sidebar lens toggle; count badge = sum of `change_count` over checkouts,
  live-refreshed on the existing git-status cadence (refresh on focus +
  after writes; no watcher).
- List groups by checkout, ordered: agent checkouts (by agent), then shared
  repo checkouts. Group header: `local-planner · source-repo · 3` /
  `source-repo · shared checkout · 1`.
- Rows: filename + friendly status chip — `Modified` (amber), `New` (green),
  `Deleted` (red), `Renamed` (blue). Porcelain XY codes never rendered.
- Click row → unified diff (v2 diff viewer) in the active editor group;
  "Open file" jumps to the editor. Diff title carries the checkout label.
- Absorbs v2's ScmPanel; delete it.

### 2.2 History at the file (Phase D)

- Editor header gains a `History` toggle button (clock icon + label). Opens a
  right-side panel inside the editor area (per group), width ~264px.
- Panel header names its subject: `History · README.md`.
- Entries: real timeline rail. Commits = accent dot, message, author + time,
  actions `View diff` / `Open at commit`. Data: existing history endpoint,
  commit entries only.

  > SUPERSEDED: the save half of this design was cut before ship. Browser save
  > snapshots were removed in `ccf477157`, the same commit that shipped v3, so
  > there is no gray save dot, no save cluster and no per-save `Restore`. The
  > endpoint emits `Kind: "commit"` and nothing else
  > (`internal/webui/svcimpl/file_history.go:236`; `kind` is a single-value
  > enum at `api/openapi.yaml:6756-6758`), `cleanupLegacySaveHistory`
  > (`file_history.go:382-390`) deletes the legacy snapshot root on startup,
  > and the shipped panel renders one `CommitItem` kind
  > (`internal/webui/frontend/src/components/FileExplorer/FileHistoryPanel.tsx:45,62`).
  > Authority:
  > [workspace-file-browser-security.md](workspace-file-browser-security.md).
- v2 TimelineSection in the sidebar is deleted. Git gutter (buffer-vs-HEAD)
  and blame toggle carry forward unchanged.

### 2.3 Kept from v2 (regression surface, do not redesign)

Editing + Cmd+S (save-history capture did NOT carry forward — removed before
ship, see §2.2); split editor groups + Open Editors
REMOVED but tab groups kept; Quick Open (Cmd+P, now workspace-wide by
default, results labeled with checkout); global search Cmd+Shift+F as an
overlay panel (no longer replaces the sidebar) with include/exclude +
replace-with-preview; find-in-file + go-to-line; symbol crumbs + Cmd+Shift+O;
CRUD context menu (plus new Move to…); read-only/binary handling.

### 2.4 Agents page convergence (Phase E)

- `AgentsPage` "files" tab renders the same explorer component **agent-rooted**:
  tree roots = that agent's checkouts only (flattened when single-repo), same
  lens toggle scoped to the agent, same editor shell. Props:
  `{ mode: "agent", agentName }` vs `{ mode: "workspace" }`.
- `components/FileEditorPanel/` is deleted; its tests migrate to the new
  embedding.
- The Agents page `git`/`diff` tabs remain this phase (their absorption into
  the Changes lens is a follow-up decision once the lens proves itself).

## 3. Visual language (applies across B–E)

- Surfaces: sidebar on `--color-chrome`, editor on `--bg-card`-style panel,
  hairline `--line-color`. Follow the Kanban board's card/pill idiom.
- Section headers: small-caps like WorkspaceTree's AGENTS/REPOS — identical
  spacing/weight, not VS Code's cramped stack.
- Controls: segmented pills (lens), labeled buttons (`Save`, `History`) —
  no bare native `<select>`, no unlabeled icon-only toolbars where a label
  fits.
- Agent avatars in the tree reuse the app's avatar component/colors.
- Mono strictly for file contents, diffs, and path fragments in headers.
- Both themes via existing tokens; no hardcoded colors beyond git-status
  dots already tokenized.

Reference mockup (approved): an interactive v3 direction artifact, not archived
in-repo and no longer resolvable. Match the layout of the shipped explorer
(`internal/webui/frontend/src/components/FileExplorer/`), not a mockup.

## 4. Phasing (historical — all phases shipped in `ccf477157`)

| Phase | Scope | Effort | Verify |
|-------|-------|--------|--------|
| A | Backend checkout model: repo qualifier + checkouts endpoint + codegen | M | gate + curl matrix (incl. multi-repo agent w/ 2nd repo added to LOCALMODE via API; back-compat: no-repo param unchanged; disallowed repo 400; missing checkout 404 + exists:false) |
| B | Unified tree IA + store v3 + Move to… + visual pass on tree/sidebar | L | gate + npm build + Opus agent-browser, REAL CDP clicks: tree roots/badges, flatten vs cross-repo children, open/edit/save in agent checkout, Move to…, no scope select anywhere, Cmd+P labeled by checkout. (Tab migration from v2 localStorage was planned here but never implemented — see §1.4.) |
| C | Changes lens + ScmPanel deletion | M | gate + build + Opus: lens toggle + live count, checkout groups, chips (no porcelain), row→diff→open file; edit a file live and watch count move |
| D | History panel + TimelineSection deletion | M | gate + build + Opus: toggle, subject named, commit entries (the save cluster was cut before ship — see §2.2), gutter+blame regression |
| E | Agents page embedding + FileEditorPanel retirement | M | gate + build + Opus on Agents page: files tab = new explorer agent-rooted, single-repo flatten, lens works, terminal/git/diff tabs unharmed |

Historical branch/PR plan (no longer actionable): sequential; commit per phase on
`feat/file-explorer-v3` (cut from `feat/file-browser-v2`); push at the end;
PR → `feat/file-browser-v2`.

## 5. Accepted risks (by decision)

- Workspace-root tree exposes system files (dimmed, collapsed) — structural
  access rules unchanged from v2; demotion is presentation only.
- Change counts are poll-based (focus/write-triggered), may lag external git
  activity; no watcher by design.
- Tab-store migration is best-effort; a dropped tab is acceptable, a crash is
  not. (Moot as shipped — no v2→v3 migration exists, so every v2 tab is
  dropped; see §1.4.)
- Cross-repo verification requires a second repo in the local-mode workspace
  (added legitimately via API/UI — never hand-created fake state).

## 6. Explicitly cut / removed in v3

Scope `<select>` + per-scope remount; Open Editors section; Timeline sidebar
section; ScmPanel sidebar section; drag-to-move (dnd-kit out of the tree);
"Jump to folder" + "Filter files" inputs; `FileEditorPanel` component; mono
tree typography; porcelain codes in any UI surface.

All of these are confirmed removed: `FileEditorPanel`, `ScmPanel`,
`TimelineSection` and `OpenEditors` return zero hits under
`internal/webui/frontend/src`, and no `dnd-kit` import remains in
`components/FileExplorer/`.

## Related

- [workspace-file-browser-security.md](workspace-file-browser-security.md) —
  the shipped security/concurrency contract (authoritative; overrides §1.2).
- [docs/arch/file-explorer.md](../arch/file-explorer.md) — component and
  data-flow map of the shipped explorer.
- [2026-07-02-file-browser-v2-scoped-explorer.md](2026-07-02-file-browser-v2-scoped-explorer.md)
  — the superseded predecessor.
