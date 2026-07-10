# SDLC Personas and Feature Groups

This document defines the product-facing frame for `feature-user-stories.tsv`.
Use it before adding or rewriting QA feature-map rows so the map starts from
SDLC jobs and user-visible capabilities, not implementation packages or test
mechanisms.

## Personas

| Persona | SDLC role | Primary jobs |
| --- | --- | --- |
| Engineering Manager | Accountable for delivery flow and confidence | See progress, understand blocked work, know review and release readiness, spot throughput risks |
| Staff / Tech Lead | Accountable for technical execution and delegation | Break down epics, prioritize work, assign tasks, unblock contributors, review technical context |
| Developer | Accountable for implementation | Pick up assigned work, use agents or terminals, inspect code context, produce commits and PRs |
| Platform Engineer | Accountable for operating Loom safely | Configure workspaces, repos, agents, workers, integrations, auth, runtime isolation, observability, and cleanup |

## Feature Groups

| Feature group | Primary personas | Scope |
| --- | --- | --- |
| Delivery Visibility | Engineering Manager; Staff / Tech Lead | Backlog health, progress, blocked work, agent/work queue state, review state, and management-level status |
| Work Planning | Staff / Tech Lead; Engineering Manager | Epics, issue creation, priorities, dependencies, decomposition, assignment, and ownership decisions |
| Agent Delegation | Staff / Tech Lead; Developer | Agent roles, agent definitions, task routing, lead workflows, epic runner behavior, and agent lifecycle controls |
| Implementation Workspace | Developer | Assigned task context, terminals, files, worktrees, sessions, diffs, and local development support |
| Review & Acceptance | Staff / Tech Lead; Developer; Engineering Manager | PRs, comments, transcripts, artifacts, diffs, review queues, acceptance evidence, and handoff context |
| Release Confidence | Engineering Manager; Staff / Tech Lead | Acceptance status, unresolved risks, test evidence, deployment readiness, and confidence signals before merge or release |
| Automation & Workflows | Staff / Tech Lead; Platform Engineer | Workflow authoring, task runners, trigger bindings, awaits, approvals, webhooks, SDK/API contracts, and connectors |
| Platform Setup | Platform Engineer | Workspace setup, repo registration, backend configuration, role configuration, worker profiles, daemon profiles, and environment bootstrap |
| Security & Runtime Controls | Platform Engineer | Auth, scoped tokens, leases, sandboxing, trust boundaries, connector grants, secret handling, and permission controls |
| Operations & Observability | Platform Engineer; Engineering Manager | Health checks, metrics, traces, logs, usage, cleanup, retention, runtime diagnostics, and operational dashboards |

## Mapping Rules

- Each TSV row should primarily serve one persona and one feature group.
- A row should describe a user-visible capability or operational outcome, not a
  package, command target, or test suite.
- Implementation paths belong in `Code refs`; tests and scripts belong in
  `Automated coverage`.
- Internal validation mechanisms are evidence, not feature groups. Examples:
  `make gate`, local-mode smoke, FleetDB UI tests, distributed smoke, sandbox
  acceptance scripts, and generated-client staleness checks.
- If a behavior exists only to verify Loom itself, map it under the product
  capability it proves, usually Release Confidence, Security & Runtime Controls,
  or Operations & Observability.
- If a capability has multiple personas, choose the persona who makes the
  decision or receives the most direct value. Mention secondary personas only
  when it clarifies scope.

## TSV Rewrite Target

When revising `feature-user-stories.tsv`, prefer this shape:

```text
Story ID: stable ID
Area: one feature group from this document
Feature: product-facing capability name
User story: As a <persona>, I want <capability>, so <SDLC outcome>.
Expected behavior: observable product behavior
Code refs: implementation anchors
Automated coverage: tests, commands, or None found
Test status: conservative verification status
Defect status: known issue or None logged
Priority: SDLC/business impact
Notes: short caveats and evidence limits
```
