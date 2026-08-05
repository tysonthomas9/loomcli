# Agent Creation Templates

**Status:** Current implemented contract, with exact packaged-runtime proof status recorded separately

**Date:** 2026-07-28

**Scope:** The user-visible templates in the web and desktop **New Agent**
modal

**Related:** [Create Agent redesign](../design/create-agent-redesign.md),
[Unified agent UX](../design/2026-07-01-unified-agent-ux-proposal.md),
[Agent execution PRD](agent-execution-prd.md), and
[Phase 4 evidence](../migrations/modular-monolith/09-phase-4-decisions-and-evidence.md).
The exact Phase 5 rerun is defined by the
[24-execution packaged Desktop proof](../migrations/modular-monolith/12-phase-5-real-codex-proof.md).

## How to read the count

The clean-workspace count of 12 is a presentation-layer composition, not 12
implementations of one runtime:

- six **Behavior** templates;
- three **Interactive** templates; and
- three **Advanced daemon-supervised** templates.

Behavior templates create either an event-triggered prompt `AgentService` or a
cron `TriggerBinding`. Interactive and Advanced templates create workspace
agent assignments, but Interactive agents are browser-launched with
`auto=false`, while Advanced workers are daemon-managed with `auto=true`.

Runnable user-defined Roles add dynamic Behavior cards, so a workspace can
show more than 12. The server also registers the hidden
`pr-review-checkout` prompt, but that prompt is not a gallery card. The legacy
Advanced Lead card is suppressed because the Interactive Lead card replaces
it.

## Which numbered templates actually ran

**#1 Behavior Planner**, **#2 Behavior Coder**, **#3 New Role**, and
**#6 Local review** completed real-Codex, UI-created, end-to-end workload
runs.

|   # | Template             | Did it run?      | Evidence boundary                                                                                                                                                                         |
| --: | -------------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
|   1 | Behavior Planner     | **Yes — full**   | Claimed an undesigned task, completed a real Codex TaskRun, persisted its design through the task-scoped API, moved the card to Review, and exposed a transcript with no repository diff. |
|   2 | Behavior Coder       | **Yes — full**   | Claimed a designed task, completed a real Codex TaskRun, delivered a local branch, and exposed transcript and diff evidence.                                                              |
|   3 | New Role             | **Yes — full**   | A UI-created documentation Role triggered on an exact move into Review, completed one real Codex TaskRun, delivered and stamped a local branch, exposed transcript/diff evidence, and returned the card to Review without retriggering itself.           |
|   4 | Bug-fix              | **No — blocked** | GitHub token/repository prerequisite was shown; no binding or PR-producing run was created.                                                                                               |
|   5 | Review loop          | **No — blocked** | GitHub token/repository prerequisite was shown; no binding or PR-review run was created.                                                                                                  |
|   6 | Local review         | **Yes — full**   | Reviewed #2's local-branch delivery with a real Codex child session, persisted the transcript and approval comment, and closed the card.                                                  |
|   7 | Lead                 | **Partial**      | Interactive lifecycle was exercised through Stop, reload, Start, a new session, and Stop; it was not accepted as a normally completed transcript run.                                     |
|   8 | PR Review            | **Partial**      | The built-in prompt launched, then the session was stopped; it was not accepted as a normally completed review transcript run.                                                            |
|   9 | Custom prompt        | **Partial**      | The stored custom prompt launched, then the session was stopped; it was not accepted as a normally completed transcript run.                                                              |
|  10 | Advanced Planner     | **No**           | Daemon-supervised assignment creation only; no planning task run was proved for this instance.                                                                                            |
|  11 | Advanced Task Runner | **No**           | Daemon-supervised assignment creation only; no implementation task run was proved for this instance.                                                                                      |
|  12 | Advanced Bug triage  | **No**           | Read-only daemon assignment and Role creation only; no bug-triage workload was proved for this instance.                                                                                  |

```gherkin
Feature: Compose the New Agent catalog

  Scenario: Show the 12 clean-workspace templates
    Given the workspace has no additional user-defined roles
    When I open "New Agent"
    Then Behavior contains:
      | Planner      |
      | Coder        |
      | + New role   |
      | Bug-fix      |
      | Review loop  |
      | Local review |
    And Interactive agents contains:
      | Lead          |
      | PR Review     |
      | Custom prompt |
    And Advanced daemon-supervised contains:
      | Planner     |
      | Task Runner |
      | Bug triage  |
    And "PR Review (checkout)" is not displayed
    And a second legacy Lead card is not displayed

  Scenario: Add a dynamic Role card
    Given I created a Role named "docs-assistant"
    And the Role has a readable prompt
    And its task filter is supported
    When I reopen "New Agent"
    Then "docs-assistant" appears as another Behavior card
    And selecting it creates a prompt agent referencing that Role
    And the gallery contains more than 12 cards

  Scenario: Apply common name and backend validation
    Given I selected a creation template
    Then Name is required
    And Name is trimmed and lowercased
    And Name must contain 1 to 100 lowercase letters, numbers, dots,
        underscores, or hyphens
    And Name cannot begin or end with punctuation
    And a model-backed template cannot be submitted without a healthy
        installed backend
```

## Behavior templates

Planner and Coder create a prompt `AgentService` with an attached
`internal.task.ready` binding. New Role lets the operator choose between that
ready-task trigger and the dedicated `internal.task.review` trigger. Bug-fix,
Review loop, and Local review reconcile stable cron bindings for built-in
TypeScript workflows. These are Autonomous agents in the UI and expose Runs
and Info rather than a browser terminal.

### 1. Planner

```gherkin
Scenario: Activate a Behavior Planner
  Given the built-in "plan" Role has a readable prompt
  And its task filter is "needs_plan"
  And the selected backend is healthy
  And prompt-agent is registered from a packaged bundle or an available build toolchain
  When I select Behavior "Planner"
  And enter a unique agent name
  And click "Activate"
  Then Loom creates an enabled prompt AgentService
  And its behavior references Role "plan"
  And it has an attached "internal.task.ready" binding
  And the UI navigates to the binding-backed agent page
  And no repository selector is shown during creation

  When a ready task has no design
  Then the Planner claims that exact task
  And dispatches "local-task-runner" with the Role prompt as data
  And requests patch-back delivery with no pull request
  And success requires a nonempty persisted design
  And the host moves the task to Review and clears its assignee
  And Runs shows the DriverRun and child TaskRun transcript

  When a task already has a design and no "needs-revision" label
  Then the Planner skips it before model execution

  When the model exits successfully without persisting a design
  Then the task is still handed to Review
  But the DriverRun reports "prompt_agent_planner_design_missing"
  And the run is not accepted as successful planning
```

### 2. Coder

```gherkin
Scenario: Activate a Behavior Coder
  Given the built-in "task" Role has a readable prompt
  And its task filter is "has_design"
  And the selected backend is healthy
  When I select Behavior "Coder"
  And enter a unique agent name
  And click "Activate"
  Then Loom creates an enabled prompt AgentService
  And its behavior references Role "task"
  And it has an attached "internal.task.ready" binding

  When a ready task has an approved design
  And it does not have the "needs-revision" label
  Then the Coder claims the task
  And dispatches "local-task-runner"
  And requests local-branch delivery with no pull request

  When the repository supports filesystem branch delivery
  Then the implementation branch is pushed locally
  And the card moves to Review and becomes unassigned
  And external_ref becomes "local-branch:<branch>@<40-lowercase-hex>"
  And Runs shows an exit-zero transcript and code diff

  When local-branch delivery is unavailable but patch-back succeeds
  Then the host closes the task
  And Runs shows the patch-back evidence

  When the task has no design
  Then the Coder skips it without invoking the model

  When the child TaskRun exhausts retries
  Then FleetDB leaves the task Blocked for review
  And prompt-agent does not silently reopen it
```

### 3. New Role

```gherkin
Feature: Activate a newly defined Behavior Role

Scenario: Choose when the Role runs
  Given I select "+ New role"
  Then I must provide:
    | Agent name    |
    | New role name |
    | Role prompt   |
    | Runs when     |
    | AI backend    |
  And "Runs when" offers:
    | Task becomes ready |
    | Task enters Review |

Scenario: Activate a ready-task Role
  Given I selected "Task becomes ready"
  When I enter Role name "docs-assistant"
  And provide a nonempty Role prompt
  And click "Activate"
  Then Loom transactionally creates or exact-reuses the Role
  And stores its prompt as "docs-assistant.md"
  And assigns task filter "has_design"
  And creates an enabled prompt AgentService for that Role
  And attaches an "internal.task.ready" binding
  And every agent wearing that Role shares the Role prompt

  When a designed task becomes ready
  Then the custom Role follows the same claim and delivery lifecycle as Coder

Scenario: Activate and run a Review-triggered documentation Role
  Given I selected "Task enters Review"
  And provided a documentation prompt that may edit repository files
  When I click "Activate"
  Then Loom assigns task filter "review"
  And creates an enabled prompt AgentService for that Role
  And attaches an "internal.task.review" binding

  When an exact non-epic task transitions from a different status into Review
  And the bridge rechecks that the live card is still in Review
  Then Loom emits one "task.review" event containing that task ID
  And the Role claims only that exact Review generation with "claimReview"
  And it does not scan or claim the ready-task queue
  And it retains the Work Item claim while the child TaskRun is active
  And it requires local-branch delivery rather than patch-back fallback

  When the child TaskRun succeeds
  Then Loom atomically hands the task back to Review
  And the task becomes unassigned
  And external_ref becomes "local-branch:<branch>@<40-hex-head>"
  And Runs exposes the real backend transcript and repository diff
  And the Role's own Review handoff does not trigger a second run

Scenario: Resume a Review Role on its existing local branch
  Given the Review card has external_ref "local-branch:<branch>@<40-hex-head>"
  When the Review Role claims that card
  Then the runner fetches the named origin branch
  And requires its head to equal the stamped head exactly
  And starts the isolated worktree at that commit
  And preserves and republishes the same branch name

  When the origin branch head has drifted from the stamped head
  Then the runner fails closed before invoking the model

  When external_ref is nonempty but is not a valid local-branch reference
  Then Loom performs a typed release back to Review
  And no child TaskRun is started

Scenario: Reject an incompatible existing Role
  Given a Role named "docs-assistant" already exists with incompatible fields
  When I activate a new Role with that same name
  Then activation fails with a conflict
  And Loom does not overwrite the operator-owned Role
```

### 4. Bug-fix

```gherkin
Scenario: Activate and run Bug-fix
  Given exactly one selected repository has a GitHub remote
  And Settings contains a decryptable GitHub token
  And the workspace default backend is healthy
  When I select "Bug-fix"
  And choose a cadence
  And click "Activate"
  Then Loom reconciles singleton binding "s1-bug-fix"
  And its workflow is "bug-fix-agent"
  And its input contains the local repo name and GitHub "owner/name"
  And the binding is enabled only after configuration succeeds
  And this modal does not create connector grants

  When the workflow finds a ready issue whose type is "bug"
  Then it claims one bug scoped to the selected repository
  And dispatches "local-task-runner" with a direct-fix prompt
  And requests pull-request delivery
  And success requires runtime metadata containing a GitHub PR URL
  And the card is reopened after the child closes it
  And the card moves to Review
  And external_ref becomes the PR URL

  When no eligible bug exists
  Then the DriverRun completes with "no ready bug to claim"
  But no child transcript exists
  And that empty sweep is not evidence that the fix path works

  When a PR is created but Review linkage fails
  Then the workflow reports needs-review
  And it does not claim complete end-to-end success
```

### 5. Review loop

```gherkin
Scenario: Activate and run the GitHub Review loop
  Given Codex is healthy
  And exactly one selected repository has a GitHub remote
  And Settings contains a decryptable GitHub token
  When I select "Review loop"
  And choose a cadence
  And click "Activate"
  Then Loom reconciles singleton binding "s2-review-loop"
  And ensures the GitHub connector using the saved runtime credential
  And grants only the selected repository:
    | github.pull_request.read |
    | github.compare.read      |
    | github.review.post       |
  And the binding is enabled only after its grants are reconciled

  When a Review card references an open PR in the selected repository
  Then the workflow reads the PR and comparison diff
  And claims the card's exact Review generation
  And runs "github-review-task-runner" through Codex
  And persists "review-cycle:<n>"
  And posts a COMMENT review against the expected head SHA
  And atomically hands the card back to Open for rework

  When the reviewer finds no defects
  Then it still posts a COMMENT review
  And it does not approve or close the PR
  And the card still returns to Open

  When one card fails during a sweep
  Then that card is reported in "skipped"
  And the sweep may still have DriverRun status Completed
  And acceptance inspects reviewed count, child transcript, review URL,
      cycle label, and card state rather than DriverRun status alone

  When the review-cycle cap is reached
  Then the card remains in Review
  And another Codex review is not dispatched
```

### 6. Local review

```gherkin
Scenario: Activate and run Local review
  Given Codex is healthy
  And exactly one repository is selected
  And no GitHub credential is required
  When I select "Local review"
  And choose a cadence
  And click "Activate"
  Then Loom reconciles singleton binding "s3-local-review"
  And its input identifies the selected local repository
  And no GitHub connector or grant is created

  When a Review card has external_ref "local-branch:<branch>@<40-lowercase-hex>"
  Then the workflow retrieves the durable task diff
  And claims the exact Review generation
  And runs "github-review-task-runner" with the diff as data
  And retains the claim until the review handoff is committed

  When blocking findings exist
  Then Loom adds a review comment
  And adds "review-cycle:<n>"
  And returns the card to Open

  When no blocking findings exist
  Then Loom adds an approval comment
  And closes the card

  When no eligible local-branch card exists
  Then the DriverRun completes without a child transcript
  And that empty sweep is not sufficient runtime proof
```

## Interactive templates

Interactive templates create workspace agent assignments with `auto=false`.
Creation navigates to the agent page, where Terminal is the first capability.
The durable run begins when the terminal attaches, not when the assignment row
is created.

Repository chips persist authorization scope. They do not guarantee the
process working directory because interactive creation does not provision a
dedicated worktree.

### 7. Lead

```gherkin
Scenario: Create and run Lead
  Given a healthy installed AI backend is selected
  When I select Interactive "Lead"
  And provide a valid name
  And click "Create Agent"
  Then Loom creates an agent with role_name "lead"
  And auto is false
  And the explicit kind and prompt_file fields are omitted
  And the existing Lead Role identifies it as interactive
  And the UI navigates to its agent page
  And Terminal is selected first

  When the terminal attaches
  Then Loom starts the built-in Lead prompt
  And creates a durable orchestration AgentSession
  And Lead begins with a read-only workspace status sweep
  And Lead asks before mutating tasks, repositories, or agents

  When the backend TUI exits normally
  Then the session becomes Completed
  And finished_at is populated
  And the Codex conversation is persisted as a session-owned transcript
```

### 8. PR Review

```gherkin
Scenario: Create and run interactive PR Review
  Given a healthy installed AI backend is selected
  When I select Interactive "PR Review"
  And click "Create Agent"
  Then Loom creates an interactive agent with role_name "pr-review"
  And prompt_file is "builtin:pr-review"
  And auto is false
  And Terminal opens on its agent page

  When the terminal attaches
  Then the agent asks for a PR URL, PR number, branch, or comparison target
  And it reviews correctness, security, tests, maintainability, and style
  And it asks before posting a GitHub review or making another mutation

  When repository chips were selected
  Then they constrain authorization scope
  But they do not guarantee the terminal's working directory
  And acceptance verifies pwd and the Git repository root

  When the user exits normally after receiving a response
  Then Runs shows a Completed orchestration session
  And its transcript contains the user request and assistant review
  And no generic interactive diff is expected
```

### 9. Custom prompt

```gherkin
Scenario: Create and run a Custom prompt agent
  Given a healthy installed AI backend is selected
  And I select Interactive "Custom prompt"
  Then a nonempty Custom prompt is required

  When I enter agent name "release-checker"
  And enter a literal custom prompt
  And click "Create Agent"
  Then Loom creates an interactive Role named "release-checker"
  And stores the trimmed prompt literally
  And auto is false
  And standard terminal safety rules are appended at launch
  And Terminal opens on its agent page

  When the backend TUI exits normally
  Then Runs shows a Completed orchestration session
  And its transcript contains real user and assistant messages

  When I click Stop instead of exiting normally
  Then Loom kills the owned PTY
  And the agent becomes stopped
  And the active session becomes Cancelled, not Completed
```

## Advanced daemon-supervised templates

Advanced templates create legacy workspace agent assignments with `auto=true`.
The daemon manager owns discovery, task matching, process lifecycle, and
recovery. These workers intentionally have no browser terminal or manual Start
path. Their evidence appears as task-bound `AgentSession` history.

### 10. Advanced Planner

```gherkin
Scenario: Create and run an Advanced Planner
  Given the workspace has at least one non-linked repository
  And a healthy backend is selected
  When I select Advanced "Planner"
  And click "Create Agent"
  Then Loom creates a legacy Agent row with role_name "plan"
  And auto is true
  And the daemon manager discovers the assignment
  And no browser-owned terminal is exposed

  When an Open non-epic task has no design or needs revision
  Then the daemon claims the task for that agent
  And starts "loom plan" in daemon mode
  And records an AgentSession with phase "planning"

  When planning succeeds correctly
  Then a nonempty design is persisted
  And the card becomes Review and unassigned
  And the session becomes Completed
  And a transcript is available
  And no code diff is expected

  But because design and status mutation are prompt-owned
  Then session Completed alone is insufficient evidence
  And acceptance separately asserts the design and Review state
```

### 11. Advanced Task Runner

```gherkin
Scenario: Create and run an Advanced Task Runner
  Given the workspace has at least one non-linked repository
  And a healthy backend is selected
  When I select Advanced "Task Runner"
  And click "Create Agent"
  Then Loom creates a legacy Agent row with role_name "task"
  And auto is true
  And the daemon starts "loom task" in daemon mode for eligible work
  And no browser-owned terminal is exposed

  When an Open task has a design and does not need revision
  Then the daemon claims it
  And records an implementation AgentSession
  And the agent implements, validates, and commits the change

  When GitHub stack publication succeeds
  Then the card becomes Closed
  And Runs shows a Completed session, transcript, and nonempty diff

  When GitHub publication is unavailable but local delivery succeeds
  Then the card becomes Review and unassigned
  And external_ref is "local-branch:<branch>@<40-lowercase-hex>"
  And Runs shows the transcript and diff

  When the design is not implementable
  Then the agent may return the card to Open with "needs-revision"

  When an external dependency blocks implementation
  Then the agent may move the card to Blocked with explanatory evidence
```

### 12. Advanced Bug triage

```gherkin
Scenario: Create and run Advanced Bug triage
  Given the workspace has at least one non-linked repository
  And a healthy backend is selected
  When I select Advanced "Bug triage"
  And click "Create Agent"
  Then Loom exact-ensures a "bug-triage" Role
  And its task_filter is "bug"
  And read_only is true
  And its built-in prompt forbids product-code changes
  And Loom creates an auto daemon assignment for that Role

  When an Open issue has canonical issue_type "bug"
  Then the daemon claims that exact bug
  And rejects a non-bug before invoking the model
  And exports read-only execution policy
  And asks the model for reproduction evidence, likely root cause,
      severity, blast radius, labels, priority, and a triage comment

  When triage succeeds
  Then the card becomes Review
  And no product-code diff is expected
  And the session becomes Completed with a transcript

  When the backend exits zero but the card is not in Review
  Then the host rejects the result
  And the session becomes Failed
  And it remains eligible for recovery rather than being accepted as triaged
```

## Evidence standard

Creating a card, Role, AgentService, Agent assignment, connector, or binding is
creation evidence only. A template passes functionally only when:

1. the UI initiates its intended runtime;
2. a real backend CLI or runner executes;
3. the task or card reaches the template-specific state;
4. the transcript, diff, branch, PR, comment, or review artifact expected by
   that scenario exists; and
5. a nominally Completed empty sweep is not mistaken for an exercised child
   execution path.

Interactive happy-path acceptance uses a normal backend TUI exit, which yields
a Completed session. The Stop control is a separate cancellation scenario.

## Proof status through 2026-07-28

This contract is broader than the currently recorded live proof:

| Evidence level                                            | Templates                                                                            | Count |
| --------------------------------------------------------- | ------------------------------------------------------------------------------------ | ----: |
| Full UI-created real-Codex execution                      | #1 Behavior Planner, #2 Behavior Coder, #3 New Role, #6 Local review                 |     4 |
| Interactive creation/lifecycle only                       | #7 Lead, #8 PR Review, #9 Custom prompt                                              |     3 |
| Creation/configuration only                               | #10 Advanced Planner, #11 Advanced Task Runner, #12 Advanced Bug triage              |     3 |
| Blocked on an authorized GitHub repository and credential | #4 Bug-fix, #5 Review loop                                                           |     2 |

These evidence levels are cumulative across the recorded snapshots; they do
not claim that all four fully exercised templates were rerun at the final
`f0011b248` head. The final current-head package revalidates #6 Local review and
the no-backend fail-closed path into #1 Planner. The earlier #1, #2, and #3
happy-path runs below remain historical proof for the exact packages that ran
them.

The latest full #1 proof before final closure used UI-created agent
`behavior-planner-20260725` in
workspace `AGENTSPROOF-R8-20260724`. It planned task
`AGENTSPROOF-R8-20260724-7` in DriverRun
`automation-run-9a546256687c3650dd0552efe1475f2a`. The child Codex TaskRun
completed with exit 0, persisted a nonempty design, reported zero changed
files, exposed a transcript, and produced no diff. The task finished in Review
and unassigned.

The latest full #3 proof before final closure used UI-created agent
`docs-review-hardened-20260725`, Role
`documentation-review-hardened-20260725`, and binding
`agt-docs-review-hardened-20260725-5b5fb7c8-1` in the same workspace.
Moving task `AGENTSPROOF-R8-20260724-9` from Open to Review emitted exactly
one `task.review` event and one delivery. DriverRun
`automation-run-22c6ffd322bc339ec8abdc1b117cb78c` dispatched real-Codex
TaskRun
`promptagent-automation-run-22c6ffd322bc339ec8abdc1b117cb78c-AGENTSPROOF-R8-20260724-9`.
It completed with exit 0, added the requested five-line documentation file,
and exposed both transcript and diff evidence. The task finished in Review
and unassigned with external reference
`local-branch:loom/AGENTSPROOF-R8-20260724-9@10214f7068d146afa93fdd26a14f0bab8a20c91c`.
After the completion handoff, the task still had exactly one DriverRun, proving
that the Role did not retrigger itself.

Seeded local-mode Planner and Coder runs remain separate verifier evidence;
they are not counted as proof of a UI-created instance. GitHub PR/review
templates still require explicit repository and credential authorization.

The final exact packaged-Desktop closure used Loom `f0011b248` with FleetDB
`de89f0544`. The package contained all six supported built-in workflow bundles
and activated Local review through the visible New Agent UI without a build
toolchain. DriverRun
`automation-run-1415fcb9a8ec97d816f5885d002506f1` launched a real Codex child,
exposed its transcript, and completed exit zero. This run intentionally found
two blocking findings, added the review comment and `review-cycle:1`, and
atomically returned the card to Open. The ready-task bridge then dispatched
the existing Coder, which produced remediation commit
`9eafb37cdfb2f2298e70aceb0479797a503c8656`, followed by the existing
Review-triggered documentation Role. Those downstream claims explain the
subsequent In Progress states; the completed Local review run was neither
stuck nor an approval.

The same exact package was then restarted without an available Codex binary.
UI-created task `PHASE4-EXACT-A7E6BF4CC-2` failed closed within about five
seconds with `local_backend_unavailable`, remained unassigned in Blocked, had
no transcript or diff, and caused no repository mutation. After restarting
again with Codex restored, the task was still Blocked. This proves both the
negative backend contract and state persistence without disturbing the
separately running stack on port 8683.

## Source anchors

- [`CreateAgentModal.tsx`](../../internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx)
- [`agentTemplates.ts`](../../internal/webui/frontend/src/components/CreateAgentModal/agentTemplates.ts)
- [`prompt-agent.ts`](../../internal/infra/workflowdistribution/builtin/prompt-agent.ts)
- [`bug-fix-agent.ts`](../../internal/infra/workflowdistribution/builtin/bug-fix-agent.ts)
- [`review-loop-agent.ts`](../../internal/infra/workflowdistribution/builtin/review-loop-agent.ts)
- [`local-review-agent.ts`](../../internal/infra/workflowdistribution/builtin/local-review-agent.ts)
- [`interactive_prompt.go`](../../internal/domain/interactive_prompt.go)
