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

### D2 — Staleness policy when a lead's subject has moved

**Ticket:** `LOOMCLI-173` · **Decided:** 2026-07-22 · **Amended** the same day
after adversarial review (see "What review changed" below).

**Question.** When a lead resumes but the thing it was working on has moved
underneath it, what should happen — and who performs the check?

**Decision — resume, don't refuse; but re-sync first and say so.**

A **subject-freshness check** runs on the launch path, with a **null default**:
a lead whose subject type defines no check is always `up-to-date`. The PR
reviewer is the only non-null implementer today.

**Freshness keys on ref identity, not a pinned SHA.**

- **Identity** — the pair *(head ref, base ref)* plus PR coordinates. Durable.
- **As-of snapshot** — the pair *(head SHA, base SHA)* the conversation reached.
- **`moved`** — re-resolving either ref yields a tip differing from its as-of SHA.

The snapshot is a **pair** because the reviewer prompt defines the review as
`git diff "$(git config loom.reviewBase)"...HEAD`
(`internal/cli/agent/prompts/pr-review-checkout.md:14-17`). A head-only model
reports `up-to-date` when the base advances or the PR is retargeted, while the
diff under discussion has changed — this ticket's own defect on the other axis.

**States.**

| State | Condition | Behaviour |
|---|---|---|
| `up-to-date` | both tips == their as-of SHAs | re-sync (no-op) → resume → no notice |
| `moved` | either tip != its as-of SHA | re-sync both axes → resume → notice naming **which axis** moved |
| `unknown` | no as-of snapshot recorded | re-sync → resume → notice, extent undetermined |
| `subject closed` | PR merged/closed, or head ref unresolvable | **no automatic start**; UI offers a **manual** start → resume the prior conversation, send nothing, wait for the user |

Fast-forward vs non-fast-forward is **collapsed** into `moved`. Head-moved vs
base-moved is **not** collapsed in the notice, because the reviewer's response
differs (re-read commits vs re-diff against a new base).

`unknown` is treated as `moved` deliberately: a spurious notice costs a sentence,
whereas silently continuing on an unverified commit is the defect being fixed.
It also gives back-compat a defined behaviour and needs no separate code path.

`subject closed` distinguishes the **trigger**, not merely the state. Automatic
start is skipped, but a merged/closed PR is exactly when a human wants to ask
"why did you flag that?" — so the UI keeps a manual start that resumes the
conversation and sends **no** review prompt.

**Detection cost.** Head and base *advancement* are pure git (fetch +
`rev-parse`). PR **lifecycle** — merged, closed, draft, retargeted — is **not a
git fact** and requires a connector read; `refs/pull/<n>/head` keeps resolving
after a merge, so ref resolution alone would essentially never fire
`subject closed`.

**Invariants.**

1. **The role's opening prompt is sent only when starting fresh against a live
   subject.** Otherwise the first turn is a staleness notice, or nothing. This
   generalises the existing "don't re-send the role prompt on resume"
   (`codexTUIArgs`, `internal/leadcontrol/codex_runtime.go:183-201`) to cover the
   no-live-subject case.
2. **Re-sync is mandatory whenever we resume.** Never hand a lead a notice about
   tip Y while its cwd sits at X. This is today's actual defect: `agentLaunchCwd`
   (`internal/webui/handlers/terminal/agent_session.go:339`) boots the remembered
   worktree with no re-validation and no re-checkout.
3. The notice is a **fresh user message**, never the role prompt. On codex it
   lands in `resume [SESSION_ID] [PROMPT]` (verified, codex-cli 0.145.0) — but
   that is a codex convention, not the contract; harness uses `PromptFlag` +
   value. The backend-agnostic shape is `LOOMCLI-195`.

**Storage.** Identity + as-of snapshot are durable fleet-db state on
`AgentSession.Metadata`'s **durable** subset (per D1), reached across restarts via
`Agent.CurrentSessionID`. Sorting rule:

> **Can it be recreated after a local purge?** No → durable fleet-db state.
> Yes → purgeable local cache.

Consequence: a purged worktree is **recreatable** from durable identity rather
than fatal — removing today's silent degradation to an empty cwd.

*Open dissent, recorded not resolved:* the adversarial review and D1's own "known
modelling debt" both argue the subject belongs on a **first-class subject
record**, since a session is an *execution attempt* while the subject belongs to
the long-lived assignment. Not overturned here; see `LOOMCLI-177` and the map's
"Not yet specified".

**Responsibility — one freshness function, two callers.**

- The **lead runtime enforces** it on every start path it controls.
- The **web layer queries** it to render the `subject closed` affordance — forced
  by that state, since the UI must know the verdict *before* anything launches.

There is **no single choke point** today: the web terminal builds cwd/argv before
`loom lead` runs (`agent_session.go:319,332,339`) and the controlled runtime can
be bypassed for unsupported backends (`harness_lead_runtime.go:51`,
`lead.go:140`). "Two callers" is therefore a requirement on the design.

**Ordering.** The check must precede cwd baking and process spawn: the codex app
server starts *before* resume is resolved, and both it and the TUI consume
`cfg.WorkDir` (`codex_runtime.go:55,73,126,156`). Adopted shape — a **prelaunch
planner** returning `{verdict, cwd, sync_action, resume_pointer, initial_turn}`.

**What review changed.** A non-Claude model (codex, gpt-5.5 @ xhigh, read-only)
was briefed to refute this decision; findings were re-verified before acceptance.
The core policy survived. Amended: the snapshot became a (head, base) pair;
`ref gone` → `subject closed` with lifecycle conceded as connector-only;
"nothing records the as-of SHA" corrected to "not re-derivable after a purge"
(`loom.reviewHead` does record it, purgeably); "single choke point" retracted;
prelaunch-planner ordering adopted.

**Hand-offs.**

- Surfacing of the refusal and the manual-start affordance → `LOOMCLI-175`.
- Freshness for an **already-running** lead (the `PTYAlive` early return at
  `agent_session.go:99-113` never re-checks) → `LOOMCLI-194`.
- Backend-agnostic delivery of the first turn → `LOOMCLI-195`.
- `check_failed` / `repo_unavailable` — freshness undeterminable → `LOOMCLI-196`.
- When the as-of snapshot advances relative to notice delivery (pending/ack
  state; advancing too early loses the only warning, too late repeats it
  forever) → `LOOMCLI-197`.

**Known caveat.** Re-splitting fast-forward from non-fast-forward later is only
"additive" while the old commit remains reachable: after a force-push plus a
local purge the ancestry test can be uncomputable even though the SHA was stored.

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

---

### D4 — How a failed or impossible resume is surfaced

**Ticket:** `LOOMCLI-175` · **Decided:** 2026-07-22

**Question.** Resume is best-effort: the transcript may be pruned, the id may be
missing, the backend may not support it at all. Today that failure is silent —
the lead cold-starts and the only signal is an empty conversation. What does a
lead owe its reader when it could not continue?

**Decision — one recorded fact, several renderers; never the agent.** The
runtime records a resume **outcome** on the `AgentSession`; the reviewer chat
view, the terminal banner and the logger each *render* that one fact. The agent
is deliberately **not** a reader: telling a lead "your history is gone" changes
the agent's behaviour rather than the human's understanding, and on a cold start
the role prompt already makes it do the right thing.

Rejected: operator-logs-only (invisible to whoever is *acting on* the
conversation) and emit-prose-at-the-moment (it lands only in the terminal
scrollback ring — `internal/webui/terminal/ringbuf.go:9-11`, 256 KB, in-memory,
dies with the loom process, i.e. gone at exactly the restart where resume
matters).

#### 1. The record

**Outcome — three typed values:** `resumed` / `cold_started` / `cannot_resume`.

**Cause — a short, deliberately open token** alongside it: `transcript_missing`,
`home_moved`, `backend_changed`, `backend_unavailable`, `pointer_absent`,
`backend_error`, … The set is **not closed**, so a sibling decision introduces a
cause without amending this contract.

Prose is **rendered at the edge** from (outcome, cause) and never stored:
wording must not require a data migration, and a runtime must not author
user-facing copy.

This decision owns the **vocabulary and the channel**. Which cause fires, and
what the runtime *does* about it, stays with the decision that owns the failure:

| Cause | Owner |
|---|---|
| `transcript_missing` | `LOOMCLI-183` |
| `backend_changed`, `backend_unavailable` | D3 / `LOOMCLI-174` |
| `home_moved` and the container case | D5 / `LOOMCLI-176` (§9 keeps "the mismatch reason code") |
| `pointer_absent` | `LOOMCLI-184` |

#### 2. The evidence must survive the cold start

Today's behaviour is not merely silent — it **erases the evidence**.
`ClearCodexThreadID` (`internal/leadcontrol/codex_metadata.go:110-137`) deletes
`codex_provider_thread_id` when a runtime cold-starts, so nothing downstream can
distinguish *"this lead never had a conversation"* from *"this lead had one and
lost it"*.

**Rebind rather than delete.** The resume key always names the conversation
*this* runtime drives; the abandoned id moves to `prior_conversation_id` on the
same metadata. The delete's original motive — do not point readers at a
conversation this runtime is not driving — is preserved by *rebinding* instead of
*erasure*. Codex rollouts remain on disk in the shared codex home even when loom
cannot resume them, so retaining the id costs one string and keeps a lost
conversation forensically recoverable. Consistent with D3 §3, which likewise
replaces `clearReviewerRuntimeMetadata`'s destructive strip with retention.

**Derived rule — silence on a first launch.** `prior_conversation_id` is itself
the discriminator, so no `no_prior` code is needed:

- `cold_started` **with** a `prior_conversation_id` → notice.
- `cold_started` **without** one → a brand-new lead; say nothing.

#### 3. Scope of "never refuse to start"

**A resume failure never, by itself, refuses a start.** Every failure to continue
a conversation cold-starts and records why: a lead that will not start is
strictly worse than a lead that forgot.

This does **not** contradict the two sibling decisions that *do* stop a launch,
because in both the refusal is caused by something other than the conversation
being unresumable:

- **D2 / `LOOMCLI-173` — the subject is gone.** The conversation is perfectly
  resumable; the *ref* it was about no longer exists. No automatic start, plus a
  manual start that resumes and sends nothing.
- **D3 §5 / `LOOMCLI-174` — the pinned backend cannot be honoured.** The lead
  cannot run *at all* on the backend its conversation belongs to. Cold-starting
  would silently switch backends, which is the exact drift the pin exists to
  prevent.

The rule that survives both: **the conversation's fate never gates the launch;
the subject's and the backend's availability do.**

#### 4. Chrome, not conversation — forced, not preferred

The notice never appears as a message in the transcript. Messages derive purely
from backend-owned sources — `flattenReviewerMessages` off the live app-server
socket for codex, `reviewerMessagesFromEvents` off harness transcript files for
claude/gemini — so loom has nowhere to insert a message it authored without
faking a record into a format it does not own.

(Distinct from D2's staleness notice, which rides codex's `resume [SESSION_ID]
[PROMPT]` positional. That is available only when there *is* a conversation to
resume; here there is not.)

#### 5. Lifetime

The record describes **this session's start**. It lives as long as the session
row and is overwritten by the next start of the same session — under D1 a restart
reuses the row and a deliberate recreate advances the head pointer. It is never
cleared by being read: dismissal is a UI concern, not stored state.

#### 6. Renderers

| Surface | Obligation |
|---|---|
| **Reviewer chat** | Keep the existing `detail` field — **no new API field** on `reviewerConversation`. `state` keeps meaning *liveness*, and a cold-started lead stays healthy (`idle`/`running`). |
| **Terminal** | Required on **both** runtimes. |
| **Operator** | **Warn** on any non-trivial outcome, **Info** on a successful resume. |

**The chat-view change is load-bearing, not cosmetic.** `detail` is currently
read **only** inside `chatUnavailable` (`state === "unsupported" || state ===
"failed"`, `PRDiscussionPanel.tsx:41,118-120`) — a branch that hides the message
list and locks the composer. A present `detail` must instead render as a
**non-blocking banner above a working conversation**. Setting `detail` without
that change is a silent no-op; forcing `state` to `failed` to make it render
would hide a fresh, working conversation and lock the composer on a healthy lead.

**Terminal banner on both runtimes.** Codex already distinguishes resume from
cold start (`internal/leadcontrol/codex_runtime.go:149-153`); the harness runtime
prints only `"Launching controlled %s lead session..."` (`harness_runtime.go:121`)
and must gain the equivalent. Mandated here rather than deferred to
`LOOMCLI-182`, because for any **non-reviewer lead** — which has no chat view at
all — the banner is the only surface that exists.

**No metrics.** `internal/leadcontrol` imports no metrics library; introducing
that dependency is a larger decision than this ticket. Today's channel is
`Logger.Debug` (`codex_runtime.go:177-179`).

#### 7. Inherited surfaces

Four sibling decisions hand their surfacing here. Each renders through §6's
channels; none needs a new one:

| Inherited from | What is surfaced |
|---|---|
| D2 §hand-offs (`LOOMCLI-173`) | The **refusal** when a subject ref is gone, **and the manual-start affordance** — the one case where the UI must offer an action, not just an explanation |
| D3 §5 (`LOOMCLI-174`) | The **unhonourable pin**: an actionable error naming the pinned backend, alongside D3 §6's "Start a new conversation on \<default\>" action |
| D5 §7 (`LOOMCLI-176`) | **Containerised leads are structurally non-resumable** — `/root/.codex` dies with the container and `sessions/` is never copied. Named honestly as `cannot_resume`, not reported as a failure |
| D5 §9 (`LOOMCLI-176`) | What the operator sees when resume is refused |

#### 8. Hand-off to the contract

**Unresumability is a static backend capability, knowable before launch** — not
discovered by attempting and failing. `cannot_resume` is a property of gemini
(and of a containerised lead), not an event that befalls it. The UI can therefore
say "this backend never resumes" without a failed attempt, and `LOOMCLI-177`'s
**null implementation becomes a legitimate member of the contract rather than an
error path**.

#### Precedent deliberately not repeated

`lead_assignment_delivery_error` and `lead_message_delivery_error` are written to
session metadata (`codex_metadata.go:184,208`) and read by **nothing** — no Go,
no frontend. A recorded fact with no renderer is the failure mode this decision
is shaped against, which is why the renderers are part of the decision rather
than follow-on work.

---

### D5 — How a lead runtime pins its backend's conversation home

**Ticket:** `LOOMCLI-176` · **Decided:** 2026-07-22

**Question.** Codex writes conversations under `$CODEX_HOME/sessions`, and loom
never sets `CODEX_HOME` — the runtime inherits the ambient environment
(`internal/leadcontrol/codex_runtime.go:126,158`). If `HOME` or `CODEX_HOME`
differs between restarts, resume degrades to a cold start with no error and no
signal. Does a lead runtime pin its backend's conversation home, and to what?

**Decision — loom is a witness, not an owner.** Loom does not relocate the
conversation. It *resolves* where the conversation lives, *records* that answer
against the head pointer, and *compares* on the next launch. A changed home
becomes a detected condition instead of a silent cold start.

#### 1. Knowability, not isolation

The purpose of the recorded home is that loom can tell whether the conversation
is still reachable from where it now stands — **not** to give each lead a private
store. Isolation was rejected: `CODEX_HOME` is one undivided directory holding
`auth.json`, `config.toml`, `AGENTS.md`, `sessions/`, `archived_sessions/` and
caches (verified on this host, ~3.3 GB), so a per-lead home would force loom to
seed and re-seed credentials into every lead home — buying isolation nobody has
asked for at the cost of credential plumbing loom currently gets for free. It
would also diverge from where a human running `codex resume` by hand finds their
own sessions, which is the tension the ticket named.

#### 2. Record only — loom never exports the home

Loom does **not** set `CODEX_HOME` (or its equivalent) in the child environment.
It resolves the ambient value to an absolute one and stores it. The child
resolves the same way it always has.

This holds together because `CODEX_HOME` and `HOME` are both allowlisted
passthroughs (`internal/cli/envfilter/envfilter.go:11,31`), so loom's resolution
and the child's own resolution agree — *unless* something rewrites the
environment between them, which is precisely the condition being detected. The
contract's guarantee is therefore: **the recorded locator is what the child would
resolve under the same environment.** Loom asserts nothing stronger.

#### 3. The home is an opaque locator, not a path

The contract element is a **backend-computed opaque locator**. Loom stores and
compares it; loom never parses it.

A path field is already wrong at backend #2. Codex's conversation home is a
directory. Claude's is the *pair* (config dir, work dir): transcripts live at
`<config-dir>/projects/<abs-cwd with "/" → "-">/<session-uuid>.jsonl` — verified
on this host, where slugs read `-private-tmp-loom-pr226-host-workspace`. Pinning
`CLAUDE_CONFIG_DIR` alone does not pin a claude conversation; moving the work dir
loses it. Only the backend knows the shape, so only the backend computes it.

(The claude pair is stable for the PR-reviewer case in practice: the work dir is
deterministic at `<wsPath>/.loom/pr-worktrees/<repo>/pr-N`,
`internal/webui/handlers/prreview/reviewer.go:293`.)

Gemini declares **no locator** — it is not resumable (`LOOMCLI-171`), so there is
nothing to record and comparison is skipped.

#### 4. The gate is plain comparison

At launch: resolve the locator now, read the recorded one, compare.

- **Equal** → resume is permitted (subject to the rest of the contract).
- **Differ** → do not resume; cold-start and report a distinct reason
  (`conversation_home_changed`).

No filesystem probing, no rollout walk at launch. "The home matches but the
conversation is gone" is deliberately **not** this decision's problem — it
belongs to `LOOMCLI-183`. Plain comparison also happens to give the right answer
for containers (§7).

#### 5. Storage — on `Agent`, at head-pointer lifetime

The locator is stored on `domain.Agent` beside `CurrentSessionID`, and is
governed by **D1's lifetime rule verbatim**: written atomically with
`CurrentSessionID`, retained across a stop, advancing **only** on a deliberate or
recovery recreate — **never rewritten on a plain resume**.

That last clause is load-bearing. This field only has value *because the home can
change between restarts*; a field rewritten at every launch destroys the value
being compared against and can never detect a mismatch. That is the same
read-before-write clobbering D1 hands to `LOOMCLI-182`, and it is why the naive
"write it each launch" variant was rejected on the record.

**Answering the `OrchestratorSessionID` tombstone** (`internal/domain/agent.go:53-55`),
as the map requires of any Agent-row proposal: the removed field cached a
*derivable liveness relation*. This one caches nothing — the ambient environment
of a *past* launch is not derivable from present state at all. There is no
authority for it to drift against.

**Known tension, handed to `LOOMCLI-177`.** D3 put the other qualifier of a
conversation — the effective backend — on the *session*, derived by reading
`lead_runtime_provider` through the head pointer. This decision puts the home on
the *Agent*. Both are head-pointer-scoped in **lifetime**, but they differ in
**placement**, so a reader discovers two qualifiers of one conversation in two
records by two routes. The synthesis ticket must reconcile placement; nothing in
either decision depends on which way it lands.

#### 6. Scope — ambient default, override reserved

The home defaults to the **ambient host default**, shared with the human, so
debugging a lead's conversation with the vendor's own CLI keeps working. The
contract states the resolver **may** be operator-overridden; no knob is built
until a use case exists.

#### 7. Containerised leads are a known non-resumable environment

Local-mode mounts the host codex home at `/codex-host:ro` and copies **only**
`auth.json` and `config.toml` into `/root/.codex`
(`docs/testing/local-mode-podman-e2e.md:327-330`). `sessions/` is never copied and
`/root/.codex` dies with the container, so a containerised lead has no
conversation to resume — structurally, today.

The contract names this rather than pretending otherwise. The §4 comparison
detects it correctly (recorded host path vs `/root/.codex` differ), and
`LOOMCLI-175` surfaces it. Making container leads resumable is not a contract
term — it is the fleet-db-backed transcript store held by `LOOMCLI-178`.

#### 8. One resolver per backend

Resolution becomes a method on the backend registry (`cli.Backend`), and every
consumer calls it. Today `CODEX_HOME` is resolved independently in three places
for three purposes — the auth check (`internal/cli/backends/backend_codex.go:223`),
the rollout sync (`internal/sessions/codex_rollout.go:79`), and the child's own
ambient inheritance. That triplication *is* the defect class this ticket exists to
close; recording a locator while leaving three resolvers free to disagree would
only move the problem.

#### 9. Boundary

| Concern | Owner |
|---|---|
| Resolve + record the home; the mismatch reason code | **This decision** |
| What the operator sees when resume is refused | `LOOMCLI-175` |
| Home matches but the transcript is gone | `LOOMCLI-183` |

#### Finding: `sqlite_home` holds more than the map claimed — ruled out of scope

While resolving this ticket, inspection of the real loom-owned runtime dirs
(`~/Library/Caches/loom/codex-leads/<ws>/<lead>/<session>`, minted by
`codexLeadRuntimeDirs`, `internal/leadcontrol/codex_runtime.go:258-276`) showed the
map's prior-art line — *"sqlite_home holds only index/state/logs"* — is **wrong**.
It also holds `memories_1.sqlite` and `goals_1.sqlite`, non-empty in 12 of 16 dirs
on this host (40–86 KB), alongside `state_5.sqlite` at ~107 MB each. Three
consequences: the dir is keyed **per session**, so every deliberate recreate
abandons its memories and goals (9 orphaned dirs under `xtermhost/lead` alone);
it lives under `os.UserCacheDir()`, which the OS may purge; and it accumulates
with no reclaim — **1.1 GB** on this machine today.

These dirs cannot move to fleet-db: the `codex` process opens them itself as local
files, and fleet-db is a Redis-backed record store over HTTP. Loom's only choice is
which local path codex gets.

**Ruled out of scope for this map.** The destination is the conversation's
survival across restart; codex's own memory/goal state is a different asset with a
different owner. Tracked separately as `LOOMCLI-198`, and the map's incorrect
prior-art line has been corrected so `LOOMCLI-177` does not synthesise from it.

**Evidence class.** Deterministic (source reading) plus real (filesystem
inspection of `~/.codex`, `~/.claude/projects`, and
`~/Library/Caches/loom/codex-leads` on this host). Nothing live was run.
