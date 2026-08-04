# Agent Execution Product Docs

> **Status:** Current index · *audited 2026-07-23*. Per-doc status is in the
> table below; there is no folder-wide status.

**Last updated:** 2026-07-23

This folder captures the product plan for making agent execution visible,
controllable, and debuggable across local and cloud/containerized runs.

Read [`docs/loom-glossary.md`](../loom-glossary.md) first — this repo
overloads ordinary words (loom, flue, fleet, aether, codex, claude, lead,
stack), and every doc here assumes the loom-specific senses.

## Reference (read these before the specs)

These three are descriptive: they say what the code does today, with
`file:line` citations. They are the authority when a spec disagrees with them.

| # | Doc | What it is for | Status |
|---|---|---|---|
| 1 | [`session-stores.md`](session-stores.md) | Disambiguates the two records both called "session": the filesystem store and the fleet-db control-plane record. Includes the writer table. | Current |
| 2 | [`agent-lifecycle-state-machine.md`](agent-lifecycle-state-machine.md) | Canonical agent, session, and task state vocabularies plus stop reasons. Canonical where it and another doc disagree — but not exhaustive: the daemon's own `running`/`blocked`/`failed`/`stopped` vocabulary is derived in the supervisor and documented inline there. | Current |
| 3 | [`error-class-reference.md`](error-class-reference.md) | Canonical failure vocabularies: `agenterr.Outcome`, `supervisor.StopReason`, and the persisted `error_class` strings. The "error-class registry" that `internal/runtimepreflight/preflight.go:92` refers to. | Current |

## Specs

Read in this order.

| # | Doc | What it is for | Status |
|---|---|---|---|
| 4 | [`agent-execution-prd.md`](agent-execution-prd.md) | Top-level PRD for agent execution visibility: MVP scope, functional requirements, four-phase rollout. | Partially shipped — the run-record model was superseded by `domain.AgentSession` |
| 5 | [`local-mode-product-spec.md`](local-mode-product-spec.md) | The first shippable slice: supervised agents on one machine, still using fleet-db as the shared control plane. | Largely implemented |
| 6 | [`desktop-app-runtime-spec.md`](desktop-app-runtime-spec.md) | How the macOS Tauri app, LaunchAgent background service, bundled CLI/runtime, updates, and multi-window workspaces fit together. | Partially implemented |
| 7 | [`desktop-installation-runbook.md`](desktop-installation-runbook.md) | How to build, install, verify, troubleshoot, and update the macOS Tauri app. | Current — runbook, follow it |
| 8 | [`agent-run-ux-spec.md`](agent-run-ux-spec.md) | What the UI shows for agent execution: sidebar, task cards, timeline, Sessions tab, logs, diffs. | Partially shipped — run detail panel, worker-history view, and cleanup UI are not built |
| 9 | [`orchestrator-worker-model.md`](orchestrator-worker-model.md) | The largest doc here: orchestrator/worker roles, ephemeral-worker lifecycle, and the future workflow runtime. | Partially shipped — phase 1 only (see its banner) |
| 10 | [`lead-agent-epic-runner-spec.md`](lead-agent-epic-runner-spec.md) | How lead agents run one epic at a time, with scoped workers, single-task ephemeral attempts, and worker history. Canonical for the lead↔epic product rules. | Partially shipped — rules enforced by the epic-runner Flue workflow, not the deleted Go runner |
| 11 | [`session-artifact-contract.md`](session-artifact-contract.md) | The evidence contract: what metadata, transcript, logs, usage, diff, commit, test, and cleanup data every run must leave behind. Canonical for the data contract. | Partially shipped — cleanup metadata is aspirational |
| 12 | [`daemon-agent-runtime-architecture.md`](daemon-agent-runtime-architecture.md) | How the daemon, agent runner, local mode, cloud mode, ownership leases, and decentralized task claiming fit together. | Partially implemented |
| 13 | [`failure-modes-recovery-ux.md`](failure-modes-recovery-ux.md) | Failure modes mapped to real error classes, messages, and recovery actions. | Partially shipped — matrix rebuilt against code; the UX mockups are proposals |
| 14 | [`agent-messaging-and-backpressure.md`](agent-messaging-and-backpressure.md) | `internal/agentinbox`, `internal/notify`, and the auto-mode rate-limit breaker — and why `internal/circuitbreaker` is none of them. | Current |
| 15 | [`container-runner-mvp-spec.md`](container-runner-mvp-spec.md) | Podman/container runner behavior after local mode is reliable. | **Aspirational** — proposed 2026-05-04, not implemented |
| 16 | [`dogfood-agent-execution-test-plan.md`](dogfood-agent-execution-test-plan.md) | End-to-end dogfood scenarios used to validate the product. | Draft header; not audited in this pass |
| 17 | [`web-onboarding-spec.md`](web-onboarding-spec.md) | Proposed server-driven six-step onboarding flow with repo wizard and backend workbench. | **Aspirational** — not built; a client-computed flow shipped instead |
| 18 | [`pr-review-spec.md`](pr-review-spec.md) | PRs as a first-class object: review queue, PR page, and the persisted PR review agent. | Partially shipped — §2, §3, §5, §6 superseded |
| 19 | [`loom-typescript-sdk-spec.md`](loom-typescript-sdk-spec.md) | Pointer to the SDK's own authoritative docs (`sdk/README.md`). | Pointer |

## Related design docs

Full index with per-doc status: [`../design/README.md`](../design/README.md).
The mechanism behind the lead/epic-runner specs above is
[`../design/epic-runner-workflow-architecture.md`](../design/epic-runner-workflow-architecture.md)
and [`../design/lead-runtime-delivery.md`](../design/lead-runtime-delivery.md).

- [`../design/agent-run-visibility-plan.md`](../design/agent-run-visibility-plan.md)
- [`../design/distributed-control-plane.md`](../design/distributed-control-plane.md)
- [`../design/distributed-control-plane-data-model.md`](../design/distributed-control-plane-data-model.md)

## Related runbooks

- [`../testing/local-mode-podman-e2e.md`](../testing/local-mode-podman-e2e.md)

## Conventions

- Status banners use the greppable form
  `> **Status:** <Current|Partially implemented|Aspirational|Superseded> · *audited YYYY-MM-DD*`.
- Row 16 is the only doc in this folder that still carries the original
  `> **Status:** Draft` header. Its claims have not been checked against code;
  treat it accordingly. Every other doc listed above was audited and carries a
  dated banner — the Status column here restates that banner, and the banner in
  the doc wins if they ever diverge.
- Every claim about behavior should carry a `path/file.go:123` citation. If it
  does not, verify before relying on it.
