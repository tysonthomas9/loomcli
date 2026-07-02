# Goal prompt: implement Terminal Worktree Groups

> Paste-ready goal for an implementing agent (lead, Claude, or codex session).
> Companion to `docs/design/terminal-worktree-groups.md`.

---

## Mission

Implement the **terminal worktree groups** feature end-to-end per the reviewed
design doc `docs/design/terminal-worktree-groups.md` (status: adversarially
reviewed 2026-07-01, findings folded in). **The doc is the single source of
truth — read it fully before writing any code.** This prompt orients you and
lists the non-negotiables; the doc carries the details (data model, API
contracts, per-section specs, edge-case table, test matrix).

## Outcome (definition of done)

A user in the web UI can:

1. **See the TERMINALS sidebar grouped**: a non-deletable default group
   (label = workspace name, cwd = workspace root) holding all existing and
   un-grouped tabs, plus user-created worktree groups with a per-member
   "repo ← base" summary line.
2. **Create a worktree group** via a modal: name (= branch), repo checkboxes
   (default: all local repos), optional base branch — producing one git
   worktree per selected repo under
   `{ws}/.loom/terminal-worktrees/{name}/{repo}`, with per-repo results
   (`created / exists / reused / conflict / error / rolled_back`) shown in the
   modal. **Create is all-or-nothing**: re-using an existing group name returns
   409 before anything touches disk, and any per-repo failure rolls back
   everything the request created (worktrees + freshly created branches) and
   persists nothing.
3. **Open a terminal in a group** and get a shell whose cwd is the **group
   root**, with every selected repo's feature-branch checkout visible as a
   subdirectory. Verified end-to-end: `pwd` in a fresh group terminal prints
   the group root — **including the very first terminal created in a new
   group** (review finding C1 was a create/attach race here; regressing it
   means the implementation is wrong, not just untested).
4. **Existing behavior unchanged**: default-group tabs still spawn at the
   workspace root with `Launch = nil`; agent tabs and their launch-spec
   precedence are untouched.

## Non-negotiables (locked decisions — do not re-litigate)

All of the doc's D1–D13 are binding. The six most commonly gotten wrong:

- **D3 (no duplicates, no merge):** POST create with an existing group name is
  a **409** — there are no ensure/merge semantics, and create is
  **all-or-nothing** (roll back this request's worktrees and fresh branches on
  any per-repo failure; keep reused branches and adopted leftovers; persist
  nothing on failure).
- **D5 (base semantics):** when `base` is omitted, call
  `EnsureGitWorktreeFromBranch` with `defaultBranch=""` (fork **local HEAD**,
  no fetch); the `rev-parse` result is used **only to record** `base_branch`.
  Passing the resolved branch name as `defaultBranch` makes the helper fetch
  and prefer `origin/<branch>`, silently dropping unpushed local commits.
- **D11 (ordering):** the frontend **awaits the tab-metadata PUT** before
  mounting the tab / opening the WebSocket for non-default-group tabs.
  Fire-and-forget remains only for the default group.
- **D12 (Cwd-only launch):** widen `launchSpecForTerminalSession`'s persisted-
  Launch acceptance (`internal/webui/handlers/terminal/ws.go:396`) to also
  accept a non-empty `Cwd` with empty Argv/Env. Do **not** touch the agent-tab
  precedence at `:387-394`.
- **D13 (recomputed cwd):** `Launch.Cwd` is recomputed at PUT time via
  `TerminalGroupRootPath(ws.Path, group.Name)`; never use the stored `Root`
  for cwd.
- **D1 layering:** webui packages must not import `internal/cli` — move
  `IsGitLinkedWorktree` into `internal/localworkspace` along with the
  validator.

## Order of work

Follow the doc's tasks 1–9, backend first: **1 → 2 → 3 → 4 → 5** (shared
helpers, store, service+API, tab metadata + ws.go, PTY fallback), then frontend
**6 → 7 → 8** (API+bridge, TerminalSection+modal, TerminalView wiring). Task 9
is the test sweep, **not** the first time tests are written — each task lands
with its own tests and green gates. One commit per task.

## Known traps (verified during design review — believe them)

- `createTab`'s PUT body (`useTerminalMetadata.ts:126-132`) is a hand-picked
  field object and `putTabMetadata` casts to `any` — add `worktree_group_id`
  **explicitly** to the body object or it is silently dropped with no type
  error.
- `EnsureGitWorktreeFromBranch` returns nil for both fresh-create and
  idempotent skip — pre-stat `target/.git`; an existing worktree is **adopted
  only when its checked-out branch equals the group name**, else per-repo
  error.
- Its `branchAlreadyExists` matcher also matches path-collision
  `"already exists"` stderr — **never feed an occupied non-`.git` target dir
  to it** (pre-check and remediate per doc B3) or every retry wedges forever
  with a misleading error.
- `runGit` is context-free — use the new ctx-aware variant with a per-repo
  timeout, or a black-holing remote hangs the HTTP handler.
- `TerminalSection` must stay a **pure bridge-state renderer** (no group
  fetching) — `TerminalView` owns group data. Two fetchers recreate review
  finding M4: a freshly created group gets deselected by validation against a
  stale list. Validate `activeGroupId` **only after the groups fetch settles**.
- Frontend `test:coverage` has a known vitest-4 heap-OOM triggered by
  importing `AgentSection.tsx` in tests — keep the AgentSection **stub**
  pattern already used in `App.test` and the WorkspaceTree tests; don't import
  `AgentSection.tsx` into new test files.
- `TerminalSection.test.tsx` asserts the **old** bridge shape — update those
  assertions, don't just add new tests.

## Verification gates

- **Read `AGENTS.md` first** for repo runbooks and the required gates; run the
  Go and frontend gates it prescribes before every commit.
- **Backend:** `go test` over every touched package (`localworkspace`,
  `worktreegroups`, `handlers/terminal`, `terminal`) including the new tests
  from the doc's test table.
- **Frontend:** vitest suites for the bridge, `TerminalSection`,
  `TerminalView`.
- **End-to-end (required before declaring done)** — use the loom-pr-test skill
  (`.agent-skills/loom-pr-test/SKILL.md`) or run the app and verify by hand:
  1. Create a multi-repo group → open a terminal → `pwd` = group root; `ls`
     shows each selected repo; `git -C <repo> branch --show-current` = group
     name.
  2. The **first** tab of a brand-new group lands at the group root (C1
     regression check).
  3. Duplicate a group tab → still lands at the group root (D12 check).
  4. Delete a group's root dir out-of-band → reopening the terminal falls back
     to the workspace root with no dead socket (D10 check).
  5. Create with a base that has unpushed local commits → the worktree
     contains them (D5 check).
  6. Create a group with an existing name → 409 surfaced in the modal, nothing
     changed on disk. Then force a per-repo failure (e.g. the branch checked
     out elsewhere in one repo) → **no** worktrees or fresh branches remain
     from the failed request and no group is persisted (D3 rollback check).
- **OpenAPI:** `api/openapi.yaml` updated; regenerate whatever the repo
  derives from it.

## Conventions

- Branch off `main`. Do **not** add AI attribution (Co-Authored-By etc.) to
  commit messages.
- Match surrounding code style; comments only for constraints the code can't
  express.
- If reality contradicts the design doc, **stop and surface it** — do not
  silently improvise around a locked decision.

## Out of scope — do not build

Drag terminals between groups; delete/rename groups or members; adding repos to
an existing group (no ensure/merge semantics); PATCH group reassignment;
branch-list dropdown for base; group names containing `/`;
per-repo "open terminal inside {repo}" shortcut; auto-`cd` for single-member
groups; multi-process store CAS.
