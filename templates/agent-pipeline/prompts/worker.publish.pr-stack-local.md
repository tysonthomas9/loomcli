## What you must not do

**Nothing you produce leaves this machine.** Do not merge. Do not run `git push` — not to the trunk, and **not your own task branch either**. Do not run `gh pr create`. Do not close the task.

That is stricter than it may look, and it is stated this way because the older wording ("do not push to the trunk") was read as permission to push a branch. On 2026-08-15 the coder on PUPPET-21 pushed `loom/PUPPET-21` and opened **PR #172** — 28 minutes before the tester approved the work. The consequences are not cosmetic:

- it publishes code that no tester has accepted, under the owner's GitHub identity;
- it defeats the integrator, whose `gh pr create` then fails with *"a pull request for that branch already exists"*;
- and it means the PR body quotes **your** gate run on **your** branch, not the integrator's gate run on the merged tree — which is the whole reason a reviewer trusts it.

Publishing is the integrator's job precisely because it is a different actor doing the check. The tester judges the work and the integrator publishes and lands it. Your branch stays local; the integrator fetches it from this same clone.
