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

---

### D3 — What happens when a lead's backend changes between restarts

**Ticket:** `LOOMCLI-174` · **Decided:** 2026-07-22

**Question.** A lead follows the workspace backend. If that flips (codex →
claude) between restarts, the stored conversation belongs to a backend that can
no longer read it. Pin, allow-and-cold-start, or refuse?

**Decision — the conversation pins the lead's backend.** A lead does not follow
*ambient* backend change once it holds a conversation.

#### 1. The pin is derived, not stored

A lead's **effective backend** is the `lead_runtime_provider` recorded on the
`AgentSession` named by `Agent.CurrentSessionID` (D1's head pointer). Only when
there is no head session — or it carries no provider — does the ordinary chain
apply.

```
head-session lead_runtime_provider  >  agent.Backend  >  role.Backend
                                    >  DaemonProfile.AgentBackend  >  codex
```

No new field. A stored `pinned_backend` would duplicate `lead_runtime_provider`
as a second source of truth for one fact — the exact shape D1 rejected when it
distinguished `CurrentSessionID` from the removed `OrchestratorSessionID`.

Consequence: the pin exists *exactly while there is a conversation to protect*,
and dissolves when that conversation is deliberately discarded. A lead that has
never talked still tracks the project default.

Three resolvers implement the lower half of that ladder independently today —
`agentLaunchBackend` (`internal/webui/handlers/terminal/agent_session.go:361-377`),
`reviewerBackend`/`runtimepreflight.ResolveLocalBackend`
(`internal/webui/handlers/prreview/reviewer.go:469-475`), and supervisor
`GetEffectiveBackend` (`internal/cli/daemon/supervisor/backend.go:88-113`). The
pin sits above all of them.

#### 2. Ambient change never wins; deliberate change always does

- **Ambient** — the workspace default (`PATCH /config/backend` →
  `DaemonProfile.AgentBackend`), the role backend, or any change not aimed at
  *this lead*. Never moves a lead that holds a conversation.
- **Deliberate** — an explicit per-agent backend change (`UpdateAgent` carries
  `Backend`, `internal/webui/svcimpl/agent_service.go:539`). Wins, and is treated
  as a deliberate new conversation: cold-start, old session row retained, new
  session minted, `CurrentSessionID` advances per D1. **This is the un-pin.**

The pin defends against ambient drift, not against intent. Refusing a deliberate
change was rejected for the reason "refuse the switch" was rejected wholesale: a
settings edit that fails on hidden per-agent state.

#### 3. The PR reviewer stops migrating on backend

`ensureReviewerAgent` re-resolves the workspace backend on every review open and
calls `migrateReviewer` on mismatch
(`internal/webui/handlers/prreview/reviewer.go:335-361, 484-497`). Its **backend
leg fires only when there is no head-session provider**. Name-generation
retirement (`retireLegacyReviewer`) and role reconciliation
(`reconcileReviewerRole`) are untouched.

`clearReviewerRuntimeMetadata`'s destructive strip of the `lead_runtime_`,
`codex_`, `lead_harness_` prefixes is replaced by the advance rule. Under a
derived pin the strip is **incoherent** — it deletes `lead_runtime_provider`,
the very fact the pin derives from. Retention also leaves the superseded
conversation retrievable, which the strip prevented.

> **Conflict, recorded deliberately.** This reverses shipped behaviour.
> `migrateReviewer` exists *because* a reviewer was wanted to follow the project
> default when it changed, and carries three generations of retire/migrate
> machinery. A future reader who finds the backend migration path half-disabled
> should find this entry rather than re-derive it.

#### 4. The conversation pointer is backend-qualified

A resume key alone is not sufficient identity. **Resume requires
`lead_runtime_provider` equality with the launch backend**; a mismatch is a
cold-start (or, under §5, a hard stop) — never a resume attempt.

Required independently of the pin, because today's protection is accidental:

- `codex_provider_thread_id` and `lead_harness_session_id` are different keys, so
  codex↔harness cannot cross-read — by naming luck, not by rule.
  `priorCodexThreadID` (`internal/leadcontrol/codex_runtime.go:224-232`) never
  consults the provider.
- `lead_harness_session_id` is **shared by every harness backend**
  (`internal/leadcontrol/harness_metadata.go:22`). A gemini→claude flip is a
  genuine cross-read of one backend's session id by another; `lead_harness_name`
  records the truth and the resume path does not read it.

The read path is already provider-qualified — `harnessTranscriptReaders` keys on
the recorded provider (`internal/webui/handlers/prreview/harness_read.go:47-50`).
The resume path must match it.

#### 5. An unhonourable pin fails closed

If the pinned backend cannot be honoured at launch — binary missing, unhealthy,
or no longer in `IsControlledLeadBackend` — the launch **stops** with an
actionable error naming the pinned backend and offering "start a new conversation
on \<default\>". No silent degrade, no automatic cold-start.

Consistent with `PreflightLocalTaskRunner`'s stated fail-closed design
(`internal/runtimepreflight/preflight.go:71-90`) and with `LOOMCLI-171`'s verified
finding that claude hard-fails on a missing transcript rather than cold-starting.
Surfacing goes through `LOOMCLI-175`.

#### 6. UI

**Required.** A **badge where backends are chosen** — the workspace settings
per-agent override list and the lead/reviewer header:
`pinned: codex · project default: claude`. The divergence must be visible at the
place the user would otherwise expect the flip to have applied. Plus an explicit
**"Start a new conversation on \<default\>"** action next to it, performing §2's
deliberate recreate; without it the un-pin is discoverable only by knowing that
stop-and-recreate is what does it.

**Not required by this contract.** A read-only view of the superseded
conversation. Retention is forced by §3, so nothing is destroyed and a later
effort can surface it — but rendering "readable but not resumable" prior
conversations is not part of this decision.

#### Facts settled while deciding

- **Backend failover cannot reach a lead.** `Agent.FallbackBackends` is walked by
  supervisor `GetEffectiveBackend`, but interactive roles cannot be
  daemon-supervised at all: `ResolveRoleConfigStatic` hard-errors with
  *"interactive role %q cannot be daemon-supervised; launch it from a terminal"*
  (`internal/cli/daemon/supervisor/role.go:19-36`). The contract needs no
  failover rule.
- A user-facing per-agent backend edit already exists (`UpdateAgent` →
  `store.AgentUpdate.Backend`), so §2's deliberate path has a surface today; what
  it lacks is the cold-start-and-advance behaviour behind it.

#### Hand-offs

- `LOOMCLI-177` (contract) — take §1's precedence ladder, §4's provider-equality
  rule, and the ambient/deliberate distinction verbatim.
- `LOOMCLI-175` (failed/impossible resume) — inherits §5's unhonourable-pin case
  as one of its surfaces.
- `LOOMCLI-182` (claude resume-vs-fresh argv branch) — the branch gates on
  provider equality per §4, not on the presence of a resume key alone.
- `LOOMCLI-184` (back-compat) — a lead whose pointer predates
  `Agent.CurrentSessionID` has no derivable pin; back-compat must decide whether
  such a lead reconstructs one from its live session's `lead_runtime_provider` or
  is treated as unpinned.
