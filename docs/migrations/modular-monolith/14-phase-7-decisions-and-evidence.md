# Phase 7 Decisions and Evidence

- **Status:** Complete
- **Date:** 2026-08-05
- **Loom implementation head:** `7942e55e3`
- **FleetDB companion head:** `54fc726`
- **Workspace:** `PHASE7-PROOF-20260804`
- **Product boundary:** signed packaged Desktop, real Codex, local delivery;
  GitHub mutations explicitly waived by the operator

## Outcome

Phase 7 completes the ten capability roots, moves the remaining Work Items,
Workspace, Source Control, Connectors, and Artifacts behavior behind their
owners, and extracts the Observability and Source Control frontend routes as
vertical feature slices. `App.tsx` and `WorkspaceViewContext` now retain only
shell/routing composition rather than feature policy.

The architecture graph is ratcheted to `completed_phase: 7` with no pending
decision. The current measured inventory is:

| Guard | Final value |
| ----- | ----------: |
| Active capability roots | 10 |
| Composite Store references | 64 |
| Composite Store references outside composition | 54 |
| Handler exceptions | 77 |
| Mutation commands | 107 |
| Primary direct-write rows | 102 |
| Runtime components | 71 |
| Goroutine launch sites | 80 |
| Performance rows measured | 6 of 6 |

The ten active roots are Agents, Artifacts, Automation, Connectors, Execution,
Interaction, Source Control, Workflow Catalog, Work Items, and Workspace.

## Capability completion

| Capability | Phase 7 closure |
| ---------- | --------------- |
| Work Items | Owns comments, dependencies, lifecycle queries/commands, issue routes, workspace activation, and onboarding task creation. |
| Workspace | Owns catalog/repository queries, durable deletion, repository admission, CLI delegation, and lifecycle commands. |
| Source Control | Owns task-outcome lineage, stack topology/publication, task-stack binding queries, and the pull-request frontend feature. |
| Connectors | Owns grants, management/rotation/synchronization, credential vault adaptation, dispatch authorization, provider action contracts, and provider dispatch lifecycle. |
| Artifacts | Owns session transcript lifecycle and session-evidence queries. |
| Interaction | Publishes harness transcripts through the Artifacts owner API and owns persisted UI tab state. |
| Automation | Owns the approval event journal. |
| Execution | Owns lead-assignment enqueue and publishes durable DriverRun claim identity. |

FleetDB commit `661cb1e` publishes DriverRun Work Item claims needed by the
paired Loom contracts. FleetDB commit `54fc726` makes the coverage gate fail
closed instead of accepting an incomplete package set.

## Frontend feature slices

Observability moved as a complete route, API mapper, polling hook, state,
components, styles, types, and tests under
`internal/webui/frontend/src/features/observability`. Source Control's pull
request workspace moved under
`internal/webui/frontend/src/features/source-control`. Lazy route imports and
the bounded feature-boundary scan prevent those slices from leaking back into
global barrels.

Product proof found one integration defect that component tests did not expose:
the Observability page rendered successfully but showed zero activity after
modular DriverRuns and a restart because its cache replayed only the legacy
JSONL journal. The closing fix supplements that journal with a narrow,
read-only projection of durable FleetDB DriverRuns. It preserves legacy data,
classifies completed/needs-review and failed/cancelled outcomes, and restores
task counts, duration, agent, role, epic, and line-change dimensions after
restart. `TestHandleObservabilityMetrics_MergesDurableDriverRuns` pins the
mixed-source behavior. The production endpoint now scopes that projection by
the workspace in the route, keeps independent TTL values for at most 128
workspaces, and falls back to an uncached read beyond that bound. Request
generation fencing prevents a late response for one workspace from replacing
the selected workspace's metrics. Backend cache-isolation and frontend
in-flight workspace-switch regressions pin both contracts.

The exact packaged API returned 17 completed and one failed durable run for
`PHASE7-PROOF-20260804`, including the expected agent and role dimensions.
The same process returned zero for its default workspace and for an unrelated
workspace, proving both durable projection and workspace isolation. The
packaged Observability route rendered the resulting 5.6% error rate and hourly
completion history after restart.

## Exact packaged-Desktop workload matrix

The UI created every counted agent. Every passing row used a real Codex process;
creation-only records and empty scheduler sweeps do not count.

| Template | Selected run 1 | Selected run 2 | Result |
| -------- | -------------- | -------------- | ------ |
| Behavior Planner | `automation-run-fddd2a2e0a5592c7f3636ad6ef88beb1` on task 1 | `automation-run-9a0f429fff5b1c5a802999df639e872d` on task 2 | 2 passed; designs persisted; Review; transcripts; no diff |
| Behavior Coder | `automation-run-a6b3c6b7d85f0630523cc1e0189a9e0b` on task 1 | `automation-run-3cad1c8a6cf6ebfedaf5e24d67fc9d46` on task 2 | 2 passed; local branches; transcripts and repository evidence |
| Behavior Bug triage | `automation-run-2a5f8dfa25de1950f330e3c97d0e0dfc` on task 5 | `automation-run-cdf01ca75148949bbb03f187087eb310` on task 6 | 2 passed; root cause/priority; Review; no diff |
| New Role, Review trigger | `automation-run-6149786fcbc8bd133cbaea615b1c338e` on task 3 | `automation-run-488655a1f3182f166caccdfb7a050c1a` on task 4 | 2 passed; transcript/diff; local branch; no self-trigger |
| Local review | task 1 child of `automation-run-c70bf90ad179d43543b591f96fcd493a` | task 2 child of the same parent | 2 passed; `codex-review`, exit 0, persisted transcripts |
| Interactive Lead | `session-311ef6c8-4c4a-4722-874f-0a0395e90147` | `session-2f535fc6-9352-40fb-9e2a-26ac14865462` | 2 passed; normal exit; Completed transcript sessions |
| Interactive PR Review | `session-2ce6545c-9c1b-4168-9540-eb40187d86a6` | `session-076b3639-4741-4274-9853-4066e1815219` | 2 passed against local targets; no external mutation |
| Interactive Custom | `session-149eac19-f60f-44f6-903b-930f0814dd53` | `session-ebf25836-b952-4b5a-bbfa-516cb2e60c3e` | 2 passed; literal prompt; normal exit; Completed transcripts |
| Bug-fix | GitHub execution row 1 | GitHub execution row 2 | Operator-waived |
| Review loop | GitHub execution row 1 | GitHub execution row 2 | Operator-waived |

Disposition: **16 local execution rows passed, four GitHub execution rows were
waived, and the three Advanced daemon-supervised templates are N/A because
Phase 6 retired them.**

## Failure, restart, and fail-closed proof

UI-created Planner agent `agt-phase7-codex-unavailable-planner-d8836e85`
received task `PHASE7-PROOF-20260804-8` while both installed Codex launch paths
were temporarily unavailable. DriverRun
`automation-run-37a1c0219b9cabf07b6f02d0b106e946` failed in 1.6 seconds with
exit 1 and `local_backend_unavailable`; the task was unassigned in Blocked,
with zero tokens, no transcript, no diff, and no repository mutation. Both
Codex binaries were restored immediately. Restarting the packaged app retained
the same Blocked state.

The focused packaged-builtin digest suite also passes all six selected cases:
refreshing digest drift, preserving the underlying build failure, failing
closed without a build toolchain, matching packaged markers, preserving an
operator-managed active version, and failing closed on an unusable
operator-managed version. The signed package itself was not corrupted for the
probe; the exact embedded-bundle paths are covered by those deterministic
tests.

Crash/stale recovery and active-claim deletion conflict were exercised before
the final catalog matrix. FleetDB's owner-fenced recovery and published Work
Item claim receipt prevented a stale generation from releasing or deleting a
successor claim.

## Gates and immutable artifacts

| Evidence | Result |
| -------- | ------ |
| Focused observability/events/serve tests | PASS |
| Phase 7 architecture guard with 2 GiB process-tree ceiling | PASS; 11 of 11 profiles, zero pending decisions |
| Loom aggregate `make gate` | PASS at `7942e55e3`, with exact FleetDB source and binary at `54fc726` |
| FleetDB aggregate `make gate` | PASS at `54fc726` |
| Paired OpenAPI SHA-256 | `816b0b0ca5a3398238cf56152f6a040e1bc4cc3bd3c5d1e2dde3fa775dca7ef0` |
| Packaged Loom sidecar SHA-256 | `85cbbc6523eea49d00c6e1a8d9bd0663dd1ee4c9bfe7f3f38458fe714b57edf5` |
| Packaged FleetDB sidecar SHA-256 | `5d304a2b49aad87f1d10b245b0f7d55f5ea4cc6e8d8e6f4b09fa4578360f37e4` |
| Packaged Desktop executable SHA-256 | `9cabe64d83bf9bedfbb77a595781f6860bcf8e3e4baa43c432d36e1df2efe9e8` |
| Packaged Node SHA-256 | `34c0af7cb2ba9eeb14e0675695e3f6da15fa5e98901e62149cf4fc1d594c8fa0` |

Selected local screenshot artifacts and SHA-256 values:

| Artifact | SHA-256 |
| -------- | ------- |
| `/tmp/phase7-planner-task-panel-1b8a45e2e.jpeg` | `bd60b17251900aa0f4a6bba1f49a5ddc6460dc05729c228d701e5209cd2ef440` |
| `/tmp/phase7-new-role-second-run-diff.jpeg` | `78c24472d5667129a57ce31e92d3c1d682396f40e209f41cd5d252f90b350e50` |
| `/tmp/phase7-local-review-task1.jpeg` | `10d89aa645980cf8cb21a3fa0db0e032adb8154106dc2ac78311f4d0343262ed` |
| `/tmp/phase7-local-review-task2.jpeg` | `6e54258d82b5429d0a2a3fa3cec012b0dfd6796f4a640e9340a74fc724426509` |
| `/tmp/phase7-behavior-bug-triage-two-runs.jpeg` | `b7d9b0bb4e7fac9f2868225fc4d60e0766b3f2a5ff2fcd05c2d4399b79066ba3` |
| `/tmp/phase7-interactive-pr-review-two-runs.jpeg` | `18b23cf2fe4a0d6bb5a1dfa4c52d8766d1bd9840f6114dba8368c864067a6ed8` |
| `/tmp/phase7-interactive-custom-two-runs.jpeg` | `995d98b68556c1a692decfbbcee4634bbe00bd1dd6c944461ff17fb48a06e55a` |
| `/tmp/phase7-codex-unavailable-blocked.jpeg` | `e29305522cdaaa1f297be185c4252c9b4121d70e2eadbb07036cb69cc182208e` |
| `/tmp/phase7-codex-unavailable-after-restart.jpeg` | `ec6d33476fbcbb2993c3df5a8df1fca2dd07e749ed33d19564df0530e6597abb` |
| `/tmp/phase7-observability-durable-runs.jpeg` | `ff22ba5d50cfc23480e9b2a45e95e14ab10e6297c9e2705546b2afe3e2466706` |
| `/tmp/phase7-source-control-route.jpeg` | `ce19e5a39ad45d69c60150fe2a406b0e9e5c0c02e50533d1b4f55cbf7b21b702` |

The screenshots remain local proof artifacts and are not source-controlled
generated output.

## Completion decision

Phase 7 is complete at the closing implementation commit and exact rebuilt
package recorded above. No capability or frontend migration phase remains;
subsequent work is ordinary product maintenance against the enforced
modular-monolith boundaries.

---

[All migrations](../README.md) · [Migration overview](README.md) · Previous:
[Phase 6 evidence](13-phase-6-decisions-and-evidence.md)
