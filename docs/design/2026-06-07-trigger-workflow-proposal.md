# Trigger-Driven Driver Workflows Proposal

> **Status:** Implemented — historical. *Audited 2026-07-24.*

Read this for the **design rationale** — why the trigger layer persists before
dispatch and never executes TypeScript inside the webhook handler. Do not read
it as a work plan: the "Missing" and "Deferred" lists are both out of date.

The webhook handler still holds the line this doc drew. `dispatchWebhook`'s own
comment says it "only persists + enqueues — it never executes work inline"
(`internal/webui/handlers/webhooks/module.go:120-123`).

## As built (2026-07-24)

Five of the seven items under "Missing" below have shipped. The two that have
not are called out after the list.

- **Webhook adapter route.** `POST /api/workspaces/{ws}/webhooks/{name}`
  (`internal/webui/handlers/webhooks/module.go:45`) — generic over adapters,
  with `github` registered as one
  (`internal/webui/handlers/webhooks/github.go:20`). The doc's literal
  `/webhooks/github` is that route with `name=github`.
- **Signature verification.** `X-GitHub-Event` / `X-GitHub-Delivery` /
  `X-Hub-Signature-256` constants at
  `internal/webui/handlers/webhooks/github.go:23-25`; HMAC-SHA256 verify at
  `github.go:171-193` (same file). Ordering matches the proposal: normalize → require a
  delivery id → resolve the enabled binding → verify → dispatch
  (`module.go:71-89`). A missing delivery id is a `400` before persistence
  (`module.go:79-82`), and a disabled binding is rejected without dispatch
  (`module.go:109-112`).
- **Dedupe keyed by delivery id.** The idempotency key is
  `name + ":" + event.DeliveryID` — i.e. `github:{delivery_id}` exactly as
  proposed (`module.go:131`).
- **Route-key mapping.** `githubRouteKey(event, action)`
  (`internal/webui/handlers/webhooks/github.go`), plus
  glob `event_type_patterns` on the binding
  (`internal/domain/platform.go:225`, `internal/trigger/pattern.go`).
- **Read API for events and deliveries.** `GET /api/workspaces/{ws}/trigger-events`,
  `.../trigger-events/{eventId}`, `.../trigger-deliveries`,
  `.../trigger-deliveries/{deliveryId}` (`module.go:46-49`). Read-only, and
  scoped to events/deliveries — not bindings; see the two gaps below.
- **Dispatch client.** `DispatchTriggerRouteV2`
  (`internal/infra/fleetdb/trigger_route.go:40`) posts
  `POST /api/v1/{ws}/trigger-routes/{route_key}` and decodes a fan-out
  `{"deliveries":[...]}` response.
- **E2E.** `TestE2E_GitHubWebhookDispatchesDriverRunWithEphemeralStack` and
  `TestE2E_GitHubWebhookRunsDriverAgainstLiveGitHubPR`
  (`internal/webui/handlers/webhooks/webhooks_e2e_test.go:33,55`). The PR-review workflow is
  `internal/workflows/builtin/github-review-agent.ts`.

Two "Missing" items did **not** ship as written:

- **"UI/API for managing trigger bindings."** No REST route and no web UI
  manages bindings — the only binding-management surface is the CLI,
  `loom trigger bindings create|update|list|show`
  (`internal/cli/trigger/trigger_cmd.go:33,60,67,74,81`), which writes through
  the store directly. "UI for editing trigger bindings" is also still on the
  Deferred list, consistently.
- **"Replay and redelivery tooling for persisted TriggerEvents."** See the
  UNVERIFIED note below; no replay CLI or API surface was located.

Two items the doc lists under **Deferred** shipped, plus one restriction from
the Binding-shape section that was dropped rather than kept:

- **Fan-out beyond one delivery per binding.** `DispatchTriggerRouteV2` returns
  a delivery list; coverage is `TestRouterE2EWebhookFanOutTraceAndOrigin`
  (`internal/webui/handlers/webhooks/webhooks_router_e2e_test.go:201`).
- **Schedule runner.** `internal/trigger/cron.go` fires synthetic `cron.tick`
  events for `source_kind=cron` bindings into the same dispatch path;
  `TriggerBinding.Schedule` is a 5-field cron expression
  (`internal/domain/platform.go:250-253`). Coverage:
  `TestRouterE2ECronTickDispatchAndReplicaDedup`
  (`internal/webui/handlers/webhooks/webhooks_router_e2e_test.go:504`).
- **`concurrency_policy`.** Five policies exist — `allow`, `forbid`, `replace`,
  `queue`, `one_active_per_epic` (`internal/domain/platform.go:204-211`), with
  a `SubjectKeyTemplate` for the concurrency subject key
  (`platform.go:237-241`). Note this is **broader** than the "one active run per
  idempotency key for GitHub v1" the Binding-shape section proposes; that
  restriction was not kept. Coverage: `TestRouterE2EReplaceSupersedeStorm` and
  `TestRouterE2EForbidRejectsAndQueuePromotesViaSweeper`
  (`internal/webui/handlers/webhooks/webhooks_router_e2e_test.go:347,420`).

Later additions this doc does not anticipate: per-source connector secrets with
a dual-secret rotation window replaced the per-binding
`webhook_secret` as the primary signature source
(`module.go:96-102`, `internal/webui/handlers/webhooks/secret_resolve.go`);
delivery retry with a sweeper (`RetryMaxAttempts` / `RetryBackoffSeconds`,
`platform.go:245-249`, `internal/trigger/delivery_sweeper.go`); an internal
event source and issue-journal bridge (`internal/trigger/internal_source.go`,
`internal/trigger/issue_journal_bridge.go`); and await dispatch
(`internal/trigger/await_matcher.go`).

**Not verified in this audit:** the replay path. `ReplayOfEventID` is carried on
the dispatch wire (`internal/infra/fleetdb/trigger_route.go:52`), but whether a
replay CLI/API exists and preserves the audit distinction the plan requires was
not established.

## Summary

Use `TriggerBinding`, `TriggerEvent`, and `TriggerDelivery` as the durable event-ingestion layer for GitHub webhooks and similar external invocation surfaces. The trigger layer should not execute TypeScript directly. It should verify and persist the incoming event, resolve one or more pinned driver versions, create auditable delivery records, and enqueue `DriverRun` records that the existing Loom driver executor can claim and run.

The minimum driver platform remains `DriverVersion -> DriverRun -> worker claim/lease -> Flue TS execution -> TaskRun -> ActionLedger close_task`. Triggers sit in front of that platform when the source is an external event rather than a direct workflow POST.

## Current State

Already exists:

- fleet-db models, storage, and API routes for `TriggerBinding`, `TriggerEvent`, `TriggerDelivery`, `Driver`, `DriverVersion`, and `DriverRun`.
- `POST /api/v1/{workspace}/trigger-routes/{route_key}` can look up a binding and create a queued `DriverRun`.
- Driver runs already pin a `DriverVersion`, keep payload, and emit lifecycle events.
- Loom can register a Flue `.ts` workflow as a DriverVersion and execute queued DriverRuns through the driver executor.

Missing:

- GitHub-specific webhook adapter route.
- GitHub signature verification with `X-Hub-Signature-256`.
- Dedupe keyed by `X-GitHub-Delivery`.
- Mapping from GitHub event/action/repository to route keys.
- Replay and redelivery tooling for persisted TriggerEvents.
- UI/API for managing trigger bindings.
- End-to-end tests where a signed GitHub pull request webhook drives a real PR review `.ts` workflow.

## Proposed Flow

GitHub webhook flow:

1. GitHub sends `POST /api/workspaces/{ws}/webhooks/github`.
2. The adapter verifies the webhook signature using the workspace GitHub webhook secret.
3. The adapter derives a route key, for example `github.pull_request.opened`, `github.issue_comment.created`, or `github.push`.
4. fleet-db persists a `TriggerEvent` before dispatch:
   - source kind: `github`
   - source ref: repository or installation
   - event type: GitHub event name
   - subject ref: repository, PR, issue, or branch
   - idempotency key: `github:{delivery_id}`
   - raw payload digest/ref
5. fleet-db finds enabled `TriggerBinding` records for the route key.
6. For each binding, fleet-db creates a `TriggerDelivery`.
7. Each delivery creates or replays a queued `DriverRun` pinned to the binding's `driver_version_id`.
8. Loom's driver executor claims the DriverRun with lease/fencing, runs the `.ts` workflow, and finishes the run.

Similar sources use the same shape:

- Schedule: `schedule.daily-report.fire`
- Slack command: `slack.command.run-epic`
- Generic webhook: `webhook.customer-alert.received`
- Manual replay: `replay.trigger-event`

## Public Interfaces

Keep direct workflow invocation:

- `POST /api/workspaces/{ws}/workflows/{name}` remains the simple manual/API path.

Add external-event invocation:

- `POST /api/workspaces/{ws}/webhooks/github`
  - verifies GitHub signature.
  - persists a TriggerEvent.
  - dispatches matching TriggerDeliveries to DriverRuns.
  - returns accepted deliveries and DriverRun IDs.

Use existing fleet-db trigger route for lower-level tests and generic integrations:

- `POST /api/v1/{workspace}/trigger-routes/{route_key}`
  - accepts a normalized trigger payload.
  - useful for non-GitHub adapters and replay.

Binding shape:

- `route_key`: normalized route, such as `github.pull_request.opened` or `github.pull_request.synchronize`.
- `driver_id` and `driver_version_id`: pinned immutable TS workflow.
- `target_entrypoint`: default `run`.
- `enabled`: false prevents dispatch while preserving history.
- `concurrency_policy`: start with existing policies, but use only one active run per idempotency key for GitHub v1.

## Implementation Plan

1. Add a GitHub webhook adapter.
   - Route: `POST /api/workspaces/{ws}/webhooks/github`.
   - Read headers: `X-GitHub-Event`, `X-GitHub-Delivery`, `X-Hub-Signature-256`.
   - Verify HMAC SHA-256 against workspace secret.
   - Reject missing/invalid signatures before persistence.

2. Normalize the trigger event.
   - Parse GitHub event name and payload action.
   - Build route key as `github.{event}.{action}` when action exists, otherwise `github.{event}`.
   - Set idempotency key to `github:{delivery_id}`.
   - Store raw payload digest and pass the JSON payload into the DriverRun payload.

3. Dispatch through TriggerBinding/Event/Delivery.
   - Persist TriggerEvent first.
   - Resolve matching enabled TriggerBindings.
   - Create one TriggerDelivery per binding.
   - Create queued DriverRun for each delivery using the binding's pinned DriverVersion.
   - Store delivery status as dispatched or failed with error class.

4. Execute with existing DriverRun worker path.
   - Do not run TS inside the webhook handler.
   - Rely on the existing Loom executor to claim, heartbeat, verify bundle digest, and execute Flue.
   - The TS workflow receives the GitHub payload as `input`.

5. Add basic management and replay.
   - CLI/API can list TriggerEvents and TriggerDeliveries.
   - Replay creates new deliveries from an existing TriggerEvent without accepting a new webhook.
   - Replays must preserve a replay reference so audit can distinguish original delivery from replay.

## Testing

Unit tests:

- GitHub signature verification accepts valid payload/signature and rejects invalid signatures.
- Route key derivation covers events with action, events without action, and malformed payloads.
- Duplicate `X-GitHub-Delivery` replays the existing TriggerEvent instead of creating duplicates.
- Disabled TriggerBinding records do not create DriverRuns.

Integration tests:

- Register a Flue `.ts` workflow as a DriverVersion.
- Create a TriggerBinding for `github.pull_request.opened`.
- POST a signed GitHub pull request payload to the webhook route.
- Assert TriggerEvent is persisted before dispatch.
- Assert TriggerDelivery is created and linked to the queued DriverRun.
- Start the Loom executor and assert the DriverRun completes.
- Assert GET run/events/stream show the run lifecycle.

E2E test:

- Seed a local test Git repository with a base branch, a feature branch, and a GitHub pull request payload that references the PR number, head SHA, base SHA, repository, and installation/sender metadata.
- Register a Flue `.ts` workflow named `github-pr-review` as a DriverVersion.
- Create a TriggerBinding for `github.pull_request.opened` pinned to that DriverVersion.
- Send a signed GitHub `pull_request.opened` webhook payload to the GitHub webhook route.
- Assert the webhook persists one TriggerEvent, creates one TriggerDelivery, and enqueues one DriverRun with the original GitHub payload.
- Start the Loom executor and assert the DriverRun completes after the TS workflow inspects the PR diff and records a review result.
- Assert GET run/events/stream show the run lifecycle and include enough output to identify the PR number and reviewed commit.
- Resend the same `X-GitHub-Delivery` ID and assert no duplicate DriverRun, TriggerDelivery side effect, or duplicate review result is produced.

## Deferred

- Real GitHub App installation authentication and token minting; the first E2E can use a local repo and a fake review sink.
- UI for editing trigger bindings.
- Fanout to multiple workflows beyond one delivery per matching binding.
- Schedule runner implementation.
- Slack adapter.
- Full DriverStep graph generation.
- WorkerProfile-based routing for trigger-created runs.

## Acceptance Criteria

- A signed GitHub pull request webhook can enqueue a DriverRun for a pinned PR review `.ts` workflow.
- The webhook handler is durable and non-blocking: persistence happens before dispatch, and execution happens asynchronously.
- Duplicate GitHub deliveries do not create duplicate effects.
- The resulting run is inspectable with existing run GET/events/stream endpoints.
- The first implementation does not require TriggerBinding/Event/Delivery for direct `POST /workflows/{name}` calls.

## Related

- [`../loom-glossary.md`](../loom-glossary.md) — trigger binding, connector,
  await, driver run
- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  what the enqueued `DriverRun` executes
- [`2026-06-07-slack-agent-service-proposal.md`](2026-06-07-slack-agent-service-proposal.md)
  — the sibling Slack ingestion proposal, which went a different way
- [`2026-06-18-stack-aware-pr-publisher.md`](2026-06-18-stack-aware-pr-publisher.md)
  — the PR side of the GitHub loop
