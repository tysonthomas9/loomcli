# E2E manual test plan — Web UI (multi-workspace)

Tests the loom webui as the user actually experiences it: opening a browser, creating workspaces, switching between them, and verifying state isolation.

**Status:** validates the FleetDB-backed web UI path.

**Prerequisites:** complete `e2e-preflight.md` setup. The fleet-db should already have at least one workspace from the CLI test plan (`e2e-cli.md` → ACME), so the UI can render something.

## Tooling

Tests below use **agent-browser** (Playwright Chromium on CDP 9222). Setup per the agent-browser memory note: `~/.claude/projects/-home-admin-codebase-2-loomcli/memory/feedback_agent_browser.md`. Falls back to manual chrome if agent-browser is wedged.

```bash
# Start loom serve in cloud mode (so the embedded fleet-db isn't an extra moving piece)
LOOM_FLEET_DB_URL=http://127.0.0.1:18095 \
LOOM_FLEET_DB_ACTOR=tester \
/tmp/loom-test serve --port 18091 \
  > /tmp/loom-serve.log 2>&1 &
echo $! > /tmp/loom-serve.pid
disown
until curl -sf http://127.0.0.1:18091/api/health; do sleep 0.2; done
```

## Phase E — UI lifecycle + multi-workspace

Each test = a navigation + assertion. Where possible, cross-check via curl against fleet-db (the UI's writes must be visible there).

| ID | Action | Expected |
|---|---|---|
| E1 | Browse to `http://localhost:18091` | webui loads; no console errors in DevTools |
| E2 | Inspect workspace selector / switcher widget | renders existing workspaces from fleet-db (e.g., shows ACME after `e2e-cli.md` ran) |
| **E3** | (precondition for clean E6+) DELETE all workspaces via curl: `for k in $(curl -s :18095/api/v1/admin/workspaces \| jq -r '.workspaces[].key'); do curl -X DELETE ":18095/api/v1/admin/workspaces/$k?force=true"; done` | fleet-db is empty; UI selector shows "no workspaces" or equivalent state |

### Multi-workspace creation + isolation

| ID | Action | Expected |
|---|---|---|
| E4 | UI: click "Create workspace" → name=ALPHA → submit | POST `/api/v1/admin/workspaces` → `key:"ALPHA"`; UI updates without page reload |
| E5 | UI: create BRAVO | `:18095/api/v1/admin/workspaces` returns `[ALPHA, BRAVO]` |
| E6 | UI: create CHARLIE | three workspaces in selector; alphabetical or creation-order (verify ordering matches API response) |
| E7 | curl `:18095/api/v1/admin/workspaces` | three workspaces present (no UI-only ghost entries) |
| E8 | UI: switch to ALPHA → "Add repo" → name=alpha-repo, url=git@x:y/a.git | repo appears in ALPHA's panel; `:18095/api/v1/ALPHA/repos` shows `alpha-repo` |
| E9 | UI: switch to BRAVO → "Add repo" → name=bravo-repo | only `bravo-repo` in BRAVO; alpha-repo not present |
| E10 | UI: switch back to ALPHA | UI repo list shows ONLY `alpha-repo` (not bravo-repo, not charlie-anything) |
| E11 | UI: switch to BRAVO | UI repo list shows ONLY `bravo-repo` |
| E12 | curl `:18095/api/v1/{ALPHA,BRAVO,CHARLIE}/repos` (each separately) | each has only its own repo. Confirms server-side isolation matches client view |
| E13 | UI: while on ALPHA's view, refresh page | active-workspace ALPHA persists (server-rendered or client-state recovers) |

### Concurrent UI sessions

| ID | Action | Expected |
|---|---|---|
| E14a | Open Tab 1 to ALPHA, Tab 2 to BRAVO (same browser) | both load independently |
| E14b | Tab 1: edit ALPHA's role description. Tab 2: edit BRAVO's role description. Submit both | each save scopes to its workspace; no cross-tab writes |
| E14c | Refresh both tabs | each tab shows the change made under its own workspace, not the other |

### Lifecycle: delete + CLI consistency

| ID | Action | Expected |
|---|---|---|
| E15 | UI: delete CHARLIE workspace via context menu | success toast; CHARLIE removed from selector |
| E16 | curl `:18095/api/v1/admin/workspaces` | shows `[ALPHA, BRAVO]` only |
| E17 | `loom workspace list` from terminal | shows `[ALPHA, BRAVO]` (matches UI selector) |
| E18 | UI: delete ALPHA, then refresh | UI gracefully handles "active workspace deleted" — prompts to pick another, doesn't crash |
| E19 | UI: under BRAVO, create issue ISSUE-1 (via kanban) | issue appears in BRAVO's kanban |
| E20 | UI: switch to (whatever's left) and check issue list | ISSUE-1 NOT visible (workspace-scoped) |

### Failure modes via UI

| ID | Action | Expected |
|---|---|---|
| E21 | UI: try to create workspace with key=lowercase | inline validation error OR backend 400; not a generic 500 |
| E22 | UI: try to create duplicate workspace | "already exists" message |
| E23 | Disconnect fleet-db (`kill $(cat /tmp/loom-fdb.pid)`); UI tries to create a workspace | error toast, not a silent failure or hang |
| E24 | Reconnect fleet-db; refresh UI | reconnects gracefully (re-fetches list) |

## Pass/fail interpretation

- **All of E1–E24** must pass post-Phase-4 to claim the migration is UI-complete
- E14 (concurrent sessions) and E18 (active-workspace deletion mid-session) are the most likely sources of subtle bugs — pay special attention to console errors during these
- E12 cross-check is non-negotiable: if the UI shows the right thing but curl shows otherwise (or vice versa), there's a state-sync bug between UI client and server

## Cleanup

```bash
kill "$(cat /tmp/loom-serve.pid 2>/dev/null)" 2>/dev/null
rm -f /tmp/loom-serve.pid /tmp/loom-serve.log
# then run e2e-preflight.md cleanup section
```
