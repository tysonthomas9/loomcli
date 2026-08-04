# AgentService as a DriverVersion Controller

> **Status:** Aspirational — proposed 2026-06-07, not implemented as of
> 2026-07-23. `domain.AgentService` (`internal/domain/platform.go:147-168`) has no
> `driver_id` and no `driver_version_id`, and there is no AgentService controller:
> the only implementation is a CRUD store over fleet-db
> (`internal/infra/fleetdb/agent_service.go:14-136`), and
> `internal/cli/serve/serve_loops.go` starts a driver executor, task workers,
> stale/await/delivery sweepers, an outbox dispatcher and an issue bridge — no
> service reconcile loop.
> **Date:** 2026-06-07

Since this was written the model moved without the doc: `AgentServiceKind` gained
`always_on`, `cron`, `event`, and `campaign_orchestrator`
(`internal/domain/platform.go:133-136`), and `TriggerBinding` gained
`target_agent_service_id` (`:230`). AgentService is currently being shaped by the
trigger/schedule work, not by this proposal — re-check this design against that
work before implementing it.

## Summary

AgentService should not store or execute TypeScript source directly. It should describe a long-running desired-state service and reference an immutable executable program through `driver_id` and `driver_version_id`.

This keeps code ownership, provenance, rollback, and execution semantics in the Driver/DriverVersion platform while AgentService handles service lifecycle.

## Proposed Shape

```yaml
AgentService:
  desired_state: running
  service_id: lead-agent
  driver_id: lead-agent
  driver_version_id: drvver_abc123
```

## Meaning

- `driver_id` identifies the registered TS agent/workflow program family.
- `driver_version_id` pins the exact immutable build artifact to run.
- `desired_state` tells the controller whether the service should be running, paused, or stopped.
- `service_id` is the stable service identity used for leases, status, logs, and UI.

AgentService becomes a controller target, not a code container.

## Runtime Model

1. A user or agent registers TypeScript as a DriverVersion.
2. A user or agent creates or updates an AgentService to point at that DriverVersion.
3. An AgentService controller watches services with `desired_state=running`.
4. The controller acquires a fenced lease for the service.
5. The controller starts or maintains the pinned DriverVersion as the service runtime.
6. If the runtime exits unexpectedly, restart behavior follows the service restart policy.

## Why This Direction

- DriverVersion stays the single source of executable TS artifacts.
- AgentService can roll forward or back by changing only `driver_version_id`.
- Service lifecycle is separated from build/package identity.
- The same DriverVersion can support both one-shot DriverRuns and long-running services.
- Auditing remains clear: "what code ran" is answered by DriverVersion, while "why was it running" is answered by AgentService.

## Deferred

- No AgentService controller in the minimum workflow runner.
- No service-level TypeScript upload endpoint.
- No dynamic package install or sandbox policy in this proposal.
- No trigger, schedule, or GitHub binding behavior until AgentService is needed as an always-on runtime.

## Open Implementation Notes

- Add `driver_id` and `driver_version_id` to the AgentService model when this feature is implemented.
- Validate that `driver_version_id` belongs to `driver_id`.
- Reject `desired_state=running` if the referenced DriverVersion is not validation-passed.
- Keep DriverRun as the one-shot execution record; introduce a separate service runtime/session record only when long-running AgentService execution is implemented.

## Related

- [`2026-06-07-slack-agent-service-proposal.md`](2026-06-07-slack-agent-service-proposal.md)
- [`2026-06-07-trigger-workflow-proposal.md`](2026-06-07-trigger-workflow-proposal.md)
- [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md)
  — the entity catalog AgentService belongs to.
- [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) —
  how a DriverVersion actually runs today.
