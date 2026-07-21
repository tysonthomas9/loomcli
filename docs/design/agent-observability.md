# Agent Observability: Traces, Online Evals, Improvement Dashboard

**Status:** Decisions complete — the wayfinder map (fleet-db epic LOOMCLI-52,
workspace LOOMCLI) closed 2026-07-17; implementation is sliced into
ready-for-agent issues LOOMCLI-69..85 (dependency-wired; ship gate =
LOOMCLI-85, the verify-evals UI suite)
**Date:** 2026-07-16
**Related:** `docs/product/session-artifact-contract.md`,
`docs/observability/tracing.md`, `docs/agents/issue-tracker.md`

## Product decisions (locked 2026-07-16)

Three-part feature:

1. **Traces view** — a new top-level workspace-scoped web-UI view listing ALL
   agent sessions (filter by agent, status, time range) with drill-in to one
   session rendered as a timeline built from the canonical `transcript.Event`
   stream (`tool_use`/`tool_result` paired by `ToolUseID`, reasoning/text
   turns, tokens, diff, errors, subagent transcripts).
2. **Cron eval agent** — a builtin Flue workflow fired by the existing cron
   scheduler (`TriggerBinding{source_kind=cron}`, hourly default). Each tick
   selects a bounded batch of terminal-state `task` sessions that have a
   transcript and no eval yet (orchestration sessions are excluded in v1 —
   see "Orchestration sessions"), runs an LLM-as-judge TaskRun per
   session (the `github-review-agent.ts` pattern), validates the returned
   JSON, and writes one eval record.
3. **Improvement loop = dashboard only** — aggregation endpoint(s) computing
   rollups over arbitrary since/until windows (7/30-day presets): average
   scores over time (bucketed), error-tag frequencies, improvement-category
   insight rollups; rendered with the hand-rolled-CSS chart conventions of
   `ObservabilityDashboard` (no chart library).

### Eval record schema

fleet-db resource `session-evals` (generic control-plane kind, id field
`eval_id`):

- Identity: `eval_id = "eval-" + session_id + "-" + prompt_version`
  (**versioned identity** — bumping the judge prompt version re-evaluates the
  fleet; no flag migration).
- `session_id`, `task_id`, `agent_id`, `workspace_key`
- `scores`: `outcome_success`, `instruction_adherence`, `efficiency`,
  `tool_use_quality` — each 0–100
- `error_taxonomy_tags []string`
- `improvement_categories`: per-category (`harness|linter|prompt|skill`)
  insight strings
- `score_rationales`: per-dimension one-sentence evidence citation (entry
  seq refs) — added by LOOMCLI-62; the prototype showed rationales are how
  scoring gets audited
- `judge_summary`, `judge_model`, `judge_prompt_version`, eval token cost
- `session_started_at`, `session_ended_at` (copied for time-range queries),
  `created_at`/`updated_at` (server-stamped)

## Chosen architecture (pragmatic 3-phase + versioned evals)

*(Amendment, 2026-07-20, LOOMCLI-86: v1's bridge-minted 1:1
legacy `flue-<taskRunID>` session mechanics (`startFlueTaskSession`,
`internal/driver/task_bridge_session.go`) are superseded post-v1 by
`docs/design/task-plane-session-model.md`. That map's migration slice deletes the
bridge mint and updates v1 e2e expectations in
`test/local-mode/verify-evals.sh`, while the doctor `transcript_ref` check
stays task-scoped and unchanged.)*

Selected over a minimal-changes variant and a heavier clean-architecture
variant after a three-way blueprint comparison. Key properties:

- **Phase A — Traces read path.** Workspace-scoped (task-less) variants of the
  existing session service methods; extraction of transcript-ref resolution
  into `internal/transcriptref` (Phase B reuses it from `driverapi`); new
  routes `GET /api/workspaces/{ws}/sessions[...]`; frontend extracts the
  proven transcript-grouping renderer out of
  `IssueDetailPanel/sessions/SessionDetailView.tsx` into a shared
  `TranscriptView` component consumed by both the old task-scoped tab and the
  new Traces view. Since/until/status filtering is **server-side**: Phase A
  depends on the fleet-db agent-sessions list extension (see "Traces list &
  server-side time filtering" below) — client-side filtering over a capped
  page was rejected because fleet-db lists in string-sorted UUID order, so a
  capped page is arbitrary with respect to time.
- **Phase B — Evals.** fleet-db `session-evals` resource (one `loomResources`
  map entry + `filterDocs` query fields + a scoped since/until time-range
  filter on `created_at`); loomcli four-file store stack
  (domain/store/fleetdb/memstore); four driver ops on
  `POST /api/workspaces/{ws}/driver/{op}`:
  - `sessions-list-unevaluated` — server-side candidate join: terminal
    sessions with an explicit `kind == task` filter (orchestration excluded
    in v1 — see "Orchestration sessions") and `Metadata["transcript_ref"]`
    present (the v1 "has transcript" predicate — decided LOOMCLI-65, see
    "Candidate predicate & transcript acquisition"), no
    `eval_status` stamp for the current prompt version, `session_ended_at`
    inside the workspace's lookback window and in-sample for its sampling
    percent, newest-first, bounded batch (workspace setting, default 25,
    server cap 100) — see "Batch selection & cost controls";
  - `session-transcript-get` — resolves `transcript_ref` via
    `internal/transcriptref`, pure ref resolution with no disk fallback
    (predicate and fetch agree by construction; LOOMCLI-65), returns parsed
    canonical events; a present-but-unresolvable ref stamps
    `eval_status=failed` with class `transcript_fetch_failed`;
  - `eval-metric-put` — validates scores/categories server-side, creates the
    eval record idempotently (409 → success), and stamps
    `AgentSession.Metadata` (`eval_status=done|failed`,
    `eval_prompt_version`) server-side in the same call;
  - `eval-rejudge` — the manual re-eval force lane (decided LOOMCLI-66; see
    "Manual re-eval & eval administration").
  Like Phase A's list-extension dependency, Phase B depends on fleet-db
  growing the artifact-content route (`PUT` + `GET
  /api/v1/{ws}/artifacts/{id}/content`) — see "Candidate predicate &
  transcript acquisition".
  Builtin workflows `session-eval-agent.ts` (orchestrator: list → fetch
  transcript → render for the judge (no condensation; see "Judge input &
  transcript rendering") → judge TaskRun with deterministic
  `taskRunId` → validate → put) and `session-eval-task-runner.ts` (LLM leaf,
  `github-review-task-runner.ts` shape). Cron binding provisioned only by
  explicit per-workspace opt-in (`loom evals enable`, see "Cron provisioning
  & enablement" below) — never at serve startup.
  **Failure semantics:** per-session isolation; judge failures stamp
  `eval_status=failed` and are excluded from future batches (no hourly retry
  storm); a new prompt version retries previously-failed sessions.
- **Phase C — Dashboard.** `GET /api/workspaces/{ws}/eval-rollup?since&until`
  computes buckets/frequencies/insight rollups in loomcli (fleet-db stays a
  dumb doc store); `EvalDashboard` panels embedded in the existing
  Observability dashboard.

Each phase lands, builds, and verifies independently (`make gate`,
`npm run check:arch`, real-runtime verification per
`.agent-skills/loom-pr-test/SKILL.md`).

### Judge execution (decided 2026-07-16, LOOMCLI-53)

- **Backend-configurable, codex-only in v1.** `session-eval-task-runner.ts`
  takes a `backend` value but accepts only `codex` for now (anything else
  fails closed as `eval_backend_unsupported`); codex is the repo's only
  schema-enforced structured-output path (`codex exec --output-schema`, the
  `github-review-task-runner.ts` pattern; `LOOM_CODEX_BIN` override applies).
- **Config flows through workflow input.** The orchestrator sources
  backend/model from env (`LOOM_EVAL_BACKEND`, `LOOM_EVAL_MODEL`) with
  in-source defaults and embeds them in each judge TaskRun's input payload;
  the leaf reads `request.input`, not env.
- **Model pinned in source, coupled to `prompt_version`.** The default model
  constant lives beside the prompt version in the workflow source; changing
  the model is a judge-identity change and requires a `prompt_version` bump
  (judge identity = prompt + model). `LOOM_EVAL_MODEL` overrides for
  experiments. The model is always passed as `codex --model <m>` so
  `judge_model` is recorded truthfully (`codex exec` does not report the
  model it ran).
- **Unavailable backend ≠ judge failure.** The orchestrator preflights codex
  availability before selecting a batch; unavailable → the workflow run
  fails, nothing is stamped, the next hourly tick retries (coverage
  deferred, never burned). Per-session failures during a judge run (timeout,
  malformed JSON, crash) keep the `eval_status=failed` stamp.

### Cron provisioning & enablement (decided 2026-07-16, LOOMCLI-58)

- **No auto-provisioning, anywhere.** Not at serve startup, not on workspace
  creation. `loom serve` never writes eval provisioning state; its cron
  scheduler sweeps whatever enabled bindings exist (already all-workspace
  when `LOOM_WORKSPACE` is unset), so multi-workspace serve needs no special
  provisioning story.
- **Explicit per-workspace opt-in is the gate.** A fresh deployment never
  judges sessions or spends LLM tokens without operator action. The opt-in
  act is the consent — it creates the binding `Enabled: true`.
- **The opt-in act is `loom evals enable --workspace <ws>`** — a dedicated
  idempotent verb (Phase B), backed by a single ensure function:
  `EnsureBuiltinWorkflow` for both eval workflows, then upsert of the cron
  binding by `RouteKey` (`cron.session-eval-agent`, hourly). The Phase C
  dashboard wraps the same function as a UI toggle. Disable/pause = flip
  `Enabled` via `loom trigger bindings update` — the sanctioned kill-switch
  lever (input to the cost-controls decision).
- **Re-running the verb is the refresh lever.** On an existing binding it
  re-pins `DriverVersionID` to the currently active builtin and corrects
  schedule drift but preserves the operator's `Enabled` state. Boot never
  self-heals; judge upgrades stay deliberate acts coupled to
  `prompt_version` bumps (see "Judge execution").

### Batch selection & cost controls (decided 2026-07-16, LOOMCLI-57)

- **Rolling lookback window, not a backfill cutoff.** The candidate join
  only considers sessions with `session_ended_at >= now −
  eval_lookback_days` (default 30, matching the dashboard's widest preset).
  A standing predicate, not enablement state: it bounds first enablement,
  every `prompt_version` bump (versioned identity re-opens history), and
  re-enable-after-pause identically. No persisted enablement timestamp.
- **Newest-first selection** (`session_ended_at` descending), overturning
  the oldest-first strawman. Under overload the 7-day dashboard populates
  first and what starves is the tail about to age out of the window;
  oldest-first would starve fresh sessions for the whole drain.
- **Policy knobs are per-workspace UI settings** on the workspace record
  (the `design_format` pattern: workspace field + validated PATCH + a
  "Session evals" SettingsView section), read server-side by
  `sessions-list-unevaluated` on every tick — no restart, no workflow-input
  plumbing:
  - `eval_sampling_percent` — int 1–100, default 100. Deterministic
    membership: `hash(session_id) % 100 < rate`. (A per-tick random roll
    would re-roll unevaluated sessions hourly and converge to 100% coverage
    regardless of the setting; the stable hash keeps the same cohort across
    prompt versions so trends compare like-for-like, and raising the rate
    backfills newly in-sample sessions still inside the window.)
  - `eval_batch_size` — int, default 25; the driver op clamps to 100
    server-side regardless of the stored value.
  - `eval_lookback_days` — int, default 30.
  Env config stays deployment-level only (`LOOM_EVAL_BACKEND` /
  `LOOM_EVAL_MODEL`, see "Judge execution"); workspace policy has no env
  knobs.
- **No explicit spend ceiling in v1.** The ceiling is structural:
  batch × 24 ticks × sampling% (600 evals/day at defaults, 2,400/day hard
  cap); the lookback bounds the re-evaluable universe; failed-stamp
  semantics bound per-session-per-version cost. Eval cost is recorded on
  every record, so a trailing-window budget preflight stays possible later
  without schema change — ruled out of v1.
- **Kill switch = the cron binding's `Enabled` flag — one lever, two
  surfaces.** The "Session evals" settings section gets an enable/pause
  toggle that reads and writes the binding through the same idempotent
  ensure function as `loom evals enable` (see "Cron provisioning &
  enablement"); there is deliberately **no** separate `eval_enabled`
  workspace field. The cron sweeper already enforces `Enabled` natively.
  No env gate — env is read at serve start, and a kill switch behind a
  restart is not one. Implementation note for slicing: `loom trigger
  bindings update` cannot patch `Enabled` today; Phase B adds that flag or
  a `loom evals disable` verb.

### Judge input & transcript rendering (decided 2026-07-16, LOOMCLI-61)

- **One parse path, reasoning restored.** The judge consumes canonical
  `transcript.Event` entries from `session-transcript-get` (parse path
  confirmed against the LOOMCLI-54 fixture set). The raw→canonical converter
  gains a `{role: assistant, type: reasoning}` entry emitted from
  backend-native reasoning items (codex `response_item/reasoning` summaries;
  backends without reasoning emit none). Traces renders the new type as a
  visually distinct block — one converter change serves both consumers.
- **No condensation.** No per-entry truncation, no size cap, no entry drops,
  no boilerplate stripping. The workflow's former "condense" step is pure
  rendering: canonical entries → judge-readable text, everything verbatim
  (system blocks, session prompt, whole tool inputs/outputs, reasoning,
  assistant prose). Fixture evidence: tool results are ~62% of bytes with
  single results up to 36KB, and every truncation heuristic edits what the
  judge believes happened; the whole fixture set fits a judge context
  untouched.
- **Overflow is a named judge failure.** A transcript exceeding the judge's
  context stamps `eval_status=failed` with error class
  `transcript_too_large` so dashboards can split "too big" from judge
  flakiness; standard failed-stamp semantics apply (excluded until a
  `prompt_version` bump). Spend stays bounded by the structural caps (see
  "Batch selection & cost controls"); per-eval cost is recorded.
- **No subagent inlining.** Every session is judged on its own transcript
  only; child sessions (`parent_session_id` linkage) are independent eval
  candidates. Stitched-tree judging was deferred along with the rest of
  orchestration evaluability (LOOMCLI-63) — see "Orchestration sessions".
- **The judge gets execution evidence beyond the transcript.** The rendering
  is prefixed with a structured session-record header — session id, agent
  kind/name, task ref + title, final status, `exit_code`, `error_class`,
  started/ended/duration, `parent_session_id`, retry linkage — plus the
  session's diff stats, which the supervisor already stamps on every
  session record (`files_changed`, `lines_added`, `lines_removed`,
  `files_touched`, `diff.patch` presence — `internal/sessions/diff_metadata.go`).
  Grounded in the false-success
  literature (agents confidently claim success on failed tasks; judges must
  see artifacts, not closing messages) and in the fixtures: the
  watchdog-killed transcript ends mid-`wait` with no terminal marker, and
  the `sleep 900` session is `completed` with a tidy summary.
  *(Amended 2026-07-17 by LOOMCLI-65: the original "full diff when present"
  clause is deferred — v1 ships stats-only artifact grounding with zero new
  acquisition machinery; the judge can still catch "claims success, changed
  nothing". Full patch content returns post-v1 riding a future `diff_ref`
  slice.)*
  *(Note, 2026-07-17, LOOMCLI-67: `error_class` in this header is known to
  be `Unknown` on every platform kill in v1 — the prompt carries
  platform-kill guidance instead; see "Watchdog/kill attribution on
  session records".)*

### Candidate predicate & transcript acquisition (decided 2026-07-17, LOOMCLI-65)

LOOMCLI-60 proved the predicate's naive reading was vacuous in practice: the
supervisor stamps `transcript_ref` best-effort
(`internal/cli/daemon/supervisor/supervisor.go:694-698`), but fleet-db serves
no artifact-content route, so the upload silently drops and 8/8 real terminal
task sessions lacked the ref. The decision keeps the ref as the predicate and
fixes the platform instead of reading around it:

- **Candidate predicate v1 = `transcript_ref` presence**, on top of the
  fleet-db-side filters (terminal state, explicit `kind == task`
  (LOOMCLI-63), no `eval_status` stamp for the current `prompt_version`,
  lookback window, sampling). A pure metadata check — no per-candidate disk
  stats — and one key covers both daemon-path and task-plane sessions.
  Rejected alternatives: `transcript_path` metadata presence (stamped at
  session *creation* regardless of whether transcript bytes ever land, so it
  admits transcript-less sessions) and a driver-op disk-stat "fetchability"
  gate (workable, but couples evals to serve-node disk layout).
- **fleet-db grows the artifact-content route as a blocking Phase B slice**:
  `PUT` + `GET /api/v1/{ws}/artifacts/{id}/content` — the same cross-repo
  posture as LOOMCLI-59 (the platform closes the gap before the feature
  ships). The GET route also lights up Traces' existing `artifact://` ref
  fallback (`session_service.go:487-516`) for sessions not on the serve
  node.
- **Acquisition is pure ref resolution.** `session-transcript-get` resolves
  `transcript_ref` via `internal/transcriptref` with **no disk leg** —
  predicate and fetch agree by construction. Traces (Phase A) keeps its
  proven disk-first chain untouched. A present-but-unresolvable ref stamps
  `eval_status=failed` with named class `transcript_fetch_failed` (analog of
  `transcript_too_large`; standard failed-stamp semantics apply).
- **Upload failure is best-effort everywhere, plus a repair lever.** The
  transcript upload never fails the session or exec: the supervisor keeps
  its warn-and-complete behavior, and the task-bridge upload fallback
  *softens from hard-fail to match* (an observability write must not wedge
  task lifecycle — resolves the error-handling asymmetry). Coverage repair:
  a new doctor check finds terminal task sessions with on-disk transcript
  bytes but no `transcript_ref`, and re-uploads + stamps on `--fix` (the
  `doctor_checks_transcripts.go` shape).
- **Cold start: doctor is the backfill.** Sessions completed before the
  route ships gain refs via `loom doctor --fix`; no automatic sweep at
  serve, enable, or boot (LOOMCLI-58's "boot never self-heals"). The
  `loom evals enable` docs point at doctor as the optional pre-enable step;
  the 30-day lookback ages out whatever isn't repaired.
- **Consistency with `eval-rejudge` (LOOMCLI-66):** verb-time validation
  also rejects sessions without `transcript_ref` — the join can never serve
  them — with `loom doctor --fix` named as the remedy in the error.
- **Diff: stats-only in v1** — see the amendment in "Judge input &
  transcript rendering". No `diff_ref` stamping, upload, or fetch in v1;
  full patch content is post-v1, riding a future `diff_ref` slice.

### Eval rubric & judge prompt v1 (decided 2026-07-16, LOOMCLI-62)

Prototyped against the LOOMCLI-54 fixtures (4 contrast sessions judged by
`gpt-5.6-sol` via `codex exec --output-schema`, zero schema violations);
prompt + schema + judged outputs live on branch `prototype/judge-prompt-v1`
(`docs/design/prototypes/judge-prompt-v1/`). The prompt text there is the
v1 source of record until Phase B inlines it into
`session-eval-task-runner.ts`.

- **Score anchors.** Six shared bands (0–19 failing · 20–39 poor · 40–59
  mixed · 60–79 good with gaps · 80–94 strong · 95–100 exemplary) with
  per-dimension descriptors; dimensions are independent (a `completed`
  session can score 12 on outcome; a killed one 98 on adherence — both
  observed in the prototype); 95+ requires positive in-session evidence.
  **Efficiency judges agent-chosen waste only**: obeying a mandated wait is
  not deducted (tagged instead); waits/loops/detours the agent chose are.
- **Error taxonomy = 12-tag allowlist + `other:<snake_case>` escape.**
  false_success_claim, incomplete_task, instruction_violation, idle_wait,
  redundant_work, tool_misuse, hallucinated_state, scope_creep,
  env_or_dependency_failure, killed_or_truncated, unsafe_operation,
  verification_skipped. Tags record evidenced facts of THIS session —
  `idle_wait` is fact not fault (tagged even when the task mandated the
  wait; fault attribution lives in insights). `eval-metric-put` validates
  allowlist ∪ `other:*` server-side. Empty array = clean session.
- **Improvement insights**: 0–3 per category (harness|linter|prompt|skill),
  each one sentence "Change X so that Y", grounded in a cited entry seq,
  generalizable; empty-preferred-over-filler (the clean planner session
  produced zero, correctly).
- **`score_rationales` added to the eval record** (see schema above): one
  evidence-citing sentence per dimension, `required` in the judge output
  schema.
- **`prompt_version` = plain monotonic `v1`, `v2`, …** — an identity token
  compared only for equality inside `eval_id`; date stamps imply time
  semantics nothing uses. v1 is the version that ships; pre-ship prototype
  iterations don't bump. Model change ⇒ bump (judge identity = prompt +
  model, per LOOMCLI-53).
- **Known calibration gap**: the fixture set contains no
  instruction-violating or false-success session, so adherence bands below
  ~94 and the `false_success_claim` tag are untested; revisit if production
  adherence scores cluster suspiciously high.
- Prototype incidentally validated that the judge leaf can run codex with
  `--sandbox read-only` (it only reasons over its stdin prompt) — stricter
  than the review runner's bypass flag.

### Orchestration sessions: excluded from v1 eval scope (decided 2026-07-16, LOOMCLI-63)

- **V1 evaluates terminal-state `task` sessions only.** The original scope
  line ("task + orchestration sessions with transcripts") matched zero
  orchestration sessions in practice: leads are terminal-backed and no
  current path writes them a transcript (LOOMCLI-54 fixtures). The
  exclusion already happened silently; this makes it deliberate and
  documented. Child sessions spawned by a lead remain independent eval
  candidates, so subagent work is still judged.
- **The candidate predicate filters `kind == task` explicitly** rather than
  letting "has a transcript" do the excluding. If orchestration transcripts
  ever appear, they must not silently enter a rubric whose anchors are
  task-shaped (diff evidence, coder/planner efficiency bands); re-inclusion
  is a deliberate decision — rubric work plus a `prompt_version` bump.
  (Input to the candidate-predicate ticket, LOOMCLI-65.)
- **Future path: both doors deliberately open, no verdict.** If
  orchestration evaluability is ever wanted: (1) lead transcript capture
  (mechanism undesigned — the lead's terminal exchange is unrecorded
  today), or (2) rollup of child evals via `parent_session_id` (fixtures
  prove the linkage; known caveats: child scores already feed dashboard
  rollups, so a synthesized orchestration record double-counts, and
  decomposition/ordering quality never appears in any child transcript).
  Neither is spec'd; the caveats travel with the options.

### Traces list & server-side time filtering (decided 2026-07-16, LOOMCLI-59)

- **Client-side time filtering is rejected for v1; fleet-db closes the gap
  before Traces ships.** The "capped page + client-side filter" premise was
  unsound: fleet-db's `ListJSON` returns agent-sessions sorted by
  string-sorted UUID (`internal/storage/loom_control_plane.go`), so a
  `limit`ed page is arbitrary with respect to time — the newest sessions can
  be entirely absent. The only correct client-side shape (uncapped
  full-workspace fetch; agent-sessions have no TTL or retention, so growth is
  unbounded) was rejected in favor of fixing the source. Phase A therefore
  carries a cross-repo dependency on fleet-db.
- **The fleet-db extension (full bundle, one `filterDocs`/response touch):**
  on `GET .../agent-sessions` — (1) `since`/`until` query params filtering on
  `started_at`; (2) results sorted `started_at` descending with `limit`
  applied after filter+sort, so a limited page is always "the newest N in the
  window"; (3) `kind` and `parent_session_id` join the `filterDocs`
  queryFields — completing the extension
  `internal/infra/fleetdb/control_plane.go` anticipates, letting loomcli drop
  its client-side Kind/ParentSessionID post-filter, and serving the eval
  agent's `kind == task` candidate predicate (LOOMCLI-63/65); (4) a `total`
  field (pre-truncation match count) in the list response.
- **Traces list defaults:** default window = last 7 days; default limit
  = 200; when `total > limit` the list shows a truncation banner ("showing
  newest 200 of N in this range — narrow the time range"); no pagination in
  v1 — narrowing the filters is the recovery path. Grounded in real volume:
  worst observed week ≈ 980 sessions (2026 week-12 burst), recent weeks
  single digits, all-time per-workspace accumulation low thousands.
- **Version skew:** `total` doubles as the capability probe. A pre-extension
  server silently ignores the unknown params and would return time-arbitrary
  data, so a response without `total` makes Traces hard-error with an
  upgrade message. No graceful fallback to client-side filtering.

### Runtime verification & dogfood path (decided 2026-07-16, LOOMCLI-64)

- **Local-mode is the verification surface.** The eval workflow ships only
  after running end-to-end on the repo's canonical runtime stack
  (`.agent-skills/loom-pr-test/SKILL.md`), which today cannot materialize
  Flue workflows: the image carries neither `sdk/` nor a flue runtime, and
  `EnsureBuiltinWorkflow` (the same function `loom evals enable` uses,
  `internal/cli/epic/run.go:289`) hard-requires both
  (`internal/workflows/workflows.go` `writeWorkflowBuildProject`). Host-side
  runs and unit-only coverage are rejected as ship evidence; the e2e
  epic-runner container (`e2e/epic_runner_codex.sh`) stays out of the eval
  story.
- **Image extension (hybrid):** bake `sdk/` into the local-mode image
  (`COPY` + `LOOM_SDK_ROOT` — in-context, branch-coupled); bind-mount the
  sibling flue runtime read-only (`${FLUE_REPO:-../flue}/packages/runtime` →
  `LOOM_FLUE_RUNTIME_ROOT`) with a make-level preflight that fails clearly
  when the checkout is missing or uninstalled (`linkFlueBuildDependencies`
  symlinks `hono`/`@hono/node-server` out of its installed `node_modules`;
  pure JS, so a mac-host mount is safe). Node becomes unconditional in the
  image — the Dockerfile comment already concedes the TS leaf needs Node ≥22
  regardless of backend, but the install is currently gated on
  `INSTALL_CODEX`/`INSTALL_CLAUDE`. `session-evals` compatibility comes from
  the sibling fleet-db checkout via the existing `LOCAL_MODE_COMPOSE_FILES`
  pattern.
- **Evidence on both variants.** Plain deterministic stack proves
  provisioning (`loom evals enable` → workflow + binding exist,
  `Enabled=true`) and the preflight-defer path (due tick with no codex →
  nothing stamped: no records, no `eval_status=failed` — the LOOMCLI-53
  backend-unavailable behavior only manifests on a codex-less stack). Codex
  stack (`make local-mode-codex-up`) proves the full loop: real task session
  → cron tick → real codex judge → LOOMCLI-62-shaped record in fleet-db
  (`eval-<sessionID>-v1`, four in-range scores, valid tags,
  `score_rationales`, truthful `judge_model`, cost) → rendered in Traces and
  the dashboard.
- **Observable cron, no manual-fire verb.** The LOOMCLI-58 ensure function
  gains an optional schedule parameter (default hourly; re-pin semantics
  unchanged); verification enables at `* * * * *` and watches the real serve
  sweep loop (`CronScheduler.RunOnce`, `internal/trigger/cron.go`) fire the
  binding. No manual-fire verb is built — that surface belongs to the
  manual re-eval UX decision (LOOMCLI-66).
- **Standing dogfood path, not CI.** `test/local-mode/verify-evals.sh`
  behind a make target, re-runnable whenever eval machinery changes; manual
  because the full loop needs podman + real codex auth + judge spend.
  `.agent-skills/loom-pr-test/SKILL.md` gets a pointer.
- **Assertion scope: machinery AND UI, phased.** Machinery assertions land
  with Phase B; `agent-browser` assertions against Traces and the dashboard
  rollups land with Phases A/C. The feature's ship gate is the full suite
  green on both variants — rendered UI and API state must agree.

### Manual re-eval & eval administration (decided 2026-07-17, LOOMCLI-66)

- **One judge path; re-eval is a force lane on the standing cron, not a
  second trigger.** A fourth driver op, `eval-rejudge`, deletes the
  session's existing eval record for the current prompt version (if any),
  clears the `eval_status`/`eval_prompt_version` stamp, and sets
  `eval_requested` in `AgentSession.Metadata`. `sessions-list-unevaluated`
  serves requested sessions at the head of the batch, **exempt from
  sampling and lookback** — without the exemption, out-of-sample or
  aged-out sessions (exactly the odd ones an operator investigates) would
  silently never re-run. Deleting the record first means the versioned
  identity `eval-<sid>-<version>` can never 409-silently-discard the fresh
  result.
- **One verb.** `rejudge` covers both administration cases: re-judging a
  `done` session and retrying a `failed` stamp (`transcript_too_large`,
  judge flakiness) short of a `prompt_version` bump. No bare `clear` verb
  exists — a cleared-but-unrequested session subject to sampling/lookback
  is a silent-no-op foot-gun.
- **One request = one attempt.** `eval-metric-put` clears `eval_requested`
  in the same server-side call that stamps `done|failed`; a repeat failure
  re-stamps `failed` and waits for another explicit rejudge or a version
  bump. No retry storm.
- **No spend or kill-switch bypass.** Requested sessions count against
  `eval_batch_size` (head of queue, still batch-capped per tick); on a
  paused binding (`Enabled=false`) requests queue until re-enable, and the
  CLI warns when the binding is disabled.
- **Verb-time validation.** `eval-rejudge` errors on sessions that can
  never be candidates (non-terminal, `kind != task` — the LOOMCLI-65
  candidate shape) instead of queueing a request the join will never
  serve. A never-evaluated session is allowed: rejudge doubles as
  "fast-track this session past sampling/lookback."
- **Destroy-and-replace; no audit trail in v1.** Delete + clear + flag
  happen inside the one driver op, so rollups only ever see the latest
  attempt per (session, version). The discarded attempt's record — and its
  cost datum — is gone; accepted.
- **Surfaces.** CLI `loom evals rejudge <session-id> [<session-id>...]`
  ships with Phase B (beside the machinery it administers); a "Re-judge"
  action on the Traces drill-in's eval panel follows in Phase C, calling
  the same driver op. Settings stays policy-only (knobs + pause toggle).
  Per-session multi-arg only — fleet-wide re-eval remains exclusively the
  `prompt_version` bump.

### Watchdog/kill attribution on session records (decided 2026-07-17, LOOMCLI-67)

- **v1 does not stamp truthful kill causes.** Supervisor-killed sessions
  keep landing as `exit_code -1` / `error_class Unknown`; the supervisor's
  `StopReason` (watchdog / max-retries / config-removed /
  backend-unavailable / shutdown) and the quarantine ledger stay out of
  the session record. Nothing supervisor-side ships with this feature.
- **Mitigation lives in the judge prompt.** The v1 prompt gains
  platform-kill guidance: `exit_code -1` plus a transcript that ends
  mid-action with no terminal marker is likely a platform kill — tag
  `killed_or_truncated`, and score only what the visible transcript
  supports rather than penalizing dimensions for work the truncation
  hides. Pre-ship prompt iteration, so no `prompt_version` bump
  (LOOMCLI-62 rule); verify against the LOOMCLI-54 watchdog fixture; the
  guidance lands with the Phase B prompt-inlining slice.
- **Accepted limitation.** The judge cannot distinguish a watchdog
  no-progress kill (the agent stalled) from a shutdown or backend-outage
  kill (not the agent's fault); every platform kill is scored with the
  same humility, and that kill-type blur stays in the 7/30-day trends
  until the post-v1 mapping lands.
- **Post-v1 upgrade path recorded, not built.** A `StopReason →
  error_class` mapping at finalize is cheap when wanted: the kill site
  sets `StopReason=watchdog` (`supervisor/health.go:197`) and it survives
  to finalize (the quarantine snapshot reads it in the same window); both
  terminal-state writers already stamp `ErrorClass`
  (`session_finalize.go`, the control-plane update in `supervisor.go`);
  quarantine eligibility keys on the internal `Outcome` enum, not the
  stamped string, so it is undisturbed. Landing the mapping changes what
  `error_class` means to the judge ⇒ `prompt_version` bump required.
- **Why `Unknown` today (for the record).** A watchdog kill is signal
  death, so Go reports exit code -1; `classifyAgentExit` ignores
  `StopReason` when a task is attached, the log tail matches no pattern,
  and `classifyByExitCode(-1)` falls through to `Unknown` — the 137/143
  branches never fire on signal death.

## Open decisions (the wayfinder map's tickets)

Detail and resolution live on the map's child tickets; this list is the
signpost:

- ~~Judge backend & model~~ — decided 2026-07-16 (LOOMCLI-53); see "Judge
  execution" above.
- ~~Fixture transcripts captured from a real local-mode stack~~ — done
  2026-07-16 (LOOMCLI-54); fixtures in
  `docs/design/fixtures/agent-observability/`.
- ~~Transcript condensation policy~~ — decided 2026-07-16 (LOOMCLI-61); see
  "Judge input & transcript rendering" above.
- ~~Eval rubric & judge prompt v1~~ — decided 2026-07-16 (LOOMCLI-62); see
  "Eval rubric & judge prompt v1" above.
- ~~Backfill, batch size, and cost controls~~ — decided 2026-07-16
  (LOOMCLI-57); see "Batch selection & cost controls" above.
- ~~Cron provisioning scope & default-on policy~~ — decided 2026-07-16
  (LOOMCLI-58); see "Cron provisioning & enablement" above.
- ~~Orchestration-session evaluability~~ — decided 2026-07-16 (LOOMCLI-63);
  see "Orchestration sessions" above.
- ~~Traces list volume stance~~ — decided 2026-07-16 (LOOMCLI-59); see
  "Traces list & server-side time filtering" above.
- ~~transcript_ref & session-identity coverage across execution paths
  (research)~~ — done 2026-07-17 (LOOMCLI-60); findings in
  `docs/research/transcript-ref-coverage.md` (branch
  `research/transcript-ref-coverage`), settled by LOOMCLI-65.
- ~~Eval candidate predicate & transcript acquisition~~ — decided 2026-07-17
  (LOOMCLI-65); see "Candidate predicate & transcript acquisition" above.
- ~~Eval-agent dogfood path (local-mode cannot run Flue workflows)~~ —
  decided 2026-07-16 (LOOMCLI-64); see "Runtime verification & dogfood
  path" above.
- ~~Manual re-eval & eval-administration UX~~ — decided 2026-07-17
  (LOOMCLI-66); see "Manual re-eval & eval administration" above.
- ~~Watchdog/kill attribution on session records~~ — decided 2026-07-17
  (LOOMCLI-67); see "Watchdog/kill attribution on session records" above.

## Out of scope

- Automated improvement actions (auto-editing prompts/skills/linters or
  filing issues from metrics) — dashboard-only was chosen.
- OTel/Jaeger as the product trace UI (it remains ops tooling; see
  `docs/observability/tracing.md`).
- Evaluating interactive/terminal-kind or maintenance sessions.
- Evaluating orchestration sessions in v1 (LOOMCLI-63) — excluded from the
  eval scope with an explicit `kind == task` predicate; future directions
  (lead transcript capture vs child-eval rollup) both left open, see
  "Orchestration sessions".
- Full diff patch content in the judge input — deferred post-v1 by
  LOOMCLI-65 (amends LOOMCLI-61); v1 grounds the judge with the
  already-stamped diff stats, and patch content returns riding a future
  `diff_ref` slice.
- fleet-db-side rollup computation.
- Judge spend budget ledger (trailing-window cost preflight) — structural
  bounds chosen for v1 (LOOMCLI-57); per-record eval cost keeps it possible
  later without schema change.
- Truthful kill-cause stamping (`StopReason → error_class` at finalize) —
  deferred post-v1 by LOOMCLI-67; v1 accepts `Unknown` plus judge-prompt
  platform-kill guidance, and landing the mapping later requires a
  `prompt_version` bump.
