# Trigger-Driven Driver Workflows Proposal

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
- End-to-end tests where a GitHub-shaped webhook drives a real `.ts` workflow.

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

- `route_key`: normalized route, such as `github.pull_request.opened`.
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

- Seed a test repo/workspace with an epic and two fleet-db tasks.
- Send a signed GitHub-shaped webhook payload that triggers the epic runner.
- Assert the TS workflow receives the payload, creates TaskRuns, completes the tasks, and records `close_task` through ActionLedger.
- Resend the same GitHub delivery ID and assert no duplicate DriverRun or duplicate close_task effect is produced.

## Deferred

- GitHub App installation authentication and token minting.
- UI for editing trigger bindings.
- Fanout to multiple workflows beyond one delivery per matching binding.
- Schedule runner implementation.
- Slack adapter.
- Full DriverStep graph generation.
- WorkerProfile-based routing for trigger-created runs.

## Acceptance Criteria

- A signed GitHub webhook can enqueue a DriverRun for a pinned `.ts` workflow.
- The webhook handler is durable and non-blocking: persistence happens before dispatch, and execution happens asynchronously.
- Duplicate GitHub deliveries do not create duplicate effects.
- The resulting run is inspectable with existing run GET/events/stream endpoints.
- The first implementation does not require TriggerBinding/Event/Delivery for direct `POST /workflows/{name}` calls.
