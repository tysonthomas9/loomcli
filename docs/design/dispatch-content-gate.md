# Dispatch content gate

**Invariant:** a task with *no description AND no acceptance criteria AND no
design* is never claimed for a worker role.

## Why it exists

Tester-created scratch rows — PUPPET-618 ("PUPPET-607 tester EM control"), and
the `AC probe PUPPET-415 tester rN` family before it — carry a title and
nothing else. Nothing in the dispatch path checked for content, so the planner
claimed PUPPET-618 one minute after it was created and it then cycled planner →
critic four times. Both roles independently concluded "there is nothing here to
plan" and refused to promote. Cost: six fleet runs, zero output.

Creation stays unvalidated on purpose. The tester legitimately needs those probe
rows to exist; what must not happen is a role being handed one.

## Why not a reserved label

`models.Role.ExcludeLabels` exists in fleet-db but nothing consumes it, and
loomcli's `config.RoleConfig` / `cli.RoleConstraints` have no label-exclusion
field at all. The only exclude-label filters that actually run are the epic-drain
and driver ones, which sit on a single path. A "probe" label would need new
plumbing, would depend on whoever created the row remembering to set it, and
would still miss the pre-assignment path — a ticket that arrives with an
assignee already set is never checked against an exclude list.

Content is intrinsic to the row. It needs no cooperation from the creator.

## Why at claim time

Every arrival path converges on the claim:

| arrival path | code | covered by |
|---|---|---|
| global ready queue | `supervisor.tryClaimBestTask` → `claimIssueForAgent` | gate in `claimIssueForAgent` |
| assignee-scoped ready queue | `supervisor.claimTask` (assignedOpts) → same | same |
| control-plane pre-assignment (`agent_start` with `payload["task_id"]` → `RequestedTaskID`) | `claimRequestedTask` → `claimIssueForAgent` | same |
| agent self-claim (auto mode, LLM picks from `loom data ready`) | daemon IPC `handleIPCClaim` | explicit gate call |
| driver / epic drain | `driver.ClaimReadyTask` | explicit gate call |
| resume of the agent's own interrupted task | `claimResumeTask` | **deliberately exempt** |

`claimIssueForAgent` is the only place `ClaimIssue` / `ClaimIssueAsActor` is
called in the supervisor, which is why it is the gate point rather than four
separate ones.

## The projection constraint

`backend.IssueData` — what `Ready` and `List` return — carries neither
`Description` nor `AcceptanceCriteria`. Those live only on
`backend.IssueDetailData`, returned by `Get`. So the gate **cannot** be a
predicate in `internal/cli/taskfilter.go` next to `HasDesign`: at selection time
the content fields are simply absent, and a naive predicate would reject every
task.

Instead the gate does one `Get` on the single candidate that is about to be
claimed — not on every candidate; the 256-row ready list is never
detail-fetched.

## Fail open

A refusal requires **positive evidence of emptiness**. If `Get` errors, times
out, or returns nil, the gate allows the claim. A guard that could not read the
row must never be the reason a fleet stops working.

## What counts as content

- a non-blank `Description` → content
- a non-blank `AcceptanceCriteria` → content
- `HasDesign`, a `Design` body, or a `DesignArtifactID` → content (a planned
  task is still work even if the body was later emptied; mirrors `cli.HasDesign`)
- a title alone → **not** content; this is exactly the PUPPET-618 shape
- notes → **not** content; they are ops chatter, and a refusal note must not be
  able to self-heal the row

Comparisons are made after `strings.TrimSpace`, so a whitespace-only or
newline-only description is empty.

## Repeat-poll cost

Without a memo the supervisor would `Get` the same bodyless row on every poll
cycle. The gate keeps a small per-process cache of refusals keyed by
`(issueID, UpdatedAt)`. `UpdatedAt` is in the slim projection, so any edit to
the row — the tester filling in a description — invalidates the entry for free.
A 15-minute TTL bounds staleness for backends with coarse timestamps, and the
cache is capped at 1024 entries.

The memo is advisory and per-process. It never mutates the row, so concurrent
supervisors need no coordination.

## Resume is exempt

`claimResumeTask` re-acquires the agent's **own** interrupted task, which is
already `in_progress` and has already had a run spent on it. Gating it would
strand a live session on a row whose description was cleared mid-run.
`ResumeTaskID` is only ever set from a surviving crash-remnant lock, never from
a control-plane pre-assignment, so the exemption does not reopen the gap.

## Non-goals

No create-time validation. No writes to the refused task — no label, no comment,
no status change. No change to `loom data claim`: operators keep the ability to
claim anything by hand.

## Kill switch

`LOOM_DISPATCH_CONTENT_GATE=off` (also `0`, `false`, `no`) makes every path
behave exactly as it did before the gate.

## Follow-up

Adding `has_description` / `has_acceptance_criteria` presence flags to the slim
projection — mirroring `has_design` — would let *selection* skip bodyless rows
before scoring, turning the gate's `Get` into a fast path rather than a rewrite.
That is a fleet-db schema + API + client change across two repos, which is why
it is not part of this change.
