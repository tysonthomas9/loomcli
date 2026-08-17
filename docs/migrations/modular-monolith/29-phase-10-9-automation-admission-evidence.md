# Phase 10.9 Automation Admission Evidence

- **Status:** Implementation, focused race proof, affected integration suites,
  full Loom gate, isolated packaged-Desktop build, UI-created system-event
  journey, live Codex transcript, and persisted Review design green
- **Stack:** 10.9 Automation admission consolidation
- **Loom branch:** `modular-monolith-phase10-09-automation-event-admission`
- **Loom base:** stack 10.8 at `f3e5c31bf`
- **Loom implementation head:** `8e5728ec0`
- **FleetDB companion:** `91c99d628`

## Implemented boundary

Automation now exposes three origin-specific public ports:

- `WebhookEventAdmission.AdmitWebhookEvent` accepts only a typed
  `WebhookAuthority` and webhook event content;
- `WorkflowEventAdmission.AdmitWorkflowEvent` accepts only a typed
  `ExecutionAuthority` and workflow event content; and
- `SystemEventAdmission.AdmitSystemEvent` accepts only a typed
  `SystemAuthority` and system event content.

The three public methods perform their matching typed-authority check and
derive provenance that callers cannot construct. They then enter one private
`admitEventAuthorized` implementation for trust policy, replay, binding
matching, reservation, and dispatch. A private `admissionContent` constructor
owns canonical content validation, the 8 MiB payload bound, JSON validation,
payload digesting, and defensive copies.

The retired generic production surface cannot be used to exchange trust
origins. Production no longer contains `EventAuthority`, `AdmitEventCommand`,
`EventAdmission`, the three public authority-envelope constructors, or
`Service.AdmitEvent`. Architecture tests reject their return and restrict each
origin port to its named application adapter.

## Origin trust boundaries

### Webhook origin

`internal/app/webhookingestion` validates only the fields needed to select a
verifier, passes an exact defensive copy of the raw body to that verifier,
derives `WebhookAuthority` only after successful verification, and then hands
the original exact bytes to Automation. JSON normalization and validation
happen after verification. The caller cannot mint external origin, verified
signature status, hop depth, parent event, or idempotency.

### Workflow origin

`internal/app/workfloweventing` requires a separately verified, running
Execution parent with workspace, run, node, lease, and positive fencing token.
It derives `ExecutionAuthority` before entering Automation. Automation reloads
the durable emission context and rejects workspace or exact owner-tuple drift;
the caller cannot supply origin, internal route, actor, parent hop depth, or
idempotency.

### System origin

`internal/app/systemeventing` binds issue-journal and Driver-run-outcome
emitters to registered component IDs during composition. The consumer port has
no component selector. The authority provider derives action-scoped
`SystemAuthority`, after which Automation derives internal source, route,
actor, and idempotency. A caller cannot impersonate another registered system
producer.

## Required proof matrix

| Required behavior | Authoritative proof | State |
|---|---|---|
| One private owner implementation | All three origin methods enter private `admitEventAuthorized`; arch tests reject the generic public seam. | Green |
| Origin-specific typed authority | Compile-time ports accept only webhook, Execution, or system authority respectively; named-adapter ratchet rejects cross-origin invocation. | Green |
| Exact webhook bytes | Adapter tests prove verification precedes malformed-JSON rejection, verifier mutation cannot alter admitted bytes, and Automation receives the exact verified body. | Green |
| Workflow running parent | App tests reject non-running/incomplete parents; owner tests reload durable context and reject workspace, node, lease, or fence drift. | Green |
| Registered system producer | Bound issue-journal and run-outcome ports capture component identity; system workflow tests reject invalid source/workspace and authority failure before admission. | Green |
| Shared content invariants | Table tests prove all three origins reject malformed JSON and payloads over 8 MiB before matching or reservation. | Green |
| Defensive ownership | Tests prove whitespace-only webhook content normalizes to `{}` only after verification handoff and caller mutation cannot change payload or subject attributes. | Green |
| Authority before content | A mismatched typed authority is denied before malformed content validation and before persistence side effects. | Green |
| Replay, trust, and dispatch | Existing owner suites cover replay-first recovery, fingerprint conflicts, trust-policy denial, hop caps, reservation, partial dispatch failure, and retry. | Green |
| Packaged UI system event | UI-created no-design task emitted `task.ready`, ran a live Codex planner once, saved a design, moved to Review, and exposed the finalized transcript plus clickable task attribution. | Green |

## Verification

The focused owner, all three application adapters, and architecture ratchet
passed:

```text
GOCACHE=/private/tmp/phase10-09-go-cache \
  go test ./internal/modules/automation \
    ./internal/app/webhookingestion \
    ./internal/app/workfloweventing \
    ./internal/app/systemeventing \
    ./internal/archtest \
    -run 'Admission|Ingest|Emit|Phase1009' -count=1
```

The same scope passed with the race detector:

```text
GOCACHE=/private/tmp/phase10-09-race-go-cache \
  go test -race ./internal/modules/automation \
    ./internal/app/webhookingestion \
    ./internal/app/workfloweventing \
    ./internal/app/systemeventing \
    ./internal/archtest \
    -run 'Admission|Ingest|Emit|Phase1009' -count=1
```

Affected composition and transport integrations passed:

```text
GOCACHE=/private/tmp/phase10-09-go-cache \
  go test ./internal/app/serve ./internal/cli/serve \
    ./internal/webui/handlers/driverapi \
    ./internal/webui/handlers/webhooks \
    ./internal/webui/handlers/connectors
```

The authoritative Loom gate passed against the paired FleetDB checkout:

```text
FLEET_DB_REPO=/Users/tyson/codebase/code-agents/rc-2/fleet-db-modular-monolith-phase7 \
FLEET_DB_BIN=/private/tmp/fleetdb-phase10-08-bin \
GOCACHE=/private/tmp/loom-phase10-09-gate-go-cache \
make gate

=== Go quality gates PASSED ===
=== Frontend quality gates PASSED ===
=== All quality gates PASSED ===
```

## Packaged product proof record

The implementation head built an isolated packaged app at
`/private/tmp/phase10-09-tauri-target/release/bundle/macos/Loom Agents.app`
with:

- Tauri executable SHA-256
  `bb8a52ec11014379408e758bc90743b761f7391999890b42e4dadd8d765d4733`;
- Loom sidecar SHA-256
  `9ceca5355bf60f1bea2afeae71f06895210cb2240c80fd95d22210f54bbbcad5`;
- FleetDB sidecar SHA-256
  `a3ef6e43384218c12c9f5081b79603730d5bab7c1a53f4457391150ef38a1410`;
- Loom build `8e5728ec0` and FleetDB source `91c99d628`; and
- isolated runtime data `/private/tmp/phase10-09-desktop-data`.

The packaged runtime started on dynamic URL `http://127.0.0.1:53045`, and its
real `/api/health` endpoint returned HTTP 200 with `{"status":"ok"}`. A final
packaged `loom local status --json` reported the same build and binary hash
with `healthy: true` after the product journey.

All product state was created through the packaged Desktop UI:

1. Created workspace `PHASE10-09-PROOF-20260817`.
2. Admitted the run-owned local repository `phase10-09-proof-repo` from
   `/private/tmp/phase10-09-proof-repo`.
3. Created autonomous Behavior Planner
   `agt-phase10-09-behavior-planner-c4bfd405` with the
   `internal.task.ready` trigger and default live Codex backend.
4. Created no-design task `PHASE10-09-PROOF-20260817-1` in Open with the proof
   repository selected.
5. Observed the system event assign the planner and move the task to In
   Progress without a manual status change.
6. Observed exactly one Automation run,
   `automation-run-8c8f9f7507592ce75e4bde8ecb27f300`, complete in 1m46s.
7. Opened its finalized live Codex transcript and the clickable task link.
8. Opened the task in the right-side panel and observed Review, the persisted
   full design, and design artifact
   `design-phase10-09-proof-20260817-1-cd547cc13b7d9430e11d947a900b46ec2e3b8509c4120d3f5a1051003657ccaa`.

The run projected 272.5k tokens, exit 0, runtime `local-cli-codex`, delivery
`patch back`, finalized transcript, and no diff. The completed summary was
`prompt-agent: PHASE10-09-PROOF-20260817-1 planned via codex (design handoff,
status=review)`. A read-only packaged-CLI projection independently confirmed
`status: review`, `has_design: true`, the same design artifact, and source repo
`phase10-09-proof-repo`.

The detached task worktree remained clean at fixture commit
`ff494543f3c75dabf7dd75d9256b6717fdf2f76b`; the source fixture and admitted
workspace checkout were also clean. This proves design-only delivery did not
write repository content.

Inspected screenshots (JPEG, SHA-256):

- `/private/tmp/phase10-09-planner-transcript.jpeg` — completed live Codex
  run, clickable task, execution metrics, and transcript,
  `6ffdccfd162ffc6506440f63dd378fe5c95f4ecf5c719f1ac38b7a4d5e63e7ba`;
  and
- `/private/tmp/phase10-09-task-review-design.jpeg` — Review status, task
  details, persisted design summary, completed run, and no-PR delivery,
  `1b9c189a5a6f2203fe45c0df2fc61bf3ffee7fb8c529d2ddd22c8993b6ab7ae3`.

The packaged Tauri window was relaunched after an overnight macOS lock left
its WebKit surface blank; only the run-owned shell was stopped. The same
isolated Loom/Fleet service PIDs, port, data directory, and task state remained
healthy and were reused. No foreign Loom app, persistent Desktop data, fixed
port, browser profile, or GitHub resource was touched. Webhook signature
forgery and workflow owner-fence negatives have no honest product UI action;
their authoritative proof remains the focused and race-enabled adapter/owner
suites rather than an API-only imitation of a user journey.

## Next stack

Stack 10.10 deepens Workflow Distribution and durable availability with the
paired FleetDB lifecycle. It must prove transition fault injection, restart
reconciliation, digest drift, approval and activation denial,
active-predecessor preservation, and retirement of the old lifecycle paths.
