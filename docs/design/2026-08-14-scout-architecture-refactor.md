# Scout Architecture Refactor — Plan

Status: REV 2 for review · 2026-08-15 · rev 1 vetted by codex (explore, xhigh); all
corrections folded in below and marked ⟲ where they changed the design.
Scope: the five deepening candidates from the 2026-08-14 architecture review of
`feat/ticket-recommender-v1`, reshaped by the decisions below. Companion changes land in
fleet-db (same-named branch). All work stacks on `feat/ticket-recommender-v1`.

## Locked decisions (from review grilling)

1. **Log storage is artifact-only.** Run/task log bytes live in the artifact store. The
   filesystem pathway (`internal/runlog`, `.loom/task-logs`, `.loom/run-logs`) is deleted,
   not wrapped. Existing runs' file-only logs go dark (no backfill) — the UI's existing
   "No AI log is available" state covers them.
2. **Scripted roles are compiled; agent instances are CRUD-able data.** ⟲⟲ A
   compile-time catalog binds machinery to *role names* ("scout", "epic-runner");
   users create/edit/remove AgentService *instances* referencing those roles via
   API/CLI plus a thin Add-agent UI flow. (Earlier revs called this an "agent kind"
   field; decision: the role IS the discriminator — see B2.)
3. **Epic-runner converts now** as the second registry row. Slack-style event triggers
   are planned, so instance bindings must support non-cron kinds from day one.
4. **pr-reviewer's reconciler migrates onto the generic reconciler now.**
5. **The client-side codex transcript parser is replaced ASAP** by the canonical
   transcript pipeline, no backward-compatibility code — the pretty UI's *behaviour*
   is contractual and survives; its data source swaps.
6. **Store conformance becomes the standing rule**, retrofitted to role-CAS and
   driver-run attribution, recorded as ADR-0001.

Sequencing: **Arc A** (artifact logs + canonical transcript) → **Arc B**
(kinds/instances/CRUD) → **Arc C** (conformance + ADRs), C parallelizable against B.
Pipeline per arc: codex implements from a brief → independent gates → e2e on the
isolated stack → browser round → commit.

---

## Arc A — Artifact-only log storage

### A1. The `taskrunlogs` module (new, `internal/taskrunlogs`)

A deep module hiding log-artifact conventions. ⟲ Artifact IDs are **attempt-scoped and
immutable**, not deterministic per run: `store.UploadContentArtifact` does *not* replace
existing content on `ErrAlreadyExists` (it returns the prior artifact), and memstore
overwrites where fleet-db rejects — a deterministic `log-task-<id>` would freeze the
first failed attempt's log across retries and behave differently per backend.

```go
package taskrunlogs

// Put uploads log bytes as a NEW immutable artifact (unique suffixed ID) and returns
// its "artifact://<id>" ref. Content is tail-capped at 1 MiB before upload.
// Empty content → ("", nil): no artifact, empty ref.
func PutTask(ctx, st, ws, taskRunID, content) (ref string, err error)
func PutRun(ctx, st, ws, runID, content)     (ref string, err error)

// Get resolves a previously persisted ref. Missing/invalid → domain.ErrNotFound.
func Get(ctx, st, ws, ref string) (Log, error)   // Log{Content, ModifiedAt, Truncated}
```

⟲ **The persisted ref is the source of truth, not a derivable ID.** `Put`'s returned ref
is written onto the TaskRun (`logs_ref`, the field the bridge already maintains) and the
DriverRun output; serving and availability resolve *through the record*:
`logsAvailable := record.LogsRef != ""` — no existence probe, no new error path in the
list projection, and deterministic-ID authorization bypass is impossible because the
handler still loads the owning record first (preserving today's entity checks).

⟲ **Upload failure never fails the run**: log persistence is best-effort exactly as the
file path is today — failure is logged, `logs_ref` stays empty, run status is untouched.
The current stdout/stderr delimiters in run logs are preserved byte-for-byte.

### A2. Writers — one per level, no double-write

- ⟲ **Task logs: the common task-request layer** (`executeClaimedTaskRunWithResult`,
  which already holds the store, workspace key, and claimed record) is the single
  writer — not the host bridge. The bridge's conditional upload branch
  (`task_bridge_artifacts.go:143`, active only when `LogsRef` is empty) is **removed**;
  its generic runner-artifact registration stays. Runner-supplied `logsRef` values are
  ignored and overwritten with the artifact ref.
- ⟲ **Run logs: `Executor.runClaimed`**, which owns `store.Store` — not `flueRunOutput`,
  which is a free function with no store handle. `flueRunOutput` returns bytes; the
  executor persists and stamps `output.logs_ref` with the real ref (the decorative
  `driver-run://` string dies).
- **Leaves**: scout/local/github leaves stop minting `logs://` refs. ⟲ Also
  `LocalTaskExecutor`'s `task-run://` refs (`task_request.go:144-179`) — all four dead
  vocabularies go.

### A3. HTTP serving — one `serveLog` helper

- `persistedLogDTO`/response declared once; `GET .../task-runs/{id}/log` and
  `GET .../runs/{runId}/log` keep their URLs, load the owning record, resolve
  `logs_ref` via `taskrunlogs.Get`, map ErrNotFound→404. The journal endpoint reuses the
  same tail helper but **stays file-based** — the journal is instance working memory,
  not a log.
- Deleted: `internal/runlog`, `driver/log_persistence.go`, both `resolveRuntimeDir`
  copies, the journal's private reader. ⟲ Deletion inventory includes the dependent
  tests (`task_request_test.go`, `executor_test.go`, both handler test packages,
  `daemon_runtime_test.go` protected-path cases) — rewritten against the artifact path,
  not dropped. ⟲ No `ProtectedRuntimePaths` additions needed: `.loom` already protects
  descendants recursively.

### A4. Canonical transcript (redesigned ⟲)

Rev 1 proposed serve-time parsing of log bytes via `codex.Events`. **That was wrong**:
`codex.Events` parses codex *rollout* files (`{"type":"response_item",...}`) and would
yield zero events for `codex exec --json` output (`{"type":"item.completed",...}`) —
they are different protocols. Serve-time parsing also loses evidence and hardcodes one
backend, while the scout leaf can run codex, claude, gemini, opencode, or cursor.

**Producer-side canonicalization instead — the pattern that already exists:**

- `local-task-runner.ts` already converts its backend stream into canonical
  `transcript_entries`, and the host bridge already stores those as a **transcript
  artifact** (`task_bridge_artifacts.go:125-137`). The scout leaf adopts the same
  producer path: its analyze phase emits `transcript_entries` for whichever backend ran
  (reusing the local-task-runner's per-backend converters, lifted into the shared leaf
  library rather than copied).
- Serving: a task-run transcript endpoint
  (`GET .../task-runs/{id}/transcript`) reads the transcript artifact through the same
  decoding path the session transcript endpoint uses. The raw log artifact remains the
  Raw view.
- ⟲ Frontend `TranscriptEntry` support is **extended before** the old vocabulary is
  deleted: the Go event model includes `reasoning` and `result` (usage) which the
  frontend type union and session renderer currently omit; plain-text, unknown, and
  failed entries get explicit rendering. The run-log chrome keeps its shipped contract
  (see Verification).
- Deleted in the same commit, after parity: `src/utils/transcript.ts`,
  `TranscriptView.tsx`. ⟲ Their test cases (malformed line, reasoning, turn-failed,
  unknown event, real 100 KB sample, plain-only) **migrate** — parser cases to the leaf
  converter tests (node) and renderer cases to the new transcript-row contract tests —
  they are consolidated, not removed.

### A5. Consequences accepted

- Old runs show the existing empty states; records/timelines unaffected.
- fleet-db redis holds ≤1 MiB per attempt; attempt-scoped artifacts accumulate only on
  retries (rare, bounded by retry policy).

---

## Arc B — Agent kinds registry + instance CRUD

### B1. The scripted-role catalog (`internal/scriptedroles`) — pure, dependency-free ⟲

⟲ Rev 1's single package would create an import cycle (provisioning needs `workflows`,
`workflows` imports `driver`, `driver` needs the trust catalog). Split:

- **`internal/scriptedroles`** — pure catalog, imports nothing above `domain`. ⟲⟲ Keyed
  by **role name** (the discriminator, per B2):

```go
type ScriptedRole struct {
    RoleName         string   // "scout", "epic-runner" — the discriminator
    DisplayName      string
    WorkflowName     string   // builtin workflow (bundle entrypoint derives from it)
    LeafRunners      []string // task-runner entrypoints this role's workflow executes
    TrustedLocalCLI  bool     // grants LeafRunners the trusted-cred superset
    Preflight        PreflightPolicy // ⟲ enum: Always | PayloadRunner | None
    JournalFilename  string   // "" = no journal
    AllowedBindingKinds []string // "cron" now; "webhook"/"slack" later
    DefaultRole      RoleSeed        // ⟲⟲ prompt seed for the role record (create-only)
    DefaultInstance  *InstanceTemplate
}
func ForRole(name string) (ScriptedRole, bool)
```

  ⟲ Three formerly-conflated names are separate fields: the workflow bundle entrypoint,
  the trusted leaf runners, and the trigger target entrypoint (`"run"`, part of
  `InstanceTemplate`). ⟲ `Preflight` is a policy, not a bool: scout is `Always`;
  epic-runner preflights only when the payload selects/defaults to the local runner.
- **`internal/agentprovision`** (sibling) — imports catalog + workflows + store; owns
  `EnsureAgentInstance`, role + default-instance seeding, and the reconciler.

Derived call sites (hand-maintained lists die): `isTrustedLocalCLIRunner`;
⟲ **all three** preflight sites (webui `preflight.go`, `workflow_cmd.go`, **and
`cli/epic/run.go:96-137`** — rev 1 missed the third, plus the entrypoint constant in
`internal/runtimepreflight`); the journal identity check; the workflows-POST special
case. ⟲ Remaining scout literals (leaf path defaults, fence markers, journal UI copy,
AuthorAvatar patterns) are inventoried in the implementation brief; frontend copy keys
off kind metadata served by the API.

### B2. The discriminator is the role ⟲⟲

⟲⟲ Rev 2 added a new `agent_kind` field; **decision reversed: the role is the
discriminator.** "scout" becomes a *role*, and an agent instance's machinery resolves
from its existing `role_name` reference through the compiled catalog. No `agent_kind`
field is added anywhere.

- **The scout role is a real record**, seeded by the reconciler if absent (create-only:
  user edits to its prompt are never clobbered). Its prompt is the scout analysis
  preamble — moved out of the leaf's hardcoded string, delivered to the leaf via the
  task payload with the embedded template as fallback. This makes scout's prompt
  viewable/editable through the existing RolePromptCard, retiring the
  "scripted-agent-prompt-note" special case.
- **Machinery binding is compile-time by role name**: `catalog.ForRole("scout")` →
  workflow, leaf runners, trust, preflight, journal, allowed binding kinds. A role with
  no catalog entry is a plain prompt role, exactly as today. Security is unchanged by
  prompt editing: the trusted-credential allowlist keys off compiled leaf entrypoints
  from the catalog, never off role content — editing the scout prompt changes what the
  LLM is told, the same exposure class as any role edit.
- **Shared-prompt semantics (accepted)**: all instances of a scripted role share that
  role. Per-instance prompt divergence (a second scout with a different persona) is out
  of scope for now — noted as future role-derivation work, not smuggled in.
- **Referential rule**: a scripted role cannot be deleted while instances reference it.
- **Rename kept**: `kind` → **`trigger_kind`** across the fleet-db wire and loomcli, as
  already decided (breaking; lockstep cutover with other fleet-db consumers; legacy
  `lead` enum value stays until lead agents get their own modeling). With `agent_kind`
  gone this is the **only** fleet-db companion Arc B needs — `role_name` already exists
  on AgentService.

The record then reads `trigger_kind=cron, role=scout`; the WebUI stops deriving
"scripted/prompt/unknown" and displays catalog metadata resolved via the role.

⟲ **Corrections from the role research** (docs/design/2026-08-15-scout-role-research.md
— verdict: sound, nothing blocks; five adjustments):

1. **Provisioning order + atomic swap.** fleet-db enforces *exactly one* of
   `role_name` XOR driver ref on an agent service and validates the role exists;
   today's provisioner actively clears RoleName. Flipping means: seed the role first,
   then one atomic patch swapping driver-ref→role_name — bundled in the same commit
   with the workflows-POST handler (which reads `svc.DriverID` today; driver resolution
   moves to catalog role→workflow) and `deriveAgentServiceKind`.
2. **Seed a PromptFile, not an inline prompt.** Scout's role is `kind: worker`
   (fleet-db strictly rejects new RoleKind enum values — none needed); a worker role
   with only an inline prompt projects as *uneditable* in the roles module. The seed
   publishes via `roleprompts.Publish`, matching the module's own worker convention.
3. **Prompt injection site is `driver.RequestTaskRun`**, not the POST handler — cron
   runs fabricate their own payloads and would miss handler-level injection. Budget:
   role prompt ≤64 KiB (inline cap is 100 KB, fleet-db body cap 1 MiB, and the payload
   rides an env var with a 128 KiB per-string Linux limit).
4. **Adoption semantics.** A user can pre-create a role named "scout" (CLI or
   interactive auto-role), so "create-only" means **prompt**-create-only: adopting an
   existing same-named record repairs its kind to worker, never touches its prompt.
5. **The referential deletion guard already exists** — fleet-db and memstore both
   refuse deleting a role referenced by a live agent service. Existing behavior gets a
   conformance case, not new code.

### B3. One generic reconciler — lifecycle only ⟲

⟲ Scoped down per vet: the helper owns **error choreography only** (create/get ordering
races, `ErrAlreadyExists` re-get, archived checks); everything entity-specific is a
callback — pr-reviewer's create-first ordering, its role diff rules (kind repair, inline
prompt clearing, description preservation), the ordered PTY-stop migration arm
(`BeforePatch`), role CAS (`ExpectedUpdatedAt` threading with defined conflict
handling), and trigger bindings' route-key alternate lookup:

```go
type Reconciler[T, C, P any] struct {
    Get, Create, Archived, Diff, BeforePatch, Patch // function fields
}
func Ensure[T, C, P any](ctx, create C, r Reconciler[T, C, P]) (*T, error)
```

Consumers: agent instances, trigger bindings, pr-reviewer role + agent (migrated now,
behaviour-preserving — verified by its existing tests plus a PR-panel browser round).
`scout_provision.go` is deleted; its four tests generalize to spec-driven table tests.

### B4. Instance CRUD

⟲ **Stale claim corrected**: fleet-db already has TriggerBinding DELETE (API, redis,
postgres, openapi). The gap is loomcli-side only: `TriggerBindingStore.Delete` on the
interface + memstore + fleetdb client, landing with a conformance suite. The fleet-db
companion for Arc B is the **`agent_kind` field** (B2), not binding delete.

Endpoints (mutations FileAccess-gated, like roles PATCH):

```
POST   .../agent-services            create: role (must be scripted), name, binding
PATCH  .../agent-services/{id}       name, desiredState, binding fields
DELETE .../agent-services/{id}       disable → delete bindings → delete service
```

⟲⟲ Create validates `role` against the scripted-role catalog (`ForRole` hit required —
plain prompt roles are not instantiable as autonomous agents); the role record is
ensured (seed-if-absent) as part of create.

⟲ **Delete semantics pinned** (vet-surfaced hazards):
- Ordered and idempotently retryable: disable first, then bindings, then the service;
  fleet-db rejects service delete while bindings remain, so a partial failure is
  re-runnable, never half-orphaned.
- In-flight/queued runs finish; DriverRun attribution is a snapshot and survives.
- Cron dispatch races (binding snapshot taken before delete) are tolerated: delivery
  for a missing binding is **dropped**, not retried — the delivery sweeper gets an
  explicit missing-binding terminal case instead of burning retry attempts.
- ⟲ Archived defaults are **not resurrected**: an archived record is a tombstone
  (matches today's `ErrInvalidTransition` stance). Deleting the default scout means
  `POST /workflows/scout` runs unattributed (today's non-scout branch) until the user
  re-creates an instance under a new ID. Seeding happens only when the kind has neither
  a live nor an archived default.
- ⟲ `POST /workflows/{name}` accepts optional `agentServiceId` to run as a specific
  instance; default = the kind's default instance if present.

ServiceID grammar (new, CRUD-enforced): `[a-z0-9][a-z0-9-]{0,63}` — required because
IDs become filesystem path segments (B5); fleet-db today only checks non-empty.

CLI: `loom agents add|list|enable|disable|remove`. ⟲ **`loom scout diff/approve`
stays** — the approved proposal's staged-regeneration controls are file-review
operations that instance CRUD does not replace; they gain `--agent <id>` (default: the
default instance).

UI: thin end-to-end Add-agent flow (pick kind → name → schedule), edit
(rename/enable/disable/schedule), remove with confirm. Trigger pickers for
webhook/Slack arrive with those binding kinds.

### B5. Per-instance state namespacing ⟲

- ⟲ **Identity flows server→leaf authenticated**: the executor exports
  `LOOM_AGENT_SERVICE_ID` from `DriverRun.AgentServiceID` into the workflow env (it
  currently exports only workspace/run/node); the workflow passes it through the opaque
  task `input` (verbatim per the SDK contract — no flue SDK change); the leaf validates
  it against the ID grammar before deriving any path. The leaf never receives a raw
  path.
- Journal: `.loom/agents/<serviceID>/<JournalFilename>`. One-time rename of the
  existing workspace-root `history.md` into the default instance's directory at ensure
  time (a file move, not a compat layer — without it scout's dedupe memory goes dark).
- ⟲ **`agents.md.pending` is namespaced too**: staging moves to
  `.loom/agents/<serviceID>/agents.md.pending`; `loom scout diff/approve --agent <id>`
  reads it and merges the instance's fence into the shared `agents.md` on approve.
  Without this, two instances clobber one pending file (proposal §staged-regeneration).
- ⟲ **Fence design pinned**: paired markers
  `<!-- loom:agent:<serviceID>:begin -->` / `<!-- loom:agent:<serviceID>:end -->`;
  serviceIDs are grammar-restricted so no escaping is needed; duplicate begin markers →
  first pair wins, extras removed on rewrite; the legacy scout fence pair is renamed to
  the default instance's markers by its first write. Regions of other instances are
  byte-preserved.
- `.loom` recursive protection already covers `.loom/agents/` (no change).

---

## Arc C — Conformance + decision records

1. **`storetest` suites**: `RoleCAS` (⟲ asserts `domain.ErrConflict` at the store
   seam — HTTP 409 mapping stays a separate client wire test),
   `DriverRunAttribution`, `TriggerBindingDelete` (with its Arc B feature), and ⟲
   `ArtifactCreateRetry` — pinning the create/already-exists/immutability semantics
   Arc A depends on, including the memstore-overwrite vs fleet-db-reject drift found
   during vetting (the suite forces one behaviour).
2. ⟲ **A non-skippable conformance lane**: the fleetdb roundtrip harness currently
   skips without `LOOM_RUN_EMBEDDED_SMOKE=1`; conformance gains a dedicated make
   target that sets the gate and is part of the arc-exit checklist (and CI when this
   branch grows one), so the ordinary test run can't silently pass without fleet-db.
3. **ADR-0001** — store semantics land as conformance suites. **ADR-0002** — logs are
   artifact-backed, no file fallback (records logs-go-dark and why the file pathway was
   deleted rather than wrapped).
4. **CONTEXT.md** — created (repo root) with the program's fixed vocabulary.

---

## Verification plan

Per arc: codex implement → independent gates (Go matrix incl. parity, frontend
typecheck/lint/unit/arch, conformance lane) → e2e on `loomcli-scout-e2e` (bundle
re-registration after digest changes; fleet-db image rebuilt when companions land) →
agent-browser round → stealth commit.

⟲ **Live-run evidence policy** (aligned with the approved proposal): deterministic
orchestration and real-local-backend checks are completion conditions; a *paid* live
LLM run is evidence when warranted, never a gate.

⟲ **Shipped UI contract preserved verbatim** (these are the r7-verified behaviours,
now backed by tests): plain-only logs render raw with no toggle; structured logs
default to Pretty with Raw round-trip; command status/output disclosures; truncation
notice; `logsAvailable=false` fetch-skip for settled tasks while expanded live tasks
poll. The transcript endpoint must reproduce availability + polling semantics exactly.

Arc-specific: **A** — new run's task log served from an artifact (verified via fleet-db
content GET); retried task keeps per-attempt logs; old run shows empty state;
transcript endpoint returns canonical entries for a codex *and* one non-codex backend
fixture; `internal/runlog` gone. **B** — two scout instances on different schedules,
journals and pending files isolated, fences merge; epic-runner instance visible and
preflighting per policy; pr-reviewer identical (tests + browser round); CRUD matrix
incl. FileAccess 403s, route-key 409s, delete-order recovery, archived-default
non-resurrection. **C** — suites red/green on both backends; conformance lane
non-skippable; ADRs + CONTEXT.md reviewed.

## Risks

- Digest churn (leaves + workflow changes): re-register once per arc, documented ritual.
- pr-reviewer regression: behaviour-preserving callbacks + dedicated browser round.
- fleet-db companion ordering: the `trigger_kind` rename (now Arc B's only fleet-db
  change) must merge before loomcli Arc B — and it is breaking for every fleet-db
  consumer, so it needs a coordinated cutover (heads-up to olesho and any other client
  owners), not just merge ordering.
- Redis memory: ≤1 MiB per attempt, bounded by retry policy; dogfood-scale fine.
- ⟲ Transcript producer lift (shared leaf converter library) touches
  `local-task-runner.ts` — its 18-test suite plus github-review leaf must stay green;
  converters move, behaviour must not.

## Out of scope (unchanged)

Journal storage backend (stays a workspace file), light-theme semantic token overrides
(~167 call sites — own visual pass), `useAgentServices` polling-machine dedup
(opportunistic), codegen from fleet-db's OpenAPI (mechanical once conformance exists).
