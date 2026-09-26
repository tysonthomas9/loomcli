# agent-pipeline — a battle-tested multi-agent pipeline template

This is the workspace-provisioning template a live loom deployment has been
running its own development fleets on (the fleet that produced #282, #368 and
many of the PRs on this repo). It is published here as **reference material**:
it was developed operationally, outside version control, and several open
discussions (notably the review of #403's Team Templates) need something on
the remote to point at. Placement and packaging are open questions — treat
the content, not the location, as the contribution.

## What it is

One parameterized pipeline, per-workspace params, rendered into a workspace
and applied declaratively:

```
install.py <params/NAME.yaml> [--dry-run] [--flush] [--smoke] [--skip-apply]
```

`install.py` renders `pipeline.yaml`, `integration.yaml` and `prompts/` from
this directory, unified-diffs them against the live workspace, backs up
anything it replaces, applies the spec via `loom workspace apply -f
pipeline.yaml` (#368) with the supervisor stopped, and restarts the
supervisor. `--flush`/`--smoke` implement a rehearsal loop for a disposable
TEST workspace: reset the board and git fixture to a recorded baseline, seed
one end-to-end task, and require it to traverse every stage to `delivered`.

## The design, in one paragraph each

**The label ladder.** Tasks climb `(no labels) → plan-draft → verdict-ready →
ready-to-implement → in-review → approved → delivered`. A role requires its
own rung's label and **excludes every rung above it**, so a task that has
climbed can never be re-claimed from below, and each open task matches
exactly one role. Both halves of that rule were learned from live failures —
see the header of `pipeline.yaml.tmpl`, which records them so they don't get
"simplified" back out.

**Hook-driven hand-offs.** Stage transitions are `on_complete` hooks
(`add_label`, `set_status`, `comment`, `cycle` — all on v5 since #275), not
prompt instructions. A hand-off that lives in a prompt happens only when the
agent remembers to run it; a hook fires deterministically, including on odd
exits. The two roles with *no* hooks (tester, integrator) are deliberate and
documented inline: their outcomes branch, and only the agent knows which
terminal state happened.

**A bounded review cycle.** The critic's `cycle` hook re-arms the planner at
most N times, then stamps `review-exhausted`, which routes nowhere: an
exhausted review *stops* instead of shipping an unimplementable plan or
looping forever.

**QA sequenced behind implementation.** The tester requires `in-review`,
which only the coder's completion hook stamps — QA structurally cannot claim
work that hasn't been implemented.

**Delivery is a stage, with a written contract.** The integrator re-runs the
gate on the merged tree and publishes per `integration.yaml` — see the two
`integration.head.*.yaml` variants (PR-only trunks with stacked PRs, vs
direct push for sandboxes). The stage exists because of a measured failure,
recorded in the pr-stack-local head: a board reading "delivered" over a
deployment that had never seen the code.

## Files

| file | role |
|---|---|
| `pipeline.yaml.tmpl` | roles, agents, hooks, budgets — the ladder, with the reasons inline |
| `install.py` | render → diff → backup → `workspace apply` → restart; TEST flush/smoke |
| `prompts/*.md` | per-role prompts; `worker.publish.*` / `integrator.*` select by merge strategy |
| `integration.head.{pr-stack-local,direct-push}.yaml` | the delivery contract, both strategies |
| `integration-repos/test.yaml` | example per-repo block (sandbox fixture) |
| `params/example.yaml` | per-workspace parameters, genericized |

`install.py` targets a specific local stack (fleet-db on `127.0.0.1:3011`,
a PM2 local-deployer on `:3010`) — read it as reference tooling, not a
portable installer. Machine-specific params and integration blocks from the
originating deployment are deliberately not included.
