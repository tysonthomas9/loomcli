# Phase 7 Decisions and Evidence

- **Status:** Complete
- **Date:** 2026-08-07
- **Loom implementation head:** `c3b9cb1bc`
- **Topology-hardening head:** `d8f7f1387`
- **Initial completion head:** `5adee3407`
- **FleetDB companion head:** `b71dec5`
- **Workspaces:** `PHASE7-PROOF-20260804`, fresh cleanup proof
  `PHASE7-CLEANUP-PROOF`, and final packaged restart proof
  `PHASE7-FINAL-PROOF3-20260807`
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
| Composite Store references | 18 |
| Composite Store references outside composition | 0 |
| Legacy handler imports | 27 |
| Legacy service-handler imports | 0 |
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
closed instead of accepting an incomplete package set. Closing companion
commit `b71dec5` makes the history E2E derive its time bounds from server
timestamps, eliminating host/Podman-VM clock skew without weakening the API
assertions.

## Legacy business-logic retirement

The closing source cleanup is subtractive rather than another facade layer.
The legacy Work Items issue service and its duplicate handler branches are
deleted; issue routes now enter through the Work Items public capability. The
remaining agent, file, session, source-control-diff, and workspace delivery
coordination was split into focused `internal/webui/*coord` packages. Log
storage moved to `internal/logstore`, path security to `internal/pathsec`, and
the terminal contract to `internal/webui/terminal`.

As a result, `internal/webui/service` and `internal/webui/svcimpl` contain no
production or test source files, and handlers have zero imports of either
legacy service package. The 27 remaining handler exceptions are generic
delivery dependencies measured by the broader handler guard; they are not
imports of the retired service centers. The exact `internal/app/*` purity rule
was retained: infrastructure-bearing coordination was not hidden inside a
named workflow core merely to make a path count fall.

Removing two umbrella imports makes the real owner dependencies visible at
five composition/delivery hubs. The default internal-import fanout ceiling
remains 18. `scripts/import-fanout-exceptions.tsv` pins those five hubs to
their exact measured values (`22`, `20`, and three at `19`); either growth or a
stale exception after a reduction fails the gate. This is an explicit
composition ratchet, not a repository-wide ceiling increase.

The cleanup commit itself changes 239 files with `3,292` insertions and
`16,246` deletions, a net removal of `12,954` lines. Across the complete Phase
7 stack, the closing comparison with Phase 6 changes 596 files with `22,481`
insertions and `24,119` deletions, a net removal of `1,638` lines. The larger
stack additions are the new capability modules, adapters, tests, and evidence;
the closing commit is the explicit deletion of the displaced legacy
implementation.

### Post-completion topology hardening

A final ownership audit after the initial completion decision found that the
source graph still exposed retired horizontal compatibility packages even
though their capabilities had moved under `internal/modules/*`. The follow-up
cleanup removes the remaining domain/entity/type aliases and tombstones the
old `agentinbox`, `connector`, `leadcontrol`, `stacklineage`, `stackpublish`,
`stackstore`, `trigger`, `webui/service`, `webui/svcimpl`, `workspace`, and
`workflows` roots so they cannot silently return.

The surviving implementations now live at their actual ownership boundary:
Interaction owns inbox behavior; Connectors owns catalog and provider
contracts; Source Control owns lineage while its persistence and publication
adapters live in infrastructure; Workspace IDs are platform primitives; and
Automation infrastructure owns trigger delivery plus await reconciliation and
timeout recovery. Driver composition no longer creates a private stack store
fallback, so source-control lifecycle state comes only from the composed owner
port.

The composite Store baseline is now an exact 18-reference allowlist, all at
composition or compatibility boundaries, with zero capability use outside
composition. The alias guard rejects any exported alias from the retired
domain/entity/type buckets, and the legacy-root guard fails if any tombstoned
package is recreated. The exact architecture pass reports 10 capability
roots, 18 Store references, zero outside composition, 27 legacy handler
imports, 102 direct-write rows, 107 mutation commands, 71 runtime components,
80 goroutine launch sites, all six performance rows, all 11 profiles, and zero
pending decisions. The last measured architecture analyzer process-tree peak
was 1.25 GiB under the unchanged 2 GiB ceiling.

Implementation commit `d8f7f1387` changes 359 files with 3,263 insertions and
5,042 deletions, a net removal of 1,779 lines. The exact source passes the full
Loom aggregate gate with the companion source and freshly built binary pinned
to FleetDB `b71dec551`; the generic sibling FleetDB checkout was deliberately
not used because it has advanced beyond the Phase 7 contract.

Closing runtime commit `55392d01d` also makes the built-in task bridge export
the typed workspace as host-owned `LOOM_WORKSPACE` and requires mutating roles
to produce a reviewable file change. An exit-zero coder with no captured change
now fails as `local_change_delivery_missing`, preserving its real transcript
instead of closing the card without a patch. Gate commit `c3b9cb1bc` folds the
Phase 5 Agents mutation scan into the authoritative per-profile architecture
transaction and removes the duplicate repository-scale race scan. The full
gate therefore retains the same 11-profile ownership evidence and 2 GiB memory
ceiling without reloading that typed graph a second time.

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

### Closing cleanup package refresh

The legacy-retirement commit was rebuilt as the signed packaged app and tested
again in a fresh workspace with a fresh local repository. UI-created Planner
`agt-phase7-cleanup-planner-2f30d072` completed
`automation-run-9e2e2be2b3441a1940d2fcf59a931141` on
`PHASE7-CLEANUP-PROOF-1`, persisted the full design, exposed the clickable task
link and transcript, and handed the task to Review. After UI approval,
UI-created Coder `agt-phase7-cleanup-coder-547defaa` completed
`automation-run-ce7ae99c11b2b1e5ed6b436b868abf22`, committed exactly
`README.md` and `proof.txt` at `e6e0fee569d3`, delivered local branch
`loom/PHASE7-CLEANUP-PROOF-1`, exposed its transcript and diff, and returned the
task to Review. The earlier pre-design event is also visible as a completed
no-dispatch filter result, proving role-phase selection rather than a duplicate
coder execution. No GitHub mutation occurred.

### Final packaged planner/coder and restart proof

The signed packaged app was rebuilt from the source subsequently committed at
`55392d01d` and `c3b9cb1bc`, with embedded prompt-agent digest
`sha256:157062b5cfc5ce395cfe66a3961b682f00c364450409e07533943ccf1fd93334`.
All task, agent, status, approval, and enablement mutations in this proof were
performed through the packaged UI.

UI-created task `PHASE7-FINAL-PROOF3-20260807-2` targeted the admitted
`hello-world` repository and requested exactly one new file,
`phase7-packaged-proof4.txt`. Planner
`agt-phase7-final3-planner-b1d9b8e3` ran real `codex exec --json`, completed
with exit 0 in 3m35s, persisted the full structured design, exposed its
transcript, and moved the card through Open, In Progress, and Review. The
persisted coder definition was disabled from an earlier timeout test; the UI
showed that state, it was explicitly enabled in the UI, and a new Review
approval emitted the fresh ready event.

Coder `agt-phase7-final3-coder-845db7f3` then ran real `codex exec --json` and
completed with exit 0 in 3m11s. Run
`logs://promptagent-automation-run-fe504c3968ca8f5f6e8848d413a042ea-PHASE7-FINAL-PROOF3-20260807-2`
reported `local-cli-codex`, one file, `+1/-0`, patch-back applied, and local
commit `de4c3cf`. Its UI transcript and Diff tab showed only the exact required
LF-terminated line, and the task converged to Closed. The delivered file has
SHA-256 `ca59d8c053a27f0c3add5cac78532ee88f75dda9664794573ad344958d330657`.

The packaged Desktop process and all three Loom/Fleet sidecars were then
stopped and relaunched on a new HTTP port. From a fresh browser session, the UI
restored the same Closed card, full planner design, four-entry run history,
coder exit/delivery summary, transcript, and exact Diff tab. This is durable
restart proof, not an API-only artifact inspection.

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
| Initial-completion Loom aggregate `make gate` | PASS at `5adee3407`, with exact FleetDB source and binary at `b71dec5`; Go, architecture, race, and frontend gates passed |
| Post-completion-hardening Loom aggregate `make gate` | PASS at `d8f7f1387`, with exact FleetDB source and freshly built binary at `b71dec551`; Go, architecture, race, coverage, and frontend gates passed |
| Closing Loom aggregate `make gate` | PASS in about 14 minutes for the source committed at `55392d01d` and `c3b9cb1bc`, down from the earlier roughly 45-minute duplicate-scan path and pinned to FleetDB source/binary `b71dec551`; architecture stayed below its 2 GiB process-tree ceiling; 151 of 151 built-in workflow tests passed; frontend passed 396 files and 8,744 tests with one skipped |
| FleetDB aggregate `make gate` | PASS at `b71dec5`, including static/lint, race, 80.8% aggregate coverage with all 28 package floors, Postgres/API suites, 102-second real-container E2E, crash/restart recovery, and harness evaluation |
| Paired OpenAPI SHA-256 | `816b0b0ca5a3398238cf56152f6a040e1bc4cc3bd3c5d1e2dde3fa775dca7ef0` |
| Initial packaged Loom sidecar SHA-256 | `2ad923ed93c29361babdab665cd06db4da3ff0b83b3a80dc5d3d1cdba8cb8502`; reports `5adee3407` |
| Packaged FleetDB sidecar SHA-256 | `d943b06c375293929f7719ee3b91fb5a964d41379003e5b4a7947de1089df162`; reports `b71dec5` |
| Initial packaged Desktop executable SHA-256 | `eb32e837ca97f79ad3e3815fc67fd54e52212488cdee8f329a9b151c12725623` |
| Packaged Node SHA-256 | `34c0af7cb2ba9eeb14e0675695e3f6da15fa5e98901e62149cf4fc1d594c8fa0` |
| Hardened packaged Loom sidecar SHA-256 | `ec1f42945811c867d68dc98c96f7e8e046083045ac4de33a73ab1dc64c2627d3`; reports `d8f7f1387` |
| Hardened packaged Desktop executable SHA-256 | `ff1b62ab6b86ccc84968f89b6976cd99cfe0fd0d9f907e815a11044337932fc1` |
| Final packaged Loom sidecar SHA-256 | `f40b8c22016a3764946e7b7354a21f19f9051a5c64543848476642ae99ed1954` |
| Final packaged FleetDB sidecar SHA-256 | `d943b06c375293929f7719ee3b91fb5a964d41379003e5b4a7947de1089df162` |
| Final packaged Desktop executable SHA-256 | `fa4dc0bb60afbc1f733244815181f645b1f0ef28ce55d182e9cf376d996f0e30` |

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
| `/tmp/phase7-cleanup-coder-diff.png` | `78430f198053dae639db8c42786eafda24f9684abdf56047110391adc9899d71` |
| `/tmp/phase7-cleanup-task-review.png` | `e3acaaa3714b5482a3a6f924fc93bb0251e1eed79a3f26769d794b19ea8209d5` |
| `/tmp/phase7-proof4-coder-diff.png` | `5f309757db3ee54f2e030fb7b5d86f4b817122bd638a75bcfeeeafe9aa8279b5` |
| `/tmp/phase7-proof4-after-restart-diff.png` | `b9195ff24bb3fa1ec84829ce79a660b817f8121b060006e1e71d2ff28e4d6495` |

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
