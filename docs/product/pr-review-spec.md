# Pull Request Review Spec

> **Status:** Partially implemented · *audited 2026-07-23*. §1 (queue) and §4
> (task/epic chrome) largely landed. **§2 (route), §3 (agent identity), §5
> (GitHub transport), and §6 (API table) were built differently** and are
> corrected below. The §5 reversal matters most: the shipped primary path
> stores a GitHub PAT, which the original decision explicitly ruled out.

**Last updated:** 2026-07-23
**Related:** [`agent-run-ux-spec.md`](agent-run-ux-spec.md),
[`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md),
[`../design/aether-wireframe-mapping.md`](../design/aether-wireframe-mapping.md)

## Purpose

Make pull requests a first-class object in the loom web UI: a loom-first
review queue, a full-screen GitHub-style PR page with inline/split diff and
review decisions, and an on-demand PR review agent that runs as a real,
persisted agent session. The feature must keep working when GitHub access is
degraded or absent.

The UI should answer three questions quickly:

- What needs review right now (tasks and PRs)?
- What does this PR change, and is it safe to merge?
- Who (human or agent) is reviewing it, and what did they decide?

## Design Source

The visual/interaction reference is the Aether wireframe handoff bundle
(Claude Design):

`https://api.anthropic.com/v1/design/h/sEdje5wpePG0ouylfOtMPw?open_file=Aether+Wireframe.html`

The bundle is a gzip archive containing `Aether Wireframe.html` (components
`PRsView`, `PRDetailPanel`, `ReviewWorkspace`) and three chat transcripts
that carry intent. This spec adopts the wireframe with three deliberate
deviations, agreed 2026-06-11:

1. **Loom-first rows.** The wireframe lists only tickets that carry a PR.
   We additionally list review-stage tasks with no PR (plan reviews) and
   GitHub PRs with no linked task ("unlinked"), and we degrade to a warning
   banner instead of an error page when gh fails. The wireframe predates the
   gh-backed implementation and does not model these cases.
2. **Full-screen PR page instead of the slide-over peek.** The wireframe's
   `PRDetailPanel` slide-over is dropped. Clicking a row opens a dedicated,
   deep-linkable PR page modeled on GitHub's PR review page, with an
   inline/split diff toggle.
3. **Task-less PRs are first-class.** The wireframe's unlinked behavior
   (open externally) is replaced by the same PR page and review-agent flow,
   minus the task integration.

Ideas explicitly rejected during the design iteration (do not reintroduce):
chat bubbles for the review agent (reuse the lead-agent terminal), a PR
context rail beside the workspace, stacking the task panel over the review
(split-screen beside it instead), a "Plan" option in the add-terminal menu.

## Primary Surfaces

| Surface | Purpose | Status |
|---|---|---|
| PRs page (`/ws/:ws/prs`) | Loom-first review queue across all workspace repos. | Shipped (`internal/webui/frontend/src/router.tsx:131`) |
| PR page (`/ws/:ws/prs/:repo/:number`) | Full-screen GitHub-style review: diff, checks, decisions, agent CTA. | **Not shipped as a route** — see §2 |
| Review agent workspace (Agents view) | Persisted PR review agent with terminal + standard tabs. | Shipped, different identity — see §3 |
| Task card / issue detail panel | PR pill and PR section on linked tasks. | Shipped |
| Epic header / detail | "N PRs" rollup chip. | Shipped |

## 1. PRs Page (review queue)

### Row sources (current behavior, retained)

1. **Loom issues** with `status == "review"` or a PR `external_ref` —
   primary source; renders with no GitHub access at all.
2. **GitHub PRs** from `gh pr list` across workspace repos, joined to
   issues by the stable `(owner, repo, number)` key (`prKeyFromRef`),
   robust to URL variants (`www.`, `.git`, `http`, sub-paths, trailing
   slash). PRs with no matching issue render as unlinked rows.

GitHub failures are per-repo warnings, never page failures: the backend
skips failing repos and returns `warnings[]`; the UI shows a banner
("GitHub metadata unavailable/incomplete: …") above a fully working queue.
A missing gh CLI is a warning with empty results, not a 503.

### Row contents (additions in bold)

State icon · `#number` pill · title · state tag · repo chip ·
**head branch (mono)** · **`+adds −dels` diff stats** · **CI checks badge**
· epic chip · ticket chip (or "Unlinked") · assignee avatar · chevron.
Chips duplicating the active grouping drop from rows (wireframe behavior).
Loom-only rows (no PR) show "Plan review"/"Review" state and omit
PR-specific fields.

### Sort, filters, grouping

- **Sort:** urgency — `Changes requested → Open (needs review) → Draft →
  Merged → Closed`; loom-only review rows sort with "Open". Ties break by
  most recent update. (Replaces the current updated-at sort.)
- **Filter pills:** All · Needs review · Changes requested · Draft ·
  Merged, with live counts; zero-count pills hidden. ("Changes requested"
  and "Draft" are new; loom-only rows count under "Needs review".)
- **Grouping:** None · Repo (header: repo chip + base branch + count) ·
  Epic (header: EPIC tag + title + count, with a "No epic" bucket).

### Data

> **Not built.** `ops.GitPullRequest` has no `checks` field
> (`internal/ops/gitops.go:99-116`) and `statusCheckRollup` appears nowhere in
> `internal/cli/git/pr_list.go`. The only `statusCheckRollup` in the repo is
> the unrelated stack publisher's GraphQL query
> (`internal/stackpublish/forge_github.go:188`).

`GitPullRequest` gains `checks` (`passed | failed | running | none`),
derived from `statusCheckRollup` added to the `gh pr list --json` field
list. Additions/deletions/changed-files **are** already fetched
(`internal/ops/gitops.go:113-115`).

## 2. PR Page (full-screen, GitHub-style)

### Route and entry

> **Superseded.** No such route exists.
> `internal/webui/frontend/src/router.tsx:131` registers only `prs` →
> `PRsPage`, and `PRReviewWorkspace` is rendered as a **child component** of
> `PRsPage` (`internal/webui/frontend/src/views/PRsPage.tsx:21,362,379`) —
> that is, the entry this section proposed to replace is what shipped. There
> is no deep link to an individual PR.

`/ws/:ws/prs/:repo/:number` — deep-linkable, back button returns to the
queue. Replaces both the wireframe slide-over and the current
PR-backed `PRReviewWorkspace` entry. Loom-only rows (plan reviews, no PR)
keep routing to the existing task-based review view.

### Layout

**Header:** title + `#number` · state pill · `base ← head` ·
`+adds −dels` · CI checks badge · "View on host ↗" · linked ticket and
epic chips (clicking opens the existing issue/epic detail panel beside the
diff). For task-less PRs: a **"Create task from PR"** action instead of
chips — creates a loom issue with `external_ref` set, which links it into
the queue via the existing join.

**Action bar (right):**

- **Start PR review agent** (or "Open review agent · Live" once spawned).
- **Approve & merge** — disabled while checks are failing, with the reason
  ("Checks failing — fix before merge"). CI gating is enforced server-side
  as well; the disabled button is not the security boundary.
- **Request changes** — requires a comment (explicit text field; the agent
  chat transcript is advisory and never auto-submitted).

Decisions live on the PR page (mirrored in the review-agent workspace) so a
trivial PR can be approved without spawning an agent.

**Diff viewer:**

- File list panel (per-file `+/−`, A/M/D badges), sticky file headers.
- **Inline (unified) / split (side-by-side) toggle**, built on
  `@codemirror/merge` (CodeMirror is already lazy-loaded for DiffTab /
  PRFilesTab). Preference persists per user via workspace-scoped storage.
- Large files collapsed by default, rendered on expand via the existing
  per-file `DiffFilePatch` backend (binary/too-large flags already exist).

### Diff data without a worktree

The PR diff is computed locally: fetch `refs/pull/N/head` into the existing
clone (plain git; works for fork PRs) and run the existing
`DiffFiles`/`DiffFilePatch` machinery over `base...head`. No worktree and
no gh needed to *view* a PR; gh is needed only for metadata refresh and the
decision actions. This generalizes to GitLab later
(`refs/merge-requests/N/head`).

## 3. PR Review Agent

### Identity and lifecycle

**As shipped** (`internal/webui/handlers/prreview/reviewer.go`):

- Keyed by `(owner, repo, number)`, as proposed — but the agent name is
  collision-resistant, not the plain shape the draft implied:
  `review-<owner>-<repo>-<sha256(owner/repo)[:8]>-pr-<N>`, capped at 100
  chars (`reviewer.go:29-31,44-55`). Two earlier name shapes are still
  migrated on encounter: `legacyReviewerAgentName` (`reviewer.go:76`) and
  `intermediateReviewerAgentName` (`reviewer.go:64`), retired by
  `ensureReviewerAgentAndRetireLegacy` (`reviewer.go:364`).
- **Backing is not a session kind.** There is no `AgentSessionKind` `review`
  — the kinds are `task|orchestration|terminal|maintenance|ad_hoc`
  (`internal/domain/control_plane.go:56-64`). The reviewer is a **persisted
  `domain.Agent`** on role `pr-reviewer` (`reviewer.go:32,344`), whose role
  is reconciled to `kind=interactive` with prompt
  `builtin:pr-review-checkout` (`reviewer.go:33,416-445`).
- **The checkout is detached**, not a named `review/pr-N` branch:
  `localworkspace.EnsureDetachedGitWorktreeAtPRHead`
  (`internal/webui/handlers/prreview/module.go:76`).
- **Persisted** in the agent store like task/lead agents: survives reload,
  visible to the CLI, appears in the AGENTS sidebar roster.
- **Teardown:** `retireLegacyReviewer` (`reviewer.go:383`) /
  `retireReviewerAgent` (`reviewer.go:400`) stop the runtime and delete the
  agent. **The
  time-based reaper for orphaned review worktrees is UNVERIFIED** — no such
  reaper was found under `internal/webui/handlers/prreview`. Treat the
  "closed for N days" cleanup as unbuilt until located.

### Workspace UI

Reuses the lead-agent shell in the Agents view with the standard
editor-group tabs (split/drag supported):

| Tab | Content |
|---|---|
| Agent | Terminal-style session; suggestion chips ("What does this PR do?", "Summarize changes", "Any risks?"); decision bar mirrored at top. |
| Info | PR stats (additions/deletions/files/checks), summary, base/head/repo, "Resolves" links (ticket + epic) or "No linked task → Create task from PR". |
| Git | Commits on the PR branch. |
| Diff | Same file list + unified diff as the PR page. |
| Files | Worktree file browser. |
| `+` | Add Claude / Codex terminals (no Plan option). |

The agent launches on the workspace's default AI backend, primed with PR
context (`loom pr review N` semantics: branch checked out, diff read).

### Decision semantics

> **Partially built.** `POST …/review` posts a GitHub review with event
> `APPROVE` or `REQUEST_CHANGES`
> (`internal/webui/handlers/prreview/handlers.go:114-124,268-273`). There is
> no merge call, no server-side CI gate, and no task-close/teardown chain in
> that handler; `merged` is only read back from the response
> (`handlers.go:229`).

- **Approve & merge:** `gh pr review --approve` → `gh pr merge` (repo's
  default merge method) → close the linked loom task (skip if none) → tear
  down the review agent. Server-side CI gate.
- **Request changes:** `gh pr review --request-changes --body <comment>` →
  if a linked task exists: move it back to `in_progress` and `startAgent`
  on the original assignee with the feedback as context (reuses the
  assign-to-start flow). If no task: post the review only, and offer
  **"Create fix task"** (loom issue carrying the feedback, assignable to an
  agent).
- Both decisions record who decided and when on the loom side (task comment
  or event), so the queue reflects review history without re-fetching
  GitHub.

## 4. Task and Epic Integration

- **Kanban/list cards:** small PR pill colored by state
  (`draft` grey · `open` green · `changes` amber · `merged` violet ·
  `closed` red); no pill when no PR.
- **Issue detail panel:** PR section between properties and description —
  state pill, branch `base ← head`, `+/−`, checks badge, "View PR" (opens
  the PR page). Empty state: dashed "No pull request yet · Open draft PR"
  (wires to the existing CreatePRAction flow).
- **Epic headers and detail:** "N PRs" rollup chip counting PRs across the
  epic's tickets; each ticket row shows its PR pill.

## 5. Architecture Decisions

| Decision | Choice | Rationale |
|---|---|---|
| GitHub transport (**shipped, reversed**) | **Connector-backed with a stored GitHub PAT** | The `prreview` module is connector-primary: the GitHub PAT from local runtime settings, or the `LOOM_WEBUI_GITHUB_TOKEN` override, supplies the outer authority bound while connector grants provide defense in depth (`internal/webui/handlers/prreview/module.go:27,29-35`). The credential is sealed in the connector vault and re-resolved via `InvalidateCredentialSeeds` (`module.go:85-94`); the token is read from `LOOM_WEBUI_GITHUB_TOKEN` at `internal/webui/handlers/prreview/seed.go:128,150`. Read grants are seeded on read paths, write grants only on explicit review posts. |
| GitHub transport (**fallback**) | gh CLI behind the `ops.GitOps` seam | Survives only for store-less `loom serve` (`internal/webui/handlers/git/pull_requests_module.go:9-12`). Auth delegated to `gh auth login`. |
| ~~GitHub transport (original)~~ | ~~gh CLI only; no token storage in loomcli~~ | **Superseded 2026-07-23 audit.** The original rationale — SSO/2FA/enterprise auth delegated to `gh`, zero credential surface in loom — no longer describes the primary path. A contributor must not assume loom holds no GitHub credential; it does. |
| Git operations | **Plain git / go-git** for fetch, worktrees, diffs | PR refs fetch over the git protocol (forks included); diff viewing works with zero GitHub API access; generalizes to GitLab refs. |
| PR identity | `(owner, repo, number)` everywhere | Survives URL variants; makes tasks optional; already used for the queue join. |
| Failure posture | Partial, never fatal | Per-repo warnings; read paths (queue, diff) keep working without gh; only metadata refresh and decisions require it. |
| Decision authority | Human-only | Agents inform; only the user clicks Approve/Request changes. CI gate enforced server-side. |

## 6. API Surface

**As registered** (`internal/webui/handlers/prreview/module.go:102-109`). Note
the `{owner}` path segment the original table omitted.

| Endpoint | Purpose |
|---|---|
| `GET /api/workspaces/{ws}/pull-requests` | Queue listing. Returns `warnings[]`; no `checks` field. |
| `GET …/pull-requests/{owner}/{repo}/{number}` | PR detail. |
| `GET …/pull-requests/{owner}/{repo}/{number}/diff` | PR diff. |
| `POST …/pull-requests/{owner}/{repo}/{number}/review` | Post a GitHub review. Approve, request-changes and comment are the `event` field, mapped to `APPROVE` / `REQUEST_CHANGES` / `COMMENT` (`internal/webui/handlers/prreview/handlers.go:268-277`) — not separate endpoints. |
| `POST …/pull-requests/{owner}/{repo}/{number}/reviewer` | Ensure (spawn-or-reopen) the persisted reviewer agent. |
| `POST …/pull-requests/{owner}/{repo}/{number}/messages` | Send a message to the reviewer agent. |
| `GET …/pull-requests/{owner}/{repo}/{number}/stream` | SSE stream of reviewer activity. |
| `GET …/pull-requests/{owner}/{repo}/{number}/conversation` | Reviewer conversation history. |

Not built: `/files` and `/files/{path}`, `/review-agent`, `/approve`,
`/request-changes`, and any merge endpoint. Merge state is only **read back**
(`handlers.go:229`).

## 7. Build Order

1. **Queue polish** (small): `statusCheckRollup` → `checks`; row branch +
   diff stats + checks badge; urgency sort; Changes-requested/Draft filter
   pills. All inside `PRsPage`/`pr_list.go`.
   **NOT DONE** — no `statusCheckRollup` in `internal/cli/git/pr_list.go`, no
   `checks` field on `ops.GitPullRequest` (`internal/ops/gitops.go:99-116`).
2. **PR page** (medium): route + header + decisions UI; PR-ref fetch + local
   diff endpoints; inline/split CodeMirror viewer; "Create task from PR".
   **NOT DONE as specified** — no dedicated route
   (`internal/webui/frontend/src/router.tsx:131`); a `/diff` endpoint exists
   (`internal/webui/handlers/prreview/module.go:104`) but the `/files`
   endpoints do not.
3. **Decision actions** (medium): approve/request-changes endpoints with
   server-side CI gate, task close/restart wiring, loom-side event record.
   **PARTIAL** — one `POST …/review` endpoint carrying an `event` field; no
   CI gate, no merge, no task-close chain.
4. **Review agent** (large): worktree ensure/teardown, roster/sidebar
   integration, workspace tabs, PR-context priming, one-per-PR semantics.
   **LANDED, differently** — see §3. Built as a persisted `pr-reviewer` agent,
   not a `kind=review` session; the orphan-worktree reaper is unverified.
5. **Task/epic chrome** (small): card PR pills, issue-panel PR section
   alignment, epic rollup chips. **LANDED.**
6. **Dev-server preview** (future, out of scope here): `loom pr preview N`
   — isolated dev server from the review worktree with browser preview.

Each step ships independently; 1–3 deliver a complete human review loop
before any agent work lands. *(In practice step 4 landed ahead of 1–3.)*

## 8. Risks and Open Questions

- **Worktree accumulation:** review worktrees must be reaped (PR closed >
  N days). Reuse the terminal-session liveness pattern.
- **gh rate limits / latency:** queue polling is 30s with per-repo
  concurrency capped at 4; PR-page metadata refresh is on-demand. Checks
  add JSON weight to `gh pr list` — verify acceptable latency on large
  repos.
- **Merge conflicts on merge:** `gh pr merge` can fail (out of date,
  conflicts); surface the host error verbatim with a "View on host" escape
  hatch rather than attempting auto-resolution in v1.
- **Non-GitHub hosts:** out of scope for v1; the git-side design (PR refs,
  local diff) and the GitOps seam are chosen so GitLab support is additive.
- **Review feedback fidelity:** request-changes sends the comment to both
  GitHub and the author agent context; line-level review comments are out
  of scope for v1 (whole-PR comment only).

## Related

- [`README.md`](README.md) — index for this folder
- [`agent-run-ux-spec.md`](agent-run-ux-spec.md) — the surrounding agent UI
- [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) — session kinds and agent states
- [`../design/aether-wireframe-mapping.md`](../design/aether-wireframe-mapping.md) — the wireframe this spec adapts
