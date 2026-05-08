# Agent Execution Product Docs

**Status:** Draft
**Date:** 2026-05-04

This folder captures the product plan for making agent execution visible,
controllable, and debuggable across local and cloud/containerized runs.

Read these in order:

1. `agent-execution-prd.md`
   The overall product requirement document for agent execution visibility.
2. `local-mode-product-spec.md`
   The first shippable slice: supervised agents running on one machine
   while still using FleetDB as the shared distributed control plane.
3. `desktop-app-runtime-spec.md`
   How the macOS Tauri app, LaunchAgent-backed background service, bundled
   CLI/runtime, updates, persistence, and multi-window workspaces fit together.
4. `desktop-installation-runbook.md`
   How to build, install, verify, troubleshoot, and update the macOS Tauri app
   during development and early release packaging.
5. `agent-run-ux-spec.md`
   What the UI should show for agents, sessions, task timelines, logs,
   diffs, failures, empty states, and stale/offline states.
6. `lead-agent-epic-runner-spec.md`
   How first-class lead agents run one epic at a time through the existing
   terminal UI, with scoped workers and epic/task panels.
7. `session-artifact-contract.md`
   The evidence every run must leave behind: transcript, logs, token usage,
   diff, commit, test result, and error class.
8. `daemon-agent-runtime-architecture.md`
   How the daemon, agent runner, local mode, cloud mode, ownership leases,
   and decentralized task claiming fit together.
9. `agent-lifecycle-state-machine.md`
   Canonical agent, run, and task states plus allowed transitions.
10. `failure-modes-recovery-ux.md`
   User-visible failure states and recovery actions.
11. `container-runner-mvp-spec.md`
   Podman/container runner behavior after local mode is reliable.
12. `dogfood-agent-execution-test-plan.md`
   End-to-end dogfood scenarios used to validate the product.

Related design docs:

- `../design/agent-run-visibility-plan.md`
- `../design/distributed-control-plane.md`
- `../design/distributed-control-plane-data-model.md`

Related runbooks:

- `../testing/local-mode-podman-e2e.md`
