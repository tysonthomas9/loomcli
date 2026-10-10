---
name: loom-pr-test
description: >-
  Test loomcli pull requests with real Loom workflows: local-mode Podman stacks,
  real daemon/UI/browser checks, and real Codex stack checks. Use when
  validating runtime behavior, Web UI behavior, daemon scheduling, sessions,
  transcripts, diffs, FleetDB compatibility, or Codex CLI integration. Never
  use manual lock files, hand-seeded FleetDB state, fake sessions, or synthetic
  stand-ins as evidence.
---

# Loom PR Runtime Testing

Use this skill when a Loom change needs runtime evidence beyond unit tests.
Default to the real repo workflow. Do not emulate state.

## Non-Negotiables

- Use real `make`, `loom`, `loom serve`, `loom daemon`, browser, API, and CLI workflows.
- Do not manually create or edit `.agent.lock`, FleetDB Redis keys, workspace state files, session records, transcript records, or diff records.
- Do not use synthetic fixtures or hand-seeded stand-ins to prove behavior.
- If a behavior cannot be reached through the product workflow, report it as blocked or unverified.
- Keep the user's real `~/.loom` safe. Use repo-provided containers or an explicit throwaway `LOOM_CONFIG_DIR`.
- For Web UI validation, use `agent-browser` against the running app with an explicit `--profile`.

## Choose The Test Path

Use one of these paths:

1. **UI, daemon, FleetDB, sessions, transcripts, diffs, scheduling**
   - Use the local-mode Podman stack through `make local-mode-up`.
   - Use `make local-mode-codex-up` when the claim depends on real Codex CLI behavior.

2. **Pure code behavior**
   - Run focused unit/integration tests from the PR worktree.
   - Do not start a browser or stack unless the change touches runtime behavior.

Do not add ad-hoc sandbox wrappers or toy repositories for runtime evidence. If a backend behavior cannot be tested through the repo Make/local-mode workflow, extend the real local-mode workflow first or report the behavior as unverified.

## Local-Mode Stack

Run from the PR worktree:

```bash
make local-mode-up
```

Open:

```text
http://localhost:8283/ws/LOCALMODE/kanban
```

Verify:

```bash
make local-mode-verify
```

Stop:

```bash
make local-mode-down
```

The standard stack exercises real Loom services, daemon supervision, FleetDB, API routes, session recording, transcript recording, diff recording, and the Web UI. If the stack uses a deterministic local backend, treat that as stack validation only. Do not use it as proof of real AI backend behavior.

## Real Codex Stack

Use this when the claim depends on a real Codex CLI process:

```bash
make local-mode-codex-up
```

Verify the Codex task IDs:

```bash
make local-mode-codex-verify
```

Common knobs:

The stack installs npm's current `latest` Codex CLI by default. Set
`LOCAL_MODE_CODEX_CLI_VERSION` only when the test requires a reproducible pin.

```bash
LOCAL_MODE_CODEX_HOME=<codex-home> make local-mode-codex-up
LOCAL_MODE_CODEX_CLI_VERSION=0.144.1 make local-mode-codex-up
```

If auth is missing, do not fake the result. Report the missing auth as a blocker or switch to a test path that does not claim real backend behavior.

## Parallel Stacks

Use separate Compose projects and ports. Keep the same project name for logs and teardown.

```bash
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_FLEETDB_PORT=8380 \
LOCAL_MODE_API_PORT=8382 \
LOCAL_MODE_UI_PORT=8383 \
LOCAL_MODE_COMPOSE="docker compose" \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
make local-mode-up
```

Verify the alternate stack with the verifier that matches the stack you started:

```bash
LOCAL_MODE_API_URL=http://127.0.0.1:8382 make local-mode-verify
LOCAL_MODE_API_URL=http://127.0.0.1:8382 make local-mode-codex-verify
```

Stop only that stack:

```bash
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-b \
LOCAL_MODE_COMPOSE="docker compose" \
make local-mode-down
```

Use `127.0.0.1` in verifier/API commands if sandboxed `localhost` access is inconsistent with Podman-published ports.

### Machine Slots (shared hosts)

When several agents share one machine, run every heavy job (`make check-go`, race runs, AFT, local-mode stacks) through `scripts/loom-slot.sh` instead of picking ports by hand. It hands out `LOOM_SLOTS` slots (default 4); each slot gets its own compose project (`loomcli-slot-N`, so its own image tags and volumes), port block, `TMPDIR`, and the durable FleetDB/AFT inputs.

```bash
scripts/loom-slot.sh status                    # who holds which slot
scripts/loom-slot.sh env 2                     # print slot 2's exports
scripts/loom-slot.sh run -- make check-go      # first free slot; waits if all are busy
scripts/loom-slot.sh run --slot 1 -- bash -c '
  LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" make local-mode-up &&
  make local-mode-verify; rc=$?; make local-mode-down; exit $rc'
scripts/loom-slot.sh run -- env LOOM_HARNESS_EMU=1 \
  AFT_SUITES=tests/aft/suites/agents-v1-lead.test.yaml tests/aft/run-aft.sh --strict
```

Slot N uses ports `B+80/82/83` (FleetDB/API/UI) and `B+90/91` (AFT `E2E_PORT`/`E2E_FRONTEND_PORT`), with `B = LOOM_SLOT_PORT_BASE (18000) + N*100`, so slot 1 is 18180/18182/18183 and 18190/18191. `LOCAL_MODE_API_URL` is set to the slot's API, so `make local-mode-verify` needs no flags. `FLEET_DB_REPO` and `AFT_DIR` default to `~/.cache/loom/aft-inputs/{fleet-db,testing-app}`; `LOCAL_MODE_FLEETDB_CONTEXT` points the compose fleet-db build at `FLEET_DB_REPO`. The Go build cache stays shared (it is safe for concurrent use).

Run the whole up/verify/down sequence inside one `run` so the slot is held while the stack exists, and always tear down inside it. The lock is released when the command exits, including on INT/TERM (the command is stopped first), and a lock whose owner died is reclaimed. Compose selection is unchanged: Podman when installed, otherwise `docker compose`; set `LOCAL_MODE_COMPOSE` to force one.

## Compose Overrides

Use `LOCAL_MODE_COMPOSE` to force the compose runner when auto-detection picks the wrong one. Use `LOCAL_MODE_COMPOSE_FILES` for real compatibility overrides, not for fabricated state.

Example:

```bash
LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode-review \
LOCAL_MODE_COMPOSE="docker compose" \
LOCAL_MODE_COMPOSE_FILES=/tmp/fleetdb-review.yml \
LOCAL_MODE_FLEETDB_PORT=8380 \
LOCAL_MODE_API_PORT=8382 \
LOCAL_MODE_UI_PORT=8383 \
LOCAL_MODE_COMPOSE_UP_FLAGS="--build -d" \
make local-mode-up
```

Good override uses:

- Point `fleet-db` at the compatible FleetDB checkout.
- Add real server flags required by the current branch.
- Change ports or resource settings.
- Override `LOCAL_MODE_FLEETDB_IMAGE`, `LOCAL_MODE_LOOM_IMAGE`, or `LOCAL_MODE_LOOM_CODEX_IMAGE` when a parallel run must use explicit image tags. By default, `make` derives image tags from `LOCAL_MODE_COMPOSE_PROJECT`.

Bad override uses:

- Preload issues, locks, sessions, transcripts, or diffs.
- Replace a missing workflow with fake state.
- Hide a real API or daemon compatibility problem.

## FleetDB Compatibility

If agents stay idle, UI data stalls, or APIs return unexpected 404/500 responses:

1. Check Loom and FleetDB logs first.
2. Identify the missing route or server behavior.
3. Read the relevant FleetDB tracking notes or branch history.
4. Build FleetDB from the compatible real checkout using `LOCAL_MODE_COMPOSE_FILES`.
5. Re-run the stack through `make`.

Do not patch around missing FleetDB behavior with direct Redis writes or fake API responses.

Useful checks:

```bash
podman ps
podman logs --tail 120 <fleet-db-container>
podman logs --tail 120 <loom-container>
curl -sS http://127.0.0.1:8282/api/config
curl -sS 'http://127.0.0.1:8282/api/monitor/agents?workspace=LOCALMODE'
```

## Browser Validation

Use `agent-browser` after the stack is up. Always pass a dedicated profile path so cookies, storage, tabs, and browser state do not bleed between reviews or stacks. Use one profile per stack and include a unique run name.

```bash
RUN_NAME=<review-or-branch-name>
PROFILE=/tmp/loom-agent-browser/$RUN_NAME/default
mkdir -p "$PROFILE"

agent-browser --profile "$PROFILE" open http://localhost:8283/ws/LOCALMODE/kanban
agent-browser --profile "$PROFILE" wait
agent-browser --profile "$PROFILE" get text body
agent-browser --profile "$PROFILE" screenshot
```

For parallel stacks, use distinct profiles and session names:

```bash
RUN_NAME=<review-or-branch-name>
PROFILE_A=/tmp/loom-agent-browser/$RUN_NAME/stack-a
PROFILE_B=/tmp/loom-agent-browser/$RUN_NAME/stack-b
mkdir -p "$PROFILE_A" "$PROFILE_B"

agent-browser --profile "$PROFILE_A" \
  --session localmode-a open http://localhost:8283/ws/LOCALMODE/kanban
agent-browser --profile "$PROFILE_A" \
  --session localmode-a get text body

agent-browser --profile "$PROFILE_B" \
  --session localmode-b open http://localhost:8383/ws/LOCALMODE/kanban
agent-browser --profile "$PROFILE_B" \
  --session localmode-b get text body
```

Verify the rendered UI and the API state agree. If they disagree, collect both pieces of evidence.

## Inspecting Real State

Use APIs and daemon commands; do not read or edit backing stores directly.

```bash
curl -sS http://127.0.0.1:8282/api/workspaces/LOCALMODE
curl -sS http://127.0.0.1:8282/api/workspaces/LOCALMODE/issues/<TASK_ID>
curl -sS http://127.0.0.1:8282/api/workspaces/LOCALMODE/tasks/<TASK_ID>/sessions
```

Inside the Loom container:

```bash
podman exec <loom-container> bash -lc \
  'cd /root/.loom/workspaces/LOCALMODE && loom daemon queue local-coder'
```

If a task is open with a design, it is normally coder-eligible. If a task needs planning, it normally needs no design or an explicit revision workflow.

## Reporting

Always report:

- Exact command path used.
- Stack URLs and ports.
- Whether the backend was deterministic or a real CLI.
- Browser evidence when UI behavior matters.
- API/session/diff evidence when daemon behavior matters.
- Any verifier mismatch or blocked condition.
- How to stop any stack left running.

Do not overclaim. If the run used a deterministic backend, say it validated stack/orchestration behavior only.

## Cleanup After Testing

Before finishing, clean up anything you started unless the user explicitly asks to leave it running.

Default local-mode stack:

```bash
make local-mode-down
```

Parallel or named stack:

```bash
LOCAL_MODE_COMPOSE_PROJECT=<project-name> make local-mode-down
```

If the stack used compose override files, pass the same override list during teardown:

```bash
LOCAL_MODE_COMPOSE_PROJECT=<project-name> \
LOCAL_MODE_COMPOSE=<compose-runner> \
LOCAL_MODE_COMPOSE_FILES=/tmp/fleetdb-review.yml \
make local-mode-down
```

Browser profiles:

```bash
agent-browser --profile /tmp/loom-agent-browser/<run-name>/<stack-name> close
```

Remove only the review-specific browser profile directory after closing it, if you created one:

```bash
rm -rf /tmp/loom-agent-browser/<run-name>
```

Do not run broad destructive cleanup commands such as `podman system prune`, `git clean`, or unscoped `rm -rf /tmp/...` unless the user explicitly asks and understands the scope. If a stack is intentionally left running, report its UI/API URLs and the exact command to stop it later.
