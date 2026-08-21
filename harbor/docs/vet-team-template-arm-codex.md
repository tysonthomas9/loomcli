# Verdict: REVISE

Do not spend yet. R4 is real, R1 has no ordinary post-apply label fix, the proposed reopen logic routes failures to the wrong worker, revised designs remain deadlocked, and the gate should promote the checked merge rather than recreate it.

## 1. R4: cross-repo claims

**CONFIRMED — yes.** `frontend-dev-1` can claim a `source_repo=qa-verify` task if it otherwise satisfies `has_design`, status, and labels.

The important behavior is slightly different from the plan’s wording:

- Template agents persist no `Repos` or `RepoGroups`; only `CrossRepo=true` is set. `internal/teamtemplate/teamtemplate.go:269-280`
- `ResolveAgentRepos` ignores `CrossRepo`. Empty `Repos` and `RepoGroups` returns `nil`, explicitly meaning all repos. `internal/cli/config/repos.go:8-17`
- `buildClaimOpts` only populates `ReadyOpts.SourceRepos` when that result is nonempty. `internal/cli/daemon/supervisor/claim.go:94-111`
- Therefore the daemon’s ready query is not source-repo-filtered. It selects with normal role constraints and claims atomically. `internal/cli/daemon/supervisor/claim.go:118-138,193-218`
- When `SourceRepos` is supplied, the Fleet backend client does hard-filter the returned queue by source repo. `internal/backend/fleet/fleet.go:400-415`; `internal/backend/fleet/deferred.go:36-46,105-126`

So R4 is decided: `verify_role=tasks` is unsafe with the stock template agents. An open, designed `qa-verify` task is visible to frontend-dev, backend-dev, and QA.

## 2. R1 and post-apply separation

**CONFIRMED — there is no simple post-apply role setter that separates QA from developers.**

- `loom role set` exposes neither `labels` nor `exclude_labels`. It exposes skills, path patterns, priority, and execution fields only. `internal/cli/role/role_cmd.go:73-119,300-420`
- `path_patterns` are explicitly not used in routing. `internal/cli/task_router.go:19-32`
- Skills are advisory: zero overlap still scores `10`, so they cannot exclude a claimant. `internal/cli/task_router.go:128-147`
- `max_priority` is a one-sided cap. It cannot partition two lanes because the other roles still accept the QA priorities. `internal/cli/task_router.go:118-121`
- Agent `task_filter` does not provide a disjoint QA/dev partition by itself.

There is one hard alternative: source-repo affinity. `agentdef add --repos` persists explicit repos, which become `ReadyOpts.SourceRepos`. `internal/cli/agentdef/agentdef_cmd.go:101-114,175-190`; `internal/cli/config/repos.go:32-59`. But:

- Existing agents cannot have repos changed through `agentdef update`; it only exposes parent, role, mode, and completion hooks. `internal/cli/agentdef/agentdef_cmd.go:121-130`; `internal/cli/agentdef/agentdef_hooks.go:307-360`
- You would have to stop/remove/recreate all three dev/QA agents: devs with `--repos app`, QA with `--repos qa-verify`.
- That is not usable naively here because `qa-verify` is a virtual repo with no checkout. Materialization attempts selected repos as Git worktrees and fails on an unusable repo path. `internal/localworkspace/agent_worktrees.go:148-189,199-210`
- Leaving dev agents with empty repo affinity does not help: empty means all repos.

### Option (c)

**CONFIRMED schema-valid, but incomplete as written.**

`qa-engineer labels:[qa]` with `task_filter:has_design`, plus developer `exclude_labels:[architect,qa]`, passes the listed validation rules:

- The “worker + `task_filter:any` requires labels” rule does not apply to the `has_design` roles. `internal/teamtemplate/validate.go:158-176`
- `exclude_labels` may exist without required labels.
- The only cross-list restriction is that the same label cannot be both required and excluded. `internal/teamtemplate/validate.go:177-195`

Two catches:

1. QA remains `has_design`; a lead-filed QA task without a design is unclaimable. Either file it with `--design`, or use `task_filter:any, labels:[qa], exclude_labels:[architect]`, which is also valid.
2. This is not a one-line fullstack-only edit. `frontend-dev`, `backend-dev`, and `qa-engineer` are shared across bundles, and `checkSharedRoles` rejects differing definitions. `internal/teamtemplate/teamtemplate.go:180-207`; `internal/teamtemplate/bundles/website.yaml:21-31`; `internal/teamtemplate/bundles/backend.yaml:21-31,48-61`. Update every shared definition or rename the fullstack roles. Bump each affected bundle revision; revision is the bundle content version. `internal/teamtemplate/teamtemplate.go:34-41`

Also, every plan occurrence of `loom data create --labels ...` is **WRONG**. The flag is repeatable `--label`. `internal/cli/data/create.go:85-102`

## 3. Integration gate

### Replay determinism

**WRONG as an acceptance mechanism.** Re-running:

```sh
git merge --no-ff --no-edit <sha>
```

can produce the same tree only if `/app` stayed at the identical base and the merge machinery behaved identically. It does not promote the exact commit that was checked; committer metadata can produce a different merge object. The plan currently checks one merge and creates another. `harbor/docs/plan-team-template-arm.md:125-139`

Use the gate worktree’s checked result directly:

1. Create detached gate worktree at exact `app_before`.
2. Prefer `git merge --ff-only "$sha"`; if that fails only because histories diverged, run `git merge --no-ff --no-edit "$sha"`.
3. Run `integration-check.sh` on that resulting tree.
4. Capture `gate_head=$(git -C "$gate_wt" rev-parse HEAD)`.
5. Verify `/app` is still exactly `app_before`.
6. Promote the checked object with `git -C /app merge --ff-only "$gate_head"`.

That preserves legacy FF behavior when possible and creates exactly one checked merge commit when necessary. The existing gate already intends “check before `/app` moves.” `harbor/scripts/gatelib.sh:60-67,91-128`; `harbor/scripts/integration-check.sh:1-12`

**WRONG:** “`--no-ff` becomes a fast-forward when `/app` is already an ancestor.” `--no-ff` expressly forces a merge commit. The conditional FF-first algorithm above makes P4’s “A fast-forwards, B merges” assertion true.

### Existing machinery with merge `app_after`

- **CONFIRMED:** `impl_reviews` does not assume `app_after == candidate`; it only extracts the IMPL-DONE candidate. `harbor/scripts/orchestrate.sh:263-300`
- **CONFIRMED:** INTEGRATED parsing accepts any hexadecimal `app_after`, including a merge commit. `harbor/scripts/orchestrate.sh:203-239`
- **CONFIRMED:** `current_marker` does not inspect Git ancestry. The single-coder ancestry check is inside `integrate`. `harbor/scripts/gatelib.sh:18-37,76-89`
- **CONFIRMED:** anti-wedge logic is status/comment-based and unaffected. `harbor/scripts/orchestrate.sh:740-778`
- **CONFIRMED:** final app log accepts merge commits, but the stub’s exact five-commit assertion does not. `harbor/scripts/orchestrate.sh:481-488`; `harbor/test/run-stub-trial.sh:81-89`
- **WRONG:** `attempt_handled` can remain unchanged if adding `INTEGRATION-STALE`. Its ledger does not recognize that record. Add it, especially because reopen/update failures are currently ignored. `harbor/scripts/gatelib.sh:39-50`
- **CONFIRMED with scope guard:** the architect pending queue assumes one linear coder branch, but `team` forbids `arch=on`, so that machinery must stay disabled. `harbor/scripts/gatelib.sh:150-156`

For provenance, prefer iterating the known delivery worktrees and running `merge-base --is-ancestor "$sha" HEAD`. Do not parse human-formatted `git branch --contains` output. Include QA’s branch if QA can emit IMPL-DONE, not merely the two developer branches.

## 4. Exact reopen transitions

Use separate dev and architect reopen functions. One team-arm `reopen_task` cannot represent both transitions.

### (a) Integration-check failure → developer

```sh
loom data comment <id> "FEEDBACK attempt=<n>: integration check failed: <reason>"
loom data update <id> \
  --status open \
  --remove-label architect \
  --remove-label needs-revision \
  --remove-label qa \
  --assignee ""
```

The task keeps its design and frontend/backend hint label. It then matches developers’ `has_design` route. Label removal is repeatable. `internal/cli/data/update.go:237-238`; `internal/cli/task_router.go:94-121,278-300`

### (b) Merge conflict or stale base → developer

```sh
loom data comment <id> "STALE-BASE attempt=<n>: merge current main and re-signal"
loom data update <id> \
  --status open \
  --remove-label architect \
  --remove-label needs-revision \
  --remove-label qa \
  --assignee ""
```

Do not add `needs-revision`; that makes `ReadyToImplement` fail. `internal/cli/task_router.go:278-300`

A further implementation issue remains: clearing the assignee does not guarantee the same developer branch reclaims the task. If another developer claims it, the rejected candidate is not in that worktree. The rework prompt must either restore/cherry-pick the recorded candidate or introduce a hard same-lane routing rule.

### (c) Lead design rejection → architect

```sh
loom data comment <id> "FEEDBACK: <design objection>"
loom data update <id> \
  --status open \
  --add-label architect \
  --add-label needs-revision \
  --remove-label qa \
  --assignee ""
```

The architect requires `architect`; developers exclude it. `internal/teamtemplate/bundles/fullstack-app.yaml:10-18,21-52`; `internal/cli/task_router.go:99-115`

The design approval command must remove both labels:

```sh
loom data update <id> \
  --status open \
  --remove-label architect \
  --remove-label needs-revision \
  --assignee ""
```

The plan removes only `architect`; after a rejected design is revised, `needs-revision` remains and P2 says no template worker can claim it. The existing verifier lead prompt already removes `needs-revision`. `harbor/prompts-generic/lead-persistent-verifier-tasks.md:34-38`

Also update every team developer/QA override’s “design unviable” path to add `architect`. Stock prompts currently add only `needs-revision`, producing the same deadlock. `internal/cli/agent/prompts/team-frontend-dev.md:90-95`; `team-backend-dev.md:85-90`; `team-qa.md:96-101`

## 5. Remaining claim audit and missed failures

- **CONFIRMED:** The branch merge base is `662523caa`; the merge-tree inspection is conflict-free, and the two shared prompt edits match byte-for-byte. Build output uses the checked-out loom and FleetDB trees, and VERSION records both SHAs. `harbor/bundle/build-bundle.sh:9-18,24-35`

- **WRONG/incomplete:** The build does not bump or pin FleetDB. It merely builds `FLEETDB_DIR` or sibling `../fleet-db`; current artifacts record `bccb9e9`. A team build must point at and verify a FleetDB commit containing #151 before packaging. `harbor/bundle/build-bundle.sh:9-18`; `harbor/bundle/dist/linux-amd64/bin/VERSION:1-3`

- **CONFIRMED:** Template apply requires a local workspace to have at least one registered repo. Current bootstrap creates the workspace with `/app`, then exports `LOOM_WORKSPACE`, so applying after line 90 is correctly ordered. `harbor/scripts/bootstrap.sh:81-90`; `internal/teamtemplate/apply.go:134-161`

- **CONFIRMED:** Four is the necessary `max_agents` value; all four template agents are runnable. The interactive reviewer has no agent and does not count. A lower limit makes daemon config invalid. `internal/teamtemplate/bundles/fullstack-app.yaml:55-82`; `internal/cli/config/project.go:404-430`

- **WRONG until implemented:** Adapter default remains two and only exports that value. Team must set four before apply, not merely warn afterward. `harbor/loom_harbor/__init__.py:40-65,218-226`

- **CONFIRMED:** `template show --json | jq -r '.agents[].name'` has the stated shape. `internal/cli/teamtemplatecmd/teamtemplate_cmd.go:112-132`; `internal/teamtemplate/teamtemplate.go:34-42`

- **WRONG:** Replacing `MARATHON_CODER_WT` with only `MARATHON_IMPL_WTS` breaks finalization under `set -u`; finalize still expands the old variable. The planner variable is also emitted from fixed bootstrap variables. Preserve compatibility aliases or rewrite every consumer and emit per-agent logs. `harbor/scripts/orchestrate.sh:13,478-515`; `harbor/scripts/bootstrap.sh:339-356`

- **WRONG:** The trust loop does not “already pick up all four”; it names `/app`, workspace roots, planner, and coder explicitly. Rewrite it over all materialized team worktrees. `harbor/scripts/bootstrap.sh:132-150`

- **CONFIRMED:** Prompt override loading and worker cwd work as claimed. `loadTemplate(id)` handles `loom-prompts/<id>.md`, and the spawned command’s cwd is the agent worktree. `internal/cli/agent/worker_prompt.go:24-54`; `internal/cli/daemon/supervisor/spawn.go:26-41`

- **CONFIRMED:** Stock developer and QA prompts close tasks, so overrides are required if their output enters the harness gate. `team-frontend-dev.md:99-109`; `team-backend-dev.md:94-104`; `team-qa.md:103-117`

- **WRONG citation:** The IMPL-DONE delivery contract is at `fleet_task-override.md:120-127`, not `:118-127`.

- **WRONG:** The architect prompt cannot remain fully unmodified. It reads actual code, but its persistent worktree is not automatically synchronized with `/app/main`; the supervisor only sets cwd and spawns `loom agent`. Add the same clean `git merge main` pre-step before architect grounding. `team-architect.md:47-67`; `internal/cli/daemon/supervisor/spawn.go:26-41,126-140`

- **CONFIRMED:** Skills give `+50` per match and zero-match fallback remains claimable. `internal/cli/task_router.go:128-147,325-340`

- **WRONG:** A 360-second cadence does not strictly bound lead approval latency. Persistent delivery may return `pending`, placing the message behind an active turn. `harbor/scripts/orchestrate.sh:193-200,644-695`

- **WRONG/incomplete:** `verify_role=off` does not by itself “rely on template QA.” Once option (c) isolates QA, the lead must deliberately create `qa`-labeled work. Decide whether those are designed `source_repo=app` tasks that produce gated test commits, or pure verification records with a different completion contract. Current `qa-verify` prompt creates undesigned tasks. `harbor/prompts-generic/lead-persistent-verifier-tasks.md:42-56`; `internal/teamtemplate/bundles/fullstack-app.yaml:44-53`

- **CONFIRMED:** Spend aggregation already scans all Codex session files recursively; it has no two-worker assumption. Four worker sessions will be counted. `harbor/scripts/spend.sh:13-17,43-74`

- **CONFIRMED:** Daemon preflight sets `AssignedTaskID`, exports `LOOM_ASSIGNED_TASK_ID`, and team prompts take the `.TaskID` branch. Their jq self-selection branch does not execute in daemon mode. `internal/cli/daemon/supervisor/claim.go:237-258`; `spawn.go:69-74`; `agent/agent_cmd.go:387-405`; `team-frontend-dev.md:16-34`

- **WRONG:** The proposed stub invocation cannot currently exercise persistent team mode. Stub mode forcibly changes persistent to oneshot, and the fake Codex implements only canned CLI turns, not the app-server/leadmsg runtime. `harbor/scripts/orchestrate.sh:27-38`; `harbor/stub/codex:31-42,195-197`

- **WRONG/incomplete:** Adding only new role-header branches to the stub is insufficient. It must seed `architect` plus frontend/backend labels using `--label`, implement architect design and team approval label transitions, resolve R1, and change the exact commit-count assertion for merge histories. `harbor/stub/codex:51-103,116-193`; `harbor/test/run-stub-trial.sh:64-95`

- **WRONG:** P1’s macOS host assertion cannot require all four worktrees while retaining `--branch marathon`; the established case-fold collision applies to `marathon` versus `MARATHON/...`. Use a non-prefix workspace branch such as `integration`, or land the product fix first. Bootstrap currently passes `--branch marathon`. `harbor/scripts/bootstrap.sh:81-90`; `internal/localworkspace/agent_worktrees.go:192-209`

- **MISSED paid-run risk:** Four workers can run fixed-port application checks concurrently. The existing free-port helper is only installed when a verifier role is enabled and killing another worker’s listener is not safe coordination. Add a shared port lock or per-agent ports. `harbor/scripts/bootstrap.sh:152-160`; `team-frontend-dev.md:62-73`

- **MISSED finalize risk:** Custom team roles spawn as `loom agent`, but the emergency kill pattern only matches `loom task|plan|daemon`. Add `agent` to the fallback cleanup. `internal/cli/daemon/supervisor/spawn.go:115-140`; `harbor/scripts/orchestrate.sh:386-403`

## Required fixes, ordered by severity

1. Resolve R1 in the product bundles: hard QA label routing, developer QA exclusion, coherent QA task lifecycle, shared-role consistency across bundles, and revision bumps.
2. Split dev versus architect reopen functions; remove both `architect` and `needs-revision` on approval; fix team prompt design-unviable paths.
3. Promote the exact checked gate-worktree HEAD into `/app`; use explicit worktree ancestry for provenance and add `INTEGRATION-STALE` to the ledger.
4. Build with a FleetDB revision containing #151 and make bundle preflight verify that capability/provenance.
5. Rewrite bootstrap around all four worker worktrees while retaining or replacing every fixed planner/coder consumer; trust all worktrees and avoid the macOS branch collision.
6. Synchronize the architect worktree with `main`, and define how a different developer recovers a failed candidate.
7. Redesign P5 for stub oneshot mode unless the stub gains app-server support; update seeding, role branches, label transitions, and merge-history assertions.
8. Add worker port coordination and finalize cleanup for `loom agent`.
9. Only then run P4/P5 and reconsider paid execution.