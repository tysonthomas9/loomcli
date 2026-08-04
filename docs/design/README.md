# Design Docs

> **Status:** Current index · *audited 2026-08-03* (written 2026-07-23). Per-doc
> status is in the tables below and in each doc's own banner; there is no
> folder-wide status.

This folder is the ADR equivalent for this repo — see
[`../agents/domain.md`](../agents/domain.md). Dated filenames are decision
records; undated ones are living subsystem designs. Nothing here is
automatically true: **read the banner at the top of a doc before believing it.**

Read [`../loom-glossary.md`](../loom-glossary.md) first. Every doc here assumes
the loom-specific senses of `loom`, `flue`, `fleet`, `daytona`, `driver`,
`lead`, `stack`, and `worker`.

## Status vocabulary

Banners use the greppable form
`> **Status:** <value> · *audited YYYY-MM-DD*`.

| Value | Means |
|---|---|
| Current | Verified against the tree on the audit date. |
| Implemented / Shipped | The change described landed; the doc is a record of it. |
| Partially implemented | Some of it shipped; the doc says which parts inline. |
| Aspirational / Future plan / Proposed | Not built. Do not read as behaviour. |
| Superseded | A named successor exists. Historical only. |
| Pointer | The content lives elsewhere; this file is a redirect. |
| *(no banner)* | Never audited. Treat every claim as unverified. |

## Control plane and the FleetDB platform

Start with the as-built map. The rest of this cluster describes either a
pre-2026-05 past or an unbuilt future.

| Doc | Purpose | Status |
|---|---|---|
| [`2026-07-23-control-plane-as-built.md`](2026-07-23-control-plane-as-built.md) | Where the distributed control plane actually lives in this repo, with citations. **Read this before the other four.** | Current |
| [`distributed-control-plane.md`](distributed-control-plane.md) | The original architecture. The conceptual model still holds; the data model and phase plan were superseded 2026-06-03. | Partially implemented |
| [`distributed-control-plane-data-model.md`](distributed-control-plane-data-model.md) | Data-model review that the V2 proposal replaced. | Superseded |
| [`fleetdb-agent-platform-v2-proposal.md`](fleetdb-agent-platform-v2-proposal.md) | The 2026-06-03 vision for the FleetDB-backed agent platform. Its entity model largely shipped (`internal/domain/platform.go`). | Historical vision, partially implemented |
| [`fleetdb-agent-platform-v2-phased-delivery.md`](fleetdb-agent-platform-v2-phased-delivery.md) | Phase plan for the above, with a per-phase shipped/not table inside. | Partially implemented |
| [`fleetdb-agent-platform-v2-execution-topology-addendum.md`](fleetdb-agent-platform-v2-execution-topology-addendum.md) | Execution-topology remediation steps for the V2 platform. | Partially implemented |
| [`fleet-http-connection-reuse.md`](fleet-http-connection-reuse.md) | Diagnosis and fix for fleet-db redis-pool exhaustion under loom-fleet load. | Implemented |

## Drivers, workflows, and Flue

| Doc | Purpose | Status |
|---|---|---|
| [`workflow-driver-authoring-guide.md`](workflow-driver-authoring-guide.md) | How a registered TypeScript workflow runs through Loom, FleetDB, and Flue. **The current platform contract — read this first in this cluster.** | Current |
| [`driver-op-http-api.md`](driver-op-http-api.md) | The driver-op HTTP surface: a running workflow bundle's entire control plane against `loom serve`. Prose companion to `sdk/api-surface.v1.json`. | Current |
| [`native-flue-driver-integration.md`](native-flue-driver-integration.md) | Why a driver is a normal Flue-authored TypeScript project rather than something Loom generates. | Current |
| [`taskrun-queue-and-worker-pool.md`](taskrun-queue-and-worker-pool.md) | The queue and worker-pool topology that replaced synchronous child-task execution. Written after the fact; the V2 docs still describe the pre-queue model. | Current |
| [`2026-06-07-agent-service-driver-version-proposal.md`](2026-06-07-agent-service-driver-version-proposal.md) | AgentService should reference an immutable `DriverVersion` instead of storing TypeScript. No AgentService controller exists. | Aspirational |
| [`2026-06-07-trigger-workflow-proposal.md`](2026-06-07-trigger-workflow-proposal.md) | `TriggerBinding`/`TriggerEvent`/`TriggerDelivery` as the durable ingestion layer in front of the driver platform. | Implemented — historical |
| [`2026-06-07-slack-agent-service-proposal.md`](2026-06-07-slack-agent-service-proposal.md) | A long-lived Slack runtime as an AgentService over a registered DriverVersion. | Aspirational |
| [`flue-daytona-fleetdb-v1-proposal.md`](flue-daytona-fleetdb-v1-proposal.md) | Pointer: the full document exists only on an unmerged branch. Kept so the "Related" links in the V2 docs resolve. | Pointer |
| [`flue-daytona-runtime-proposal.md`](flue-daytona-runtime-proposal.md) | Pointer, same reason as above. | Pointer |

## Lead agents and the epic runner

Product rules for this cluster live in
[`../product/lead-agent-epic-runner-spec.md`](../product/lead-agent-epic-runner-spec.md);
the docs here are the mechanism.

| Doc | Purpose | Status |
|---|---|---|
| [`epic-runner-workflow-architecture.md`](epic-runner-workflow-architecture.md) | An epic run is a driver run of a Flue workflow, not a Go loop. Canonical for how epic execution is wired today. | Current |
| [`lead-runtime-delivery.md`](lead-runtime-delivery.md) | The controlled lead runtime: how loom owns the lead's AI process so something other than the human can put a turn into the conversation. | Current |
| [`epic-runner-lead-control.md`](epic-runner-lead-control.md) | The 2026-05-16 direction plus its validation record. Superseded in part by the two docs above. | Superseded in part |
| [`2026-07-22-lead-conversation-resume.md`](2026-07-22-lead-conversation-resume.md) | Decision log for resuming a lead's conversation across restarts, accreting one decision at a time. **Active work — do not edit.** | In progress |
| [`2026-07-22-lead-resume-defects.md`](2026-07-22-lead-resume-defects.md) | Defects surfaced while planning the above. **Active work — do not edit.** | In progress |

## Web UI, file explorer, and workspace surfaces

| Doc | Purpose | Status |
|---|---|---|
| [`workspace-file-browser-security.md`](workspace-file-browser-security.md) | Scope resolution, path containment, and the write path for `/api/workspaces/{ws}/files`. **Canonical for shipped file-browser behaviour.** | Current |
| [`2026-07-07-file-explorer-v3-unified-tree.md`](2026-07-07-file-explorer-v3-unified-tree.md) | Unified tree with two lenses. Canonical for file-explorer information architecture. | Shipped |
| [`2026-07-02-file-browser-v2-scoped-explorer.md`](2026-07-02-file-browser-v2-scoped-explorer.md) | The v2 scoped explorer. Historical proposal — do not use as a behaviour reference. | Superseded |
| [`workspace-provider-refactor.md`](workspace-provider-refactor.md) | The stale-workspace bug on terminal-view workspace switching, and the router/loader fix proposed for it. Shipped by a different route. | Implemented differently |
| [`aether-wireframe-mapping.md`](aether-wireframe-mapping.md) | Maps every region of the Aether wireframe handoff to a repo component and classifies the delta. | Current |
| [`generic-sse-envelope.md`](generic-sse-envelope.md) | 2026-05-15 note proposing that the workspace SSE stream carry all fleet-db state changes, not only issue-shaped ones. | Current |

## Everything else

| Doc | Purpose | Status |
|---|---|---|
| [`2026-06-18-stack-aware-pr-publisher.md`](2026-06-18-stack-aware-pr-publisher.md) | The native stacked-PR publisher: publishing a group of lineage-linked tasks as PRs whose bases chain. | Shipped — design rationale, not an API reference |
| [`agent-run-visibility-plan.md`](agent-run-visibility-plan.md) | 2026-05-04 plan for surfacing agent runs in the UI. The primitives shipped; the unified UX did not. | Partially implemented |
| [`2026-07-23-module-map-for-external-docs.md`](2026-07-23-module-map-for-external-docs.md) | Primary-source survey of the repo's modules, gathered as input for writing external docs. Findings, not documentation. **Active work — do not edit.** | Research findings |
| [`2026-07-24-api-surface-consolidation.md`](2026-07-24-api-surface-consolidation.md) | Whether the 167-route HTTP surface should be collapsed. Recommends deleting the dead and redundant routes and collapsing the agent git verbs; leave the rest alone. | Proposal |

## Assets

- [`cortex-v7/`](cortex-v7/) — UI screenshots referenced by
  [`../epics/cortex-ui-v6-workspace-redesign.md`](../epics/cortex-ui-v6-workspace-redesign.md).
  Images only; see its README.

## Related

- [`../arch/README.md`](../arch/README.md) — as-built architecture of shipped
  subsystems. Canonicality is claimed per-doc in the banners, not by folder:
  `arch/file-explorer.md` owns the component/data-flow map while
  `design/workspace-file-browser-security.md` owns the security and write-path
  rules, and each says so.
- [`../product/README.md`](../product/README.md) — the product specs these
  designs implement.
- [`../loom-glossary.md`](../loom-glossary.md) — the shared dictionary.
