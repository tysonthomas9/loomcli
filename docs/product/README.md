# Agent Execution Product Docs

**Status:** Draft
**Date:** 2026-05-04

This folder captures the product plan for making agent execution visible,
controllable, and debuggable across local and containerized runs.

Read these in order:

1. `agent-execution-prd.md`
   The overall product requirement document for agent execution visibility.
2. `local-mode-product-spec.md`
   The first shippable slice: supervised agents running on one machine
   while still using FleetDB as the shared distributed control plane.
3. `agent-run-ux-spec.md`
   What the UI should show for agents, sessions, task timelines, logs,
   diffs, failures, empty states, and stale/offline states.
4. `session-artifact-contract.md`
   The evidence every run must leave behind: transcript, logs, token usage,
   diff, commit, test result, and error class.
5. `agent-lifecycle-state-machine.md`
   Canonical agent, run, and task states plus allowed transitions.
6. `failure-modes-recovery-ux.md`
   User-visible failure states and recovery actions.
7. `container-runner-mvp-spec.md`
   Podman/container runner behavior after local mode is reliable.
8. `dogfood-agent-execution-test-plan.md`
   End-to-end dogfood scenarios used to validate the product.

Related design docs:

- `../design/agent-run-visibility-plan.md`
- `../design/distributed-control-plane.md`
- `../design/distributed-control-plane-data-model.md`

Related runbooks:

- `../testing/local-mode-podman-e2e.md`
