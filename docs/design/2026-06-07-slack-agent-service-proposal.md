# Slack AgentService Proposal

> **Status:** Aspirational — proposed 2026-06-07, not implemented as of
> 2026-07-24. A different Slack path shipped instead.

The **inbound** half of this proposal does not exist. The **outbound** half was
solved by a different mechanism (connectors). Before implementing from this
doc, reconcile it against the connector design; the two paths overlap and this
one has not been re-decided.

## What is not built

The "Missing" list below is still substantially accurate:

- `domain.AgentService` has **no** `driver_id` / `driver_version_id` fields
  (`internal/domain/platform.go:147-168`), so nothing pins executable code to a
  service. Implementation-plan step 1 is untouched.
- There is **no AgentService controller**. The only non-store code that touches
  `AgentServices()` is CRUD — `loom serve worker service add|list|show|set|rm`
  (`internal/cli/serve/worker/service_cmd.go:166,207,233,252,267,277` — the
  only `AgentServices()` call sites outside the store layer) and a tracing wrapper
  (`internal/cli/cmdstore/store_tracing_platform.go:134`). Nothing claims a
  lease, starts a long-lived runtime, or heartbeats one. Step 2 is untouched.
- **No Socket Mode anywhere.** Grep finds no socket-mode client, no Slack event
  ingestion, no Slack normalized-payload type, and no Slack idempotency-key
  builder. The only registered webhook adapter is `github`
  (`internal/webui/handlers/webhooks/adapter.go:77-79`,
  `internal/webui/handlers/webhooks/github.go`). Steps 3 and 5 are untouched.
- The `slack_post_message` / `slack_update_message` / `slack_add_reaction`
  action-ledger types of step 4 **do not exist**.

`always_on` is a real `AgentServiceKind`
(`internal/domain/platform.go:133`), so the YAML shape above is at least
type-valid — but `driver_id`, `driver_version_id`, and `metadata.mode` are not
fields the model has (`metadata` is a free `map[string]string`,
`platform.go:165`).

## What shipped instead

**Slack as a connector provider**, for *outbound* egress only:
`internal/connector/providers/slack.go` wraps `chat.postMessage`
(`slack.go:131`) and `conversations.history` (`slack.go:190`) behind the
connector grant/dispatch layer.

Two corrections to note when reading the proposal against that code:

- The action constants are `slack.chat.post` and `slack.conversations.read`
  (`internal/connector/providers/slack.go:29,32`) — **not**
  `slack.chat.post_message`. (`domain.ConnectorGrant.Action`'s doc comment uses
  `slack.chat.post_message` as its example, `internal/domain/connector.go:159`;
  that example string does not match any registered action.)
- The "Slack replies are idempotent" acceptance criterion is met, but by
  connector idempotency rather than an ActionLedger: `chat.postMessage` requires
  an idempotency key and derives Slack's `client_msg_id` from it, so a retry
  reuses the message identity instead of minting a new one
  (`slack.go:20-28,126,151,334-338`).

Adjacent risk the proposal did not consider, now documented in the code: the
pre-egress `conversations.info` freshness check has an accepted TOCTOU window
(`slack.go:22-26`).

## Summary

Use `AgentService` for a long-lived Slack agent runtime. The Slack agent should be registered as a `DriverVersion`, then run by an `AgentService` controller as a desired-state service that maintains the Slack connection, receives events, and dispatches durable work.

This is different from direct workflow invocation. Slack needs a process that can stay connected, acknowledge events quickly, dedupe retries, and keep conversation context. AgentService owns that long-lived lifecycle; DriverRun and TaskRun remain the auditable execution records for meaningful work started from Slack.

## Current State

Already exists:

- Driver and DriverVersion primitives for immutable TS programs.
- DriverRun queue, claim, heartbeat, finish, payload, and run events.
- TaskRun for fleet-db task execution.
- ActionLedger for idempotent side effects such as `close_task`.
- AgentService model/storage/API as a desired-state concept.

Missing:

- AgentService controller that claims and starts long-lived services.
- AgentService fields that pin `driver_id` and `driver_version_id`.
- Slack-specific runtime adapter or SDK helpers.
- Slack credential storage/secret references.
- Slack event dedupe and durable event record.
- ActionLedger support for Slack side effects such as `post_message`, `update_message`, and `add_reaction`.
- End-to-end test with a fake Slack event source and fake Slack Web API sink.

## Proposed Shape

```yaml
AgentService:
  service_id: slack-agent
  name: Slack Agent
  kind: always_on
  desired_state: running
  driver_id: slack-agent
  driver_version_id: drvver_slack_agent_1
  max_instances: 1
  metadata:
    slack_team_id: T123
    mode: socket
```

Sensitive Slack tokens must not live in metadata. Use secret references or process environment until a workspace secret store exists.

## Runtime Flow

1. A user or agent registers a TypeScript Slack agent as a DriverVersion.
2. A user or agent creates an AgentService that points at the pinned DriverVersion.
3. The AgentService controller watches services with `desired_state=running`.
4. The controller claims a fenced lease for `service_id=slack-agent`.
5. The controller starts the pinned DriverVersion as a long-lived Flue/Node service.
6. The service connects to Slack Socket Mode using configured credentials.
7. When Slack sends an event, the service acknowledges it quickly, normalizes it, and dedupes by Slack envelope/event ID.
8. For meaningful work, the service creates a DriverRun with the Slack payload and conversation metadata.
9. The DriverRun may create TaskRuns for fleet-db task execution.
10. Slack replies, message updates, and reactions are recorded through ActionLedger so retries do not double-post.

## Slack Event Model

First version should target Slack Socket Mode because it fits a long-lived AgentService:

- `app_mention`: ask the agent to answer, inspect a repo/task, or start a workflow.
- `message.im`: direct-message agent interaction.
- `slash_command`: explicit command such as `/loom review-pr`.
- `interactive`: button/menu callbacks for approve, retry, cancel, or assign.

Each incoming Slack event should produce a normalized payload:

```json
{
  "source": "slack",
  "teamId": "T123",
  "channelId": "C123",
  "userId": "U123",
  "eventType": "app_mention",
  "eventTs": "1710000000.000100",
  "threadTs": "1710000000.000100",
  "text": "@loom review PR 42",
  "raw": {}
}
```

Use idempotency keys shaped like:

- `slack:event:{team_id}:{event_id}`
- `slack:reply:{team_id}:{channel_id}:{thread_ts}:{driver_run_id}`
- `slack:reaction:{team_id}:{channel_id}:{event_ts}:{reaction}`

## DriverRun and ActionLedger Use

AgentService should not hide work inside an opaque long-running process. For each Slack request that performs non-trivial work:

- Create a DriverRun with source kind `slack`.
- Store the normalized Slack payload as DriverRun payload.
- Include Slack identifiers in DriverRun output for observability.
- Record Slack side effects in ActionLedger before applying them.
- Replay existing ActionLedger records on retry rather than sending duplicate Slack messages.

This keeps the system debuggable:

- AgentService answers "why is the Slack bot running?"
- DriverVersion answers "what code is the Slack bot running?"
- DriverRun answers "what Slack request was processed?"
- ActionLedger answers "which Slack messages/reactions were sent?"

## Implementation Plan

1. Extend AgentService to pin executable code.
   - Add `driver_id` and `driver_version_id`.
   - Validate the version belongs to the driver.
   - Require validation-passed DriverVersion for `desired_state=running`.

2. Add an AgentService controller.
   - Poll/watch desired running services.
   - Acquire a fenced lease per service.
   - Start the pinned DriverVersion as a long-lived runtime.
   - Heartbeat while the process is alive.
   - Restart according to restart policy.
   - Reject stale lease/fencing mutations.

3. Add Slack service runtime support.
   - Pass service identity, lease, workspace, and Slack credential refs to the TS process.
   - Provide a small SDK helper for Slack event normalization and DriverRun creation.
   - Keep the Slack Web API client injectable so tests can use a fake sink.

4. Add ActionLedger side-effect types.
   - `slack_post_message`
   - `slack_update_message`
   - `slack_add_reaction`
   - Use deterministic idempotency keys for every Slack side effect.

5. Keep external HTTP Slack Events API deferred.
   - Socket Mode is the first long-lived AgentService target.
   - HTTP Events API can later use TriggerBinding/Event/Delivery like the GitHub webhook proposal.

## Testing

Unit tests:

- AgentService rejects running state for missing or non-passed DriverVersion.
- Controller acquires one fenced service lease and rejects stale owner updates.
- Slack event normalization builds stable payloads and idempotency keys.
- Duplicate Slack event IDs do not create duplicate DriverRuns.
- Duplicate reply attempts replay ActionLedger instead of posting twice.

Integration tests:

- Register a `slack-agent` Flue TS DriverVersion.
- Create an AgentService with `desired_state=running`.
- Start the AgentService controller with a fake Slack Socket Mode server.
- Send an `app_mention` event.
- Assert the service acknowledges the event and creates one DriverRun.
- Assert the DriverRun completes and records a Slack reply ActionLedger entry.
- Send the same Slack event again and assert no duplicate DriverRun or reply.

E2E test:

- Start embedded fleet-db and Loom with AgentService controller enabled.
- Seed a local repo and a fleet-db task.
- Start a fake Slack Socket Mode/Web API server.
- Register `slack-agent` as a DriverVersion.
- Create the `slack-agent` AgentService.
- Send `@loom review task FLEET-1` in a fake Slack channel.
- Assert the long-lived service receives the event, creates a DriverRun, uses TaskRun if it executes the fleet-db task, and sends one Slack reply through the fake Web API.
- Restart the controller during processing and assert lease/fencing prevents stale duplicate replies.

## Deferred

- Real Slack app installation OAuth.
- Workspace-level secret store.
- HTTP Slack Events API adapter.
- UI for creating Slack AgentServices.
- Rich Slack block interaction flows.
- Multi-team sharding and capacity policy.
- WorkerProfile-based placement for Slack-created DriverRuns.

## Acceptance Criteria

- A registered TypeScript Slack agent can run as a long-lived AgentService.
- The service keeps a Slack Socket Mode connection alive under controller lease.
- Slack events create durable DriverRuns for meaningful work.
- Slack replies are idempotent through ActionLedger.
- Duplicate Slack retries and controller restarts do not duplicate work or messages.
- Direct workflow invocation and GitHub trigger workflows remain independent of this Slack AgentService path.

## Related

- [`2026-06-07-trigger-workflow-proposal.md`](2026-06-07-trigger-workflow-proposal.md)
  — the sibling ingestion proposal, which *was* built. Step 5 of this doc
  ("HTTP Events API can later use TriggerBinding/Event/Delivery like the GitHub
  webhook proposal") is now the cheaper path: adding a `slack` adapter next to
  `github` would reuse the whole durable dispatch layer and needs no
  AgentService controller.
- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  the `DriverRun` / `TaskRun` lifecycle this proposal builds on, and
  `loom.connectors.*` as the egress surface
- [`../loom-glossary.md`](../loom-glossary.md) — connector, driver version,
  trigger binding
