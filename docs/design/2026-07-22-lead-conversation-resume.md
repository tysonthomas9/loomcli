# Lead conversation resume — decision log & contract (in progress)

**Status:** in progress — accreting one decision at a time.
**Started:** 2026-07-22
**Planning map:** `LOOMCLI-169` (wayfinder map, local fleet-db backend, workspace
`LOOMCLI`). See `docs/agents/issue-tracker.md` for tracker operations.

## Why this doc exists

The wayfinder map's destination is *a settled, written-down contract for how an
interactive lead agent's conversation survives a runtime restart — across
backends, not just codex*, such that someone can implement the claude/gemini path
without re-deciding anything.

The planning tickets live on the **local** fleet-db backend, which persists only
to `~/.loom/fleet-db/redis-snapshot.json` — **not committed, not backed up**.
This doc is the git-durable mirror: each facet decision is appended here as its
ticket closes, and the synthesis ticket (`LOOMCLI-177`) turns the accumulated
entries into the contract proper.

Codex is the reference implementation and already ships (`aec26e468`). Treat it
as evidence, not as the contract.

## Scope

Host-local by design. Conversation content held in fleet-db so a lead resumes
off-host is **out of scope** (a future map); the only in-scope obligation is that
this contract must not foreclose it (`LOOMCLI-178`).

---

## Decisions

### D1 — Where a lead's conversation pointer lives durably

**Ticket:** `LOOMCLI-172` · **Decided:** 2026-07-22

**Question.** Which record owns the pointer to a lead's previous conversation,
such that it survives every way a lead runtime can end?

**Decision — two hops, each a single source of truth.**

1. **`Agent.CurrentSessionID`** — a new *typed* field on `domain.Agent`, scoped to
   `RoleKind == interactive`. It holds the id of the lead's **current/head
   `AgentSession`**. This is the reverse of the existing `AgentSession.AgentID`
   FK: the FK groups a lead's sessions (1:many history); `CurrentSessionID` names
   *which one is current*. It answers "which session to resume" — **not** "which
   session is running".
2. **`AgentSession.Metadata`** — unchanged as the home of the conversation
   *state*, split by lifetime:
   - **durable subset** — `codex_provider_thread_id` (codex) /
     `lead_harness_session_id` (harness) — the resume key;
   - **ephemeral subset** — `codex_app_server_endpoint`, `lead_runtime_status*` —
     re-established every launch. The codex endpoint is a fresh loopback socket
     per run (`net.Listen("tcp","127.0.0.1:0")` → `app-server --listen`,
     `internal/leadcontrol/codex_runtime.go:297,125`), so it must never be
     carried forward stale. Harness has no dialable endpoint at all
     (`harness_metadata.go:34`).

**Invariants.**

- `CurrentSessionID` is **retained when a session stops**. The head session may be
  `completed` / `FinishedAt != nil` and still be the thing to resume. It is *not*
  cleared by `leadSessionFinalizer`.
- It **advances** to a new session id only on a *deliberate new conversation*:
  the user stops and recreates, or recovery when the current session is missing
  or unloadable. On a plain restart/resume it **stays put** and the same session
  row is reused.
- **Liveness** ("is it running right now?") remains a *separate, derived* fact
  (session `Status` + lease), never conflated with this pointer.

**Cardinality.** Same model, two usage regimes: an interactive lead has a real
1:many history (stop-and-recreate is a first-class user action); a PR reviewer is
effectively 1:1, with extra sessions arising only from the missing/unloadable
recovery path.

**Why this is not the field that was deliberately removed.**
`domain.Agent.OrchestratorSessionID` was removed (tombstone at
`internal/domain/agent.go:53-55`) because it *cached a derivable liveness
relationship* — "the currently-active orchestration session", which
`store.OrchestrationSessionFor` computes authoritatively — and it rotted into a
half-deprecation when commit `9aef2ae5` stopped writing it
(`internal/store/orchestration.go:16-19`).

`CurrentSessionID` carries a *different relationship* that the join provably
**cannot** express: the join returns only the most-recent **active** session
(`activeOrchestrationSession`: `FinishedAt == nil` and a live status), so it
returns `""` exactly after a graceful exit — the one moment the resume pointer is
needed. Identity-to-resume and currently-active are different questions, are
never expected to be equal, and have one writer each; so there is no cache to
drift.

**Consumer split.**

| Need | Reads |
|---|---|
| Resume / read the conversation (may be stopped) | `Agent.CurrentSessionID` — terminal-lead launch, PR-reviewer read (`readReviewerSnapshot`), chat view |
| Liveness ("running now?") | `store.OrchestrationSessionFor` — delivery gating, metrics |

**Why the churn exists today.** Restart-resume currently works *by accident*: a
crash/SIGKILL skips `leadSessionFinalizer` (`internal/cli/agent/lead/lead.go:371`),
leaving the session row active, so `ensureTerminalOrchestratorLink`
(`internal/webui/handlers/terminal/agent_session.go:200-214`) reuses its id
instead of minting `"lead-" + uuid`. A *graceful* exit finalizes the row, the
lookup returns `""`, and a fresh empty session is minted — orphaning the pointer.

**Hand-offs.**

- Launch mechanics — reviving a finalized row, and read-before-write so
  `RunHarnessLeadRuntime` stops clobbering the durable key with a freshly minted
  UUID each launch — go to `LOOMCLI-182`.
- Whether the lead's **subject** should be first-class goes to `LOOMCLI-173`.
- `AgentService.StateRef` is the right *shape* but the wrong *plane* (the
  managed-service fleet, not the terminal `Agent` lead) — out of scope.

**Known modelling debt (off-route, not blocking).** The PR reviewer manufactures
its stable identity by encoding a natural key into a surrogate one:
`reviewerAgentName(owner, repo, number)` derives `Agent.Name` from the PR
coordinates plus a disambiguating hash, capped at 100 chars with segment
truncation (`internal/webui/handlers/prreview/reviewer.go:29-84`). The tells are
the truncation-plus-hash and three generations of the scheme with
retire/migrate machinery. The clean model is a first-class subject binding
(subject → agent/session) that is *looked up* rather than *derived*. This does
not affect D1 — `CurrentSessionID` needs *a* stable `Agent` row, however that row
earned its name.
