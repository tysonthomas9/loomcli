# EnsureLeadReady — broker-owned lead recovery (v2)

Status: DRAFT v2 (revised after codex vet; for re-vet + review)
Date: 2026-08-21
Area: internal/placement (broker + reaper), internal/leadprovision, internal/webui, api/openapi

## Why (verified)

The lead Terminal tab shows a truthful `runtime_status` card; "recovery" is
unresolved. Two design iterations were codex-vetted; the following are **verified in
code**, and they set the constraints for this design:

- **`/start` is an async desired-state change**, not a pure `Provision`: it sets
  `desired=running`, creates a `start` command, and kicks provisioning in a goroutine,
  returning 200 before completion (`handlers.go:139,224-252`). Hammering it undoes Stop,
  can't detect success, and churns commands.
- **The reaper is dry-run by default with a 30-min lost grace** (`serve_loops.go`
  enforce gate; `reaper.go:16`). `lost` does not auto-release in a default deployment.
- **Ambiguous-create billing leak (pre-existing bug).** A create error with no sandbox id
  is treated as "no sandbox" → placement `released` (`broker_provision.go` createSandbox;
  asserted by `TestProvisionRecordBeforeCreateFailure`, `broker_test.go:84`). Under
  response-loss (Daytona created the sandbox, the reply dropped) the real sandbox leaks —
  billable, invisible to quota.
- **Stop-vs-provision race.** `needsDaytonaLeadProvision()` (`provisioner.go:165`) gates on
  role kind + provider only — never `desired_state` — so recovery firing after a Stop
  provisions a paid sandbox while the agent says stopped.

Decision (owner: Tyson): build the **full broker-owned reconciler**, and **fix the
create-seam leak first**.

## Architecture

One reconciler, shared by two callers.

```
                 ┌─────────────────────────────────────────┐
 background      │  Broker.reconcileLead(ctx, node, policy) │  ← ONE locked state machine
 reaper  ───────▶│  (per-agent lock; desired+placement+     │
 interactive     │   fresh provider state → single action)  │
 recovery ──────▶│                                          │
                 └─────────────────────────────────────────┘
                      ▲                         ▲
   leadprovision singleflight+async       reaper loop passes its ReaperPolicy
   wrapper passes InteractivePolicy       (enforce, full grace, reconfirm)
   (desired-gated, full grace, enforce
    per operator flag)
```

The state machine lives in `internal/placement` (it needs the placement-private
`lockPlacement`, `markLostAbsenceConfirmed`, `markReleased`, label reconciliation,
fencing). `leadprovision` becomes a thin async/singleflight wrapper; the reaper calls
the same primitive it already half-implements. No second, drift-prone state machine.

## Component 1 — Fix the ambiguous create (FIRST, standalone-shippable)

Bug: `createSandbox` maps *any* create error with empty id to `released`.

Amended after re-vet (codex vet3): a finite **label-*list*** window cannot close the leak —
Daytona's list is eventually consistent (adapter documents it; upstream issue #5138 shows
absent→present flapping ~2.6s). The correctness mechanism is **deterministic naming +
authoritative point-`Get`**, with the two-pass absence protocol as the release gate.

Fix at the broker/provider create seam:

1. **Fail-closed typed create outcome.** Extend `CreateResult` with `Outcome`
   (`internal/placement/provider.go`): zero-value/default = `Unknown`. `NotDispatched`
   **only** for provably-local pre-`Execute` failures (e.g. `createPayload` validation).
   Every `Execute` error → `Unknown`; a 2xx with empty id → `Unknown`. 4xx is `Unknown`
   unless Daytona contracts it side-effect-free. The Daytona adapter cannot generally tell
   post- from pre-dispatch, so it fails closed.
2. **Deterministic provider identity (primary).** Send a stable, collision-resistant sandbox
   **name** derived from Loom's placement identity in the create request (Daytona v0.190
   persists name+labels atomically, enforces uniqueness, and `GetSandbox` resolves an id
   **or name** via a direct repository lookup — authoritative, not list-timed). On an
   ambiguous (`Unknown`) create, **point-`Get` by that name** and validate its
   `loom-placement` label. Label-list reconciliation (`providerSandboxIDForPlacement`,
   which already point-`Get`-confirms each match) is the fallback.
3. **On `Unknown`:** do **not** mark released. Reconcile (name point-`Get`, then label list):
   - exactly one confirmed match → `recordSandboxID` (adopt; P1 → active);
   - multiple → block with a **durably persisted** reason;
   - zero → keep P1 `provisioning`; release only via the **two-pass absence protocol**
     (below). A list/`Get` error is **not** a zero and must block retry without releasing.
4. **Durable two-pass absence for empty-id rows** (min ~12-min window): first zero no earlier
   than the existing `ProvisioningDeadlineAt` (10m) records a persisted first-absence
   confirmation; release only on a second zero ≥ a new semantic `createAbsenceReconfirm`
   (~2m) later. Apply the **same** protocol everywhere an empty-id provisioning row can be
   released — not just `createSandbox`, but `resolveResumeSandbox`, `releaseUnknownSandboxID`
   (`broker_release.go`), and the reaper's empty-id path (`reaper.go`) — because a crash can
   occur after dispatch, before classification. (A recorded-id + point-`Get`-404 stays
   single-pass: point-404 is authoritative for a known identity.)
5. **Reconcile predecessor before admitting a successor.** `preparePredecessorForSuccessor`
   must reconcile the predecessor's provider identity before `Provision` admits a new
   generation (`broker.go:267`): one match → adopt P1 + resume, do **not** admit P2;
   multiple → persist blocked reason, no create; zero → allow P2 only after definitive
   non-dispatch, confirmed deletion, or the completed absence protocol.
6. **Durable placement fields** on `NodePlacement` (`control_plane.go`) for create-ambiguity,
   first-absence-confirmation timestamp, and a reason-code/detail (`LastDeleteError` is the
   wrong home). Update memstore cloning, FleetDB wire round-trip tests, and
   `PlacementNeedsAttention`.
7. **`recordSandboxID` hardening.** Validate the current placement state and reject a newer
   successor row: permit `provisioning → active` and reconciled `released-predecessor →
   active`, and recheck no newer live generation exists — so a multi-instance race cannot
   resurrect P1 after P2.

Ships independently of the recovery UI; closes the leak; the reconciler depends on it.

## Component 2 — Broker-owned reconcile-and-ensure

```
Broker.reconcileLead(ctx, node, policy) → (LeadReadyState, error)
```

Acquires the per-agent lock **once**, then uses locked internal primitives (never re-enters
`lockPlacement`, avoiding the non-reentrant self-deadlock). Decision inputs are
**desired_state + placement_state + a fresh provider probe** — never `runtime_status`
alone (which can't tell a live sandbox from a stopped one, or a crashed provisioning row
from a live one).

`LeadReadyState`: `ready | working | awaiting_confirmation | stopped | blocked | error`
(+ `reason_code`, `detail`).

**Capability policy (not one `enforce` bool).** The op takes a policy with explicit
capabilities — `allow_revive`, `allow_create`, `allow_pty_repair`, `allow_lost_release`,
`dry_run`, `user_confirmed_lost_release` — so the background reaper (observe-only) and
interactive recovery (spend-authorized, user-confirmed lost release) call the *same*
reconciler without the reaper inheriting spend/revival authority.

### Decision table (desired=running unless noted)

| Situation (placement + provider probe) | Action | State |
|---|---|---|
| desired != running | none (no spend) | `stopped` |
| active + provider started + lead PTY present | none | `ready` |
| active + provider stopped/paused/starting/… | revive (EnsureRunning) | `working` |
| active + started + PTY missing | recreate `lead` PTY (stable id, non-destructive) | `working` |
| active + provider error/build_failed | none | `blocked` |
| active/provisioning + no sandbox id | reconcile-by-label → adopt, else per safety window | `working`/`blocked` |
| provisioning + live create holds lock | join | `working` |
| provisioning + stuck (past deadline, no sandbox) | reconcile-by-label → release → retry | `working` |
| released / not_provisioned | reconcile predecessor label → create successor | `working` |
| releasing | complete `releaseLocked` (respect `NextDeleteAt` backoff) → successor | `working` |
| releasing + no sandbox id (malformed) | dead-letter | `blocked` |
| lost + has id + past **full** grace | advance two-confirmation (below) | `awaiting_confirmation`→`working` |
| lost + no id / no LostAt | ineligible under protocol | `blocked` |

### Safe `lost` advance (unchanged safety)

Reuses the reaper's exact protocol under the lock: list-by-label **and** point-`Get`;
pass 1 writes `AbsenceConfirmedAt` → `awaiting_confirmation`; pass 2 (≥ `reconfirmInterval`
later, still absent, fenced by generation+sandbox) → `markReleased(lost_confirmed_absent)`
→ create successor. **Full configured grace is preserved** — not shortened (the two
confirmations both hit Daytona's eventually-consistent control plane, so a shorter window
risks a correlated false-absence double-sandbox). Faster lost recovery is a *deployment*
choice (operator lowers `LOOM_LEAD_LOST_RELEASE_GRACE` + enables enforce), surfaced
honestly in the UI, not a code shortcut.

## Component 3 — leadprovision: thin async + Stop-race close

- Generalize `ReviveCoordinator` into a **singleflight + observable async executor** keyed by
  `(workspace, agent, placement generation)`. It provides only: one in-flight op per key,
  a pollable operation record, and TTL/refcount **eviction** (today's `entries` map never
  evicts — unbounded). Never holds the entry lock during broker/provider I/O.
- **Cache only in-flight ownership.** `ready`/`blocked`/`awaiting_confirmation`/`error`
  describe durable/external state — recompute from store+provider each poll, never cache.
- **Close the Stop race:** re-read `desired_state` **under the broker lock at the spend
  boundary**, immediately before any create/revive. Stop between observe and spend aborts
  with `stopped`. Add `needsDaytonaLeadProvision()` a desired-state check for the
  auto/background path.

## Component 4 — Durable, observable recovery operation (API)

`runtime_status` has no `working`/`blocked`/reason, and dead-lettering only logs. So one
POST + polling `runtime_status` cannot carry recovery state. Instead:

- `POST /api/workspaces/{ws}/agents/{name}/lead/recovery` → 202; **starts or joins** the op
  for the current placement generation. Concurrent/retried POSTs join the same op — they
  never reset `AbsenceConfirmedAt`, start new timers, or spawn a goroutine per request.
- `GET /api/workspaces/{ws}/agents/{name}/lead/recovery` → polls it.
- Response is self-contained: `status`, `reason_code`, sanitized `detail`, `desired_state`,
  `runtime_status`, `placement_id`, `generation`, `retry_after`.
- **Persist** the operation/dead-letter reason durably (survives a lost HTTP reply / later
  poll / process restart) via a new **`LeadRecoveryOperation`** record keyed by
  `(workspace, agent, placement_id, generation)` — *not* an extension of the single
  overwritten `LeadProvisionAttempt` (OQ3).
- Add the endpoint + types to `api/openapi.yaml`, regenerate Go + TS. ⚠️ Run
  `go test ./internal/backend/api/gen/` after `make gen-go-api` (the oapi-codegen
  enum-prefix landmine — see repo memory).

## Component 5 — Consent split (lost vs. automatic)

- **Automatic** recovery (one call on entering a recoverable failed state, gated on
  `desired=running`) may perform only **harmless** actions: probe, revive, recreate PTY,
  and create a successor for `released`/`not_provisioned`.
- **`lost` release is a mutation that requires an explicit user click** — never the auto
  call — and additionally requires reaper-enforce **or** a narrow, **default-off** operator
  flag `LOOM_LEAD_INTERACTIVE_LOST_RELEASE`. This resolves codex's "an auto POST isn't user
  consent for lost mutation" objection.

## Frontend

- Button **"Recover lead" / "Try again"** → `POST .../lead/recovery`; render `{status}`.
- One auto-call on entering a recoverable state (from the response's `desired_state`), then
  **poll GET** (or the monitor stream) — no 30s×5 hammer, no repeated POSTs.
- Honest `lost` copy: "confirming the sandbox is gone…", and on default timings state it can
  take up to the configured grace; a manual **Release & recover** affordance appears only
  when interactive lost release is enabled.

## Phased implementation plan

1. **PR1 — create-seam fix (the leak).** Fail-closed typed `CreateResult.Outcome`
   (default `Unknown`); deterministic Daytona sandbox name + point-`Get`-by-name adoption
   (label-list fallback); `Unknown` never single-observation-releases; durable two-pass
   absence (persisted first-absence + ~2m reconfirm, min ~12m) shared across `createSandbox`,
   `resolveResumeSandbox`, `releaseUnknownSandboxID`, reaper empty-id path; predecessor
   reconcile before successor; durable `NodePlacement` ambiguity/absence/reason fields
   (+ memstore clone + FleetDB wire round-trip + `PlacementNeedsAttention`);
   `recordSandboxID` state/generation hardening. Tests per the vet's list (split
   `TestProvisionRecordBeforeCreateFailure` into NotDispatched-releases vs Unknown-provisions;
   add response-loss-adopts, not-dispatched-releases, multiple-match-blocks, live-predecessor-
   no-successor, two-pass absence, crash-after-dispatch adopt, name-conflict adopt, list/Get-
   error-not-absence, adapter classification). No UI.
2. **PR2 — broker reconciler + capability policy.** `reconcileLead` + a **capability** policy
   (`allow_revive`, `allow_create`, `allow_pty_repair`, `allow_lost_release`, `dry_run`,
   `user_confirmed_lost_release`) — not a single `enforce` bool, so the background reaper
   keeps observe-only semantics and gains no spend/revival authority. Refactor the reaper's
   lost path to call it; decision table; provider-probe helper. Preserve all reaper test
   suites (two-pass, dry-run no-write, reappearance/label-mismatch, fencing).
3. **PR3 — leadprovision async wrapper + Stop-race + API.** Singleflight/eviction (cache only
   in-flight ownership); new durable **`LeadRecoveryOperation`** record keyed by
   `(workspace, agent, placement_id, generation)`; `/lead/recovery` POST/GET; OpenAPI + gen;
   desired-state re-read at the spend boundary.
4. **PR4 — frontend.** Recover button, auto-call-then-poll, honest lost copy, tests.

Each PR passes the pre-push gate independently; PR1 is shippable on its own.

## Codex blockers → how v2 addresses them

1. Wrong seam → **Component 2** moves the state machine into the broker; reaper + interactive
   share it.
2. `runtime_status` too lossy → decisions key on **desired + placement + fresh provider probe**.
3. Unsafe shortcuts → **full grace preserved**; **no enforce bypass** (Component 5 flag,
   default off, lost release requires explicit click).
4. Leak not closed by coalescing → **Component 1** fixes the create seam (label reconcile).
5. Unobservable one-POST → **Component 4** durable operation resource (POST/GET, persisted).
6. Stop race / idempotency → **Component 3** desired-state at spend; single-instance
   invariant stated (multi-instance needs a distributed lease — out of scope, documented).

## Open questions — resolved by re-vet (vet3)

- OQ1 (safety window): **Resolved.** A label-list-only window can't close the leak (list is
  eventually consistent). Use deterministic naming + authoritative point-`Get`-by-name as the
  correctness mechanism; the release gate is the two-pass absence: first zero ≥ existing 10m
  `ProvisioningDeadlineAt`, second zero ≥ a new ~2m `createAbsenceReconfirm` later (min ~12m).
  No new operator config.
- OQ2 (reaper preservation): **Not automatic.** The reaper currently *observes* a stopped
  active sandbox without reviving; the general table would revive it. Hence the capability
  policy in PR2 (`allow_revive`/`allow_create`/…) so the reaper keeps observe-only. Preserve
  the lost-path suites (two-pass, eligibility, transient-Get, reappearance/label-mismatch,
  under-lock candidate change, fencing, dry-run no-write). Orphan enumeration stays
  reaper-owned.
- OQ3 (durable record): **New `LeadRecoveryOperation`**, keyed by
  `(workspace, agent, placement_id, generation)` — not an extension of `LeadProvisionAttempt`
  (single overwritten last-attempt). Low-level create-ambiguity facts live on `NodePlacement`;
  user-visible execution history lives in the operation record. Update OpenAPI + Go/TS atomically.
- OQ4 (single instance): Acceptable **for this POC only** if `loom serve` replica count is
  pinned to one and a second writer is refused/loudly detected. Not a general invariant
  (deploy docs describe k8s Deployments). Scaling beyond one serve process requires FleetDB
  CAS / distributed-lease fencing.
