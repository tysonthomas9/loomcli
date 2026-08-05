# E2E manual test plan — CLI + curl

Covers the noun-verb CLI commands (`workspace`, `repo`, `role`, `agentdef`, `daemon profile`) end-to-end against a real fleet-db, and direct fleet-db API verification via curl.

**Prerequisites:** complete `e2e-preflight.md` setup. Sections below assume cloud-mode env unless they explicitly switch to embedded.

## Phase A — Happy path (cloud mode)

Tests CLI writes are visible at the API layer (cross-checked via curl). State is built up sequentially; later tests assume earlier ones passed.

| ID | Command | Pass criteria | Cross-check |
|---|---|---|---|
| A1 | `loom workspace add ACME --description "ACME demo"` | stdout matches `^Created workspace ACME` | `curl :18095/api/v1/admin/workspaces/ACME` → JSON `key:"ACME"` |
| A1.cache | (after A1) `cat $LOOM_CONFIG_DIR/state.json` | contains `"last_workspace": "ACME"` | — |
| A2 | `loom workspace use ACME` | stdout includes `Selected workspace: ACME` and `export LOOM_WORKSPACE=ACME` | state.json `last_workspace=ACME` as a UI hint only |
| A2.env | `export LOOM_WORKSPACE=ACME` | subsequent workspace-scoped commands target ACME | — |
| A3 | `loom repo add backend git@github.com:acme/backend.git --groups infra` | `^Created repo ACME/backend` | `curl :18095/api/v1/ACME/repos/backend` → `name:"backend"` |
| A4 | `loom repo add frontend git@github.com:acme/frontend.git` | `^Created repo ACME/frontend` | `loom repo list` shows both |
| A5 | `loom role add task --description Implementer --backend claude --max-concurrency 5` | `^Created role ACME/task` | `curl :18095/api/v1/ACME/roles/task` → `max_concurrency:5` |
| A6.set | `loom role set task max_concurrency 10` | `^Set ACME/task.max_concurrency = 10$` | **curl** `:18095/api/v1/ACME/roles/task` → `max_concurrency:10` (do NOT trust `loom role show` for this — use curl) |
| A6.verify | (curl above) | response has `"max_concurrency":10` | — |
| A7.unset | `loom role unset task max_concurrency` | `^Cleared ACME/task.max_concurrency$` | **curl** response has NO `max_concurrency` field |
| A7.profile | `loom worker profile add agent-1 --role task --repo backend` | stdout identifies profile `agent-1` | management response has `role:"task", repos:["backend"]` |
| A8 | `loom agentdef add agent-1 --role task --profile agent-1 --auto` | `^Created agent ACME/agent-1` | `curl :8080/api/workspaces/ACME/agent-identities/agent-1` → `behavior.role_name:"task", profile_name:"agent-1"` |
| A8.state | (response from A8 curl) | `desired_state:"running"` is the initial value | — |
| A9 | `loom daemon profile set max_agents 8` | `^Set ACME.max_agents = 8$` | `curl :18095/api/v1/ACME/daemon` → `max_agents:8` |
| A10 | `loom daemon profile show --json` | JSON contains `"max_agents": 8` | matches A9 curl |
| A11 | `loom workspace show --json` | nested `workspace+repos+agents+roles` | counts: 2 repos, 1 agent, 1 role |

## Phase B — Failure modes

Tests every error path produces an actionable, parseable message.

| ID | Command | Expected error pattern | Notes |
|---|---|---|---|
| B1 | `loom workspace add lowercase` | `HTTP 400` AND `domain: invalid value` | fleet-db key regex check |
| B2 | `loom workspace add ACME` (after A1) | `HTTP 409` AND `already exists` | — |
| B3 | `loom agentdef add bad --role nonexistent` | error references the role | fleet-db's referential validation |
| B4 | `loom repo show nope` | `HTTP 404` AND `not found` | — |
| B5 | (env without `LOOM_WORKSPACE`) `loom repo list` | `^Error: no active workspace: set LOOM_WORKSPACE` | runtime commands ignore state-cache defaults |
| B6 | `loom role set task no_such_key value` | `^Error: unknown key "no_such_key"` | — |
| B6.agentdef | `loom agentdef add scoped --role task --repo-groups core` | unknown flag; no Agent identity is created | Repo-group/cross-repo parity has no Phase 5 WorkerProfile contract and fails closed |
| **B7** | **`loom workspace remove ACME` with `LOOM_WORKSPACE=ACME`** | success | **then** `loom repo list` → `^Error: active workspace "ACME" not found in fleet-db` (explicit env becomes stale) |
| **B8** | (corrupt state.json) `echo '{bad json' > $LOOM_CONFIG_DIR/state.json && loom repo list` | error mentions `parse` and the file path | malformed-cache recovery |
| **B9** | (B8 fix-up: write valid state.json with `last_workspace: "DELETED-WS"` and unset `LOOM_WORKSPACE`) `loom repo list` | `^Error: no active workspace` — NOT a panic | stale state cache is ignored by runtime selection |
| **B10** | `curl -X PATCH :18095/api/v1/ACME/roles/task -d '{"clear_max_priority":true,"max_priority":7}'` | API returns 400 (mutually-exclusive flags) OR documents which wins | edge case in fleet-db's PATCH semantics |

## Phase C — Embedded mode

`loom serve`/`loom <cmd>` auto-starts fleet-db + miniredis when `LOOM_FLEET_DB_URL` is unset.

| ID | Test | Pass criteria |
|---|---|---|
| C1 | `loom workspace add EMBED` (cold start) | `mode=local`; `pgrep fleet-db` shows exactly 1 process |
| C2 | (C1 finishes) `loom workspace show EMBED` (separate CLI invocation) | data persists; entries=N>0 in snapshot load log |
| **C3** | **Capture mtime, run `loom workspace show EMBED` twice (no mutations), capture mtime again** | **EXPECTED FAIL**: mtime changes per CLI run. See `known-issues.md` for `loomcli-26v50.41` (dirty-flag skip ineffective in embedded-CLI) |
| C4 | `loom <any cmd> 2>&1 \| grep -E 'waitid\|exited unexpectedly'` | empty (the `.39` race is fixed) |
| C5 | Add 3 workspaces, exit, re-run, verify all present | snapshot round-trip works for HASH+SET+STRING types |
| **C6** | **`pgrep fleet-db` after each test exits** | exactly 0 (no leaked subprocesses) — required for CI |
| **C7** | **Hard-kill scenario: start fleet-db via `loom workspace add KILLME`; kill -9 the loom PID before it exits cleanly; check snapshot file** | exists and is valid JSON (atomicfile guarantees no partial write) |
| **C8** | **Multi-workspace isolation in embedded mode**: add WS1+WS2, write a repo to each, list repos in WS1 | shows ONLY WS1's repo. Then list in WS2 → shows ONLY WS2's |

## Phase D — Direct fleet-db API via curl

Independent of loom CLI; verifies fleet-db schema additions work.

| ID | Endpoint | Body | Expected |
|---|---|---|---|
| D1 | `POST /api/v1/admin/workspaces` | `{"key":"DTEST","name":"D Test"}` | 201 + `key:"DTEST"` |
| D2 | `POST /api/v1/DTEST/repos` | `{"name":"d-repo","remote_url":"git@x:y/z.git"}` | 201 + `name:"d-repo"` |
| D3 | `POST /api/v1/DTEST/agents` | `{"name":"d-agent","role_name":"d-role"}` (after creating d-role) | 201 + `state:"idle"` |
| D4.set | `PATCH /api/v1/DTEST/roles/d-role` | `{"max_priority":50}` | GET shows `max_priority:50` |
| D4.clear | `PATCH /api/v1/DTEST/roles/d-role` | `{"clear_max_priority":true}` | GET response does NOT contain `max_priority` field |
| D4b.budget | (analog) `PATCH …roles/d-role` | `{"max_budget_usd":10.5}` then `{"clear_max_budget_usd":true}` | symmetric set+clear works |
| D4c.concurrency | (analog) `PATCH …roles/d-role` | set + clear `max_concurrency` via `clear_concurrency` | symmetric |
| D5 | `PUT /api/v1/DTEST/daemon` (after setting max_agents=42) | `{}` | **EXPECTED FAIL**: GET still returns `max_agents:42`. See `known-issues.md` for `loomcli-26v50.40` |
| D6 | (no `X-Actor` header) `POST /api/v1/admin/workspaces` | any body | 401 `unauthorized` |
| **D6b** | (X-Actor present, but role lacks PermDaemonUpdate when authz enabled) `PUT /api/v1/{ws}/daemon` | any | 403 forbidden — gated on production auth (skipped in dev mode) |

## Phase F — CLI multi-workspace isolation

The CLI surface needs the same isolation guarantees the UI does.

| ID | Test | Pass criteria |
|---|---|---|
| F1 | `loom workspace add ALPHA && LOOM_WORKSPACE=ALPHA loom repo add alpha-repo git@x:y/a.git` | repo created in ALPHA |
| F2 | `loom workspace add BRAVO && LOOM_WORKSPACE=BRAVO loom repo add bravo-repo git@x:y/b.git` | repo created in BRAVO |
| F3 | `LOOM_WORKSPACE=ALPHA loom repo list` | shows ONLY `alpha-repo` (NOT `bravo-repo`) |
| F4 | `LOOM_WORKSPACE=BRAVO loom repo list` | shows ONLY `bravo-repo` |
| F5 | `curl :18095/api/v1/ALPHA/repos` and `…/BRAVO/repos` | each has exactly its own repo |
| F6 | Concurrent: `loom workspace use ALPHA &; loom workspace use BRAVO &; wait` | state.json contains exactly one of ALPHA/BRAVO as a UI hint (file lock works); runtime commands still require `LOOM_WORKSPACE` |
| F7 | `loom workspace remove BRAVO --force` | deletion succeeds; `loom workspace list` shows ALPHA only |
| F8 | (after F7) `curl :18095/api/v1/BRAVO/repos` | 404 (workspace + cascaded data gone) |

## Pass/fail interpretation

- **A1–A11, B1–B10, C1–C8 (except C3), D1–D4c, D6, F1–F8** must all pass — these gate the CLI surface and isolation guarantees
- **C3 and D5** are documented expected-failures pending bugs `.41` and `.40` respectively. A future "true" pass requires those bugs to land first
- **D6b** is skipped in `--auth-dev-mode` runs; document it as production-only

## Recommended automation

This document is structured for execution by an agent. Each row's "command" can be run sequentially per phase. Failures should report `phase.id` (e.g., `A6.verify FAIL: …`) and continue, accumulating a final pass/fail summary. The pre-flight script in `e2e-preflight.md` should be invoked before phase A; cleanup after phase F.
