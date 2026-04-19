---
name: loom-dev-container
description: Build, run, and drive the loom dev container (Dockerfile.dev) for local end-to-end verification of the web UI and backend APIs. Use when the user wants to "test in the dev container", "run loom locally", "spin up the dev UI", "verify the session detail view", "seed a test session", "exercise a transcript against the real HTTP stack", or otherwise needs a real loom serve + vite preview stack running against podman. Combines with agent-browser for UI-driven checks. Triggers include mentions of Dockerfile.dev, scripts/dev-container-run.sh, loomcli-dev, podman, or a URL at localhost:8091.
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Loom Dev Container

Build and run the full loom stack (API + frontend + bd daemon) in a podman
container so other agents can exercise real code paths without a cloud
deployment or an actual AI agent run.

The container is built from `Dockerfile.dev`; `scripts/dev-container-run.sh`
is the canonical launcher. Everything below wraps those.

## Prerequisites

- `podman` installed (macOS: `brew install podman && podman machine start`)
- `agent-browser` for UI tests (separate skill)
- Optional: `~/.claude`, `~/.codex`, `~/.config/opencode` on host — mounted
  read-only so the container's CLIs inherit your auth
- Optional: `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` in env — forwarded

## Core workflow

```bash
# 1. Build (first time ~3–5min; later rebuilds ~30s with cache)
podman build -f Dockerfile.dev -t loomcli-dev .

# 2. Start (binds host port 8091 → container 3000)
scripts/dev-container-run.sh --no-build      # reuse existing image
scripts/dev-container-run.sh                 # build + run

# 3. Wait for readiness (daemon + API + vite)
until curl -fsS --max-time 2 http://localhost:8091/api/health >/dev/null; do sleep 1; done

# 4. Drive: API via curl, UI via agent-browser. See "Verification patterns" below.

# 5. Cleanup
podman stop loomcli-dev && podman rm loomcli-dev
```

`HOST_PORT=9000 scripts/dev-container-run.sh` runs on a different host port.
`NAME=loom-xyz scripts/dev-container-run.sh` runs multiple containers in
parallel (they isolate by name).

## Stack layout inside the container

| Path                               | Contents                               |
|------------------------------------|----------------------------------------|
| `/root/.loom/workspaces/alpha`     | Primary workspace (has bd daemon)      |
| `/root/.loom/workspaces/bravo`     | Secondary workspace (no bd)            |
| `/root/.loom-config`               | loom CLI state (`LOOM_CONFIG_DIR`)     |
| `/root/.loom/workspaces/<ws>/sessions/` | Per-session dirs + `index.jsonl`  |
| `/usr/local/bin/loom`, `bd`        | Static Go binaries                     |

Workspace IDs are **dynamic UUIDs** — fetch them at runtime, don't
hard-code:

```bash
curl -s http://localhost:8091/api/workspaces | jq -r '.workspaces[] | "\(.name): \(.id)"'
# alpha: 8d5a4b4e-4346-48df-b393-d80142ca4115
# bravo: af00f0b9-9183-42b0-bd8f-8aa057ed737a
```

## Verification patterns

### Backend-only: hit an API endpoint

```bash
WS=$(curl -s http://localhost:8091/api/workspaces | jq -r '.workspaces[] | select(.name=="alpha") | .id')
curl -sS "http://localhost:8091/api/workspaces/$WS/tasks/$TASK/sessions/$SID/transcript" | jq .
```

The transcript API emits one `Event` per content block, not one per JSONL
line. An assistant JSONL line with a mixed `content` array like
`[{text...}, {tool_use...}]` produces two events. So a 3-line sample
transcript (user → assistant text+tool_use → user tool_result) yields
**four** events, not three.

### Seed a fake bd issue + session + transcript

To exercise the SessionsTab → Transcript flow without running a real AI
agent, seed an issue in `alpha`'s bd and a matching session directory:

```bash
# Creates issue, seeds session dir + metadata + transcript + index,
# prints the task ID and session ID.
./.claude/skills/loom-dev-container/templates/seed-test-session.sh
```

The script accepts `TITLE=`, `TRANSCRIPT=` (path to a JSONL file — a
sample lives at `templates/sample-claude-transcript.jsonl`), and
`WORKSPACE=` (default `alpha`).

### UI: drive the session detail view via agent-browser

```bash
# After seeding a task+session:
WS=$(curl -s http://localhost:8091/api/workspaces | jq -r '.workspaces[] | select(.name=="alpha") | .id')
agent-browser open "http://localhost:8091/ws/$WS/kanban"
agent-browser find text "E2E transcript test" click     # the seeded issue
agent-browser snapshot -i                                # refs are fresh after navigation
agent-browser find text "Sessions" click                 # Sessions tab
agent-browser find text "Transcript" click               # transcript sub-tab
agent-browser screenshot --full
```

See the `agent-browser` skill for the full interaction vocabulary. The key
gotcha on the session detail view: the transcript list is a nested
scrollable container — `agent-browser scroll down` moves the outer page.
Use `eval` to scroll the inner pane:

```bash
agent-browser eval --stdin <<'EVALEOF'
Array.from(document.querySelectorAll('*'))
  .filter(el => el.scrollHeight > el.clientHeight
              && getComputedStyle(el).overflowY !== 'visible')
  .forEach(el => el.scrollTop = el.scrollHeight)
EVALEOF
```

## ID validation (reject traversal before hitting disk)

The web service enforces:

- `taskID`      → `^[a-zA-Z0-9._-]+$`  (validators.go)
- `sessionID`   → `^[a-zA-Z0-9._-]+$`
- `subagentID`  → `^[a-zA-Z0-9]+$`     (sessions.SubagentIDPattern)

Use those shapes when seeding; use crafted `../` strings to verify
rejection.

## Troubleshooting

**"Daemon offline" badge in the UI header**
The bd daemon inside `alpha` exited. Check with `podman exec loomcli-dev
bash -c 'cd /root/.loom/workspaces/alpha && bd daemon status'`. Restart
with `bd daemon start` via `podman exec`. Unrelated to the loom serve
itself — API endpoints still work.

**`/api/workspaces/<id>/agents` returns 503**
The `loom daemon` (agent supervisor) isn't running. The dev container
doesn't start it by default — it's only needed for the agents API.
Ignore if you're testing transcripts / sessions / kanban.

**Container starts but UI is blank**
Vite's `--strictPort` fails if port 3000 is taken inside the container.
Check `podman logs loomcli-dev | grep -i vite`.

**Workspace ID changes between runs**
The workspace UUIDs regenerate when `/root/.loom/workspaces/<name>` is
recreated. If you want stable IDs, mount a host dir:
`-v $PWD/.loom-dev-state:/root/.loom:Z`. Otherwise always resolve via
`/api/workspaces` at test time.

**Stale image — fixes not reflected**
The image bakes the code at build time. After editing Go/frontend code
you MUST rebuild with `podman build -f Dockerfile.dev -t loomcli-dev .`
(or drop `--no-build`). A frontend-only change still triggers a full
`npm ci && vite build` in stage 2.

## Templates

| Template                                    | Purpose                                              |
|---------------------------------------------|------------------------------------------------------|
| `templates/seed-test-session.sh`            | Create bd issue + session dir + transcript + index   |
| `templates/sample-claude-transcript.jsonl`  | 3-line Claude Code native transcript (user→asst→tool)|

## Related

- `Dockerfile.dev` and `scripts/dev-container-run.sh` are the source of truth; this skill is a wrapper
- `e2e/` has a different (stub-backend) container for Playwright CI
- `agent-browser` skill: UI interaction vocabulary
- `dogfood` skill: structured bug-hunting reports
