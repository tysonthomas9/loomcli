# Agent Creation Templates

**Status:** Current implemented contract, with exact packaged-runtime proof status recorded separately

**Date:** 2026-08-05

**Scope:** The user-visible templates in the web and desktop **New Agent**
modal

**Related:** [Create Agent redesign](../design/create-agent-redesign.md),
[Unified agent UX](../design/2026-07-01-unified-agent-ux-proposal.md),
[Agent execution PRD](agent-execution-prd.md), and
[Phase 4 evidence](../migrations/modular-monolith/09-phase-4-decisions-and-evidence.md).
The historical Phase 5 rerun is defined by the
[24-execution packaged Desktop proof](../migrations/modular-monolith/12-phase-5-real-codex-proof.md).
The current catalog and its two-runs-per-local-template acceptance are recorded
in the [Phase 7 evidence](../migrations/modular-monolith/14-phase-7-decisions-and-evidence.md).

## How to read the count

The clean-workspace count of 10 is a presentation-layer composition, not 10
implementations of one runtime:

- seven **Behavior** templates; and
- three **Interactive** templates.

Behavior templates create either an event-triggered prompt `AgentService` or a
cron `TriggerBinding`. Interactive templates create workspace agent
assignments with `auto=false` and are browser-launched. Phase 6 retired
the three Advanced daemon-supervised templates together with their supervisor;
they are historical catalog entries, not creatable Phase 7 agents.

Runnable user-defined Roles add dynamic Behavior cards, so a workspace can
show more than 10. The server also registers the hidden
`pr-review-checkout` prompt, but that prompt is not a gallery card. The legacy
Advanced Lead card is suppressed because the Interactive Lead card replaces
it.

## Which numbered templates actually ran

Eight local templates each completed two UI-created real-Codex executions in
the Phase 7 package. Bug-fix and Review loop each retain two authorized GitHub
execution rows, waived by the operator because this proof intentionally did not
mutate GitHub.

|   # | Template         | Phase 7 disposition | Evidence boundary |
| --: | ---------------- | ------------------- | ----------------- |
|   1 | Behavior Planner | **2 passed** | Two tasks persisted nonempty designs, reached Review, and exposed exit-zero transcripts with no diff. |
|   2 | Behavior Coder | **2 passed** | Two designed tasks produced local-branch delivery with exit-zero transcript and repository evidence. |
|   3 | Behavior Bug triage | **2 passed** | Two bug cards received root-cause/priority evidence, reached Review, and produced no code diff. |
|   4 | New Role | **2 passed** | The Review-triggered documentation Role ran twice, delivered local branches, exposed transcript/diff, and did not self-trigger. |
|   5 | Bug-fix | **2 waived** | Requires an authorized GitHub repository and credential; the operator waived GitHub mutations. |
|   6 | Review loop | **2 waived** | Requires an authorized GitHub repository and credential; the operator waived GitHub mutations. |
|   7 | Local review | **2 passed** | Two selected local-branch cards launched real Codex review children with transcripts and policy-owned outcomes. |
|   8 | Lead | **2 passed** | Two browser-owned Codex sessions exited normally and remained visible as Completed durable sessions. |
|   9 | PR Review | **2 passed** | Two local-target review conversations exited normally with persisted user/assistant transcripts and no external mutation. |
|  10 | Custom prompt | **2 passed** | Two sessions obeyed the literal custom prompt, exited normally, and persisted Completed transcripts. |

```gherkin
Feature: Compose the New Agent catalog

  Scenario: Show the 10 clean-workspace templates
    Given the workspace has no additional user-defined roles
    When I open "New Agent"
    Then Behavior contains:
      | Planner      |
      | Coder        |
      | Bug triage   |
      | + New role   |
      | Bug-fix      |
      | Review loop  |
      | Local review |
    And Interactive agents contains:
      | Lead          |
      | PR Review     |
      | Custom prompt |
    And "PR Review (checkout)" is not displayed
    And no Advanced daemon-supervised card is displayed

  Scenario: Add a dynamic Role card
    Given I created a Role named "docs-assistant"
    And the Role has a readable prompt
    And its task filter is supported
    When I reopen "New Agent"
    Then "docs-assistant" appears as another Behavior card
    And selecting it creates a prompt agent referencing that Role
    And the gallery contains more than 10 cards

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

### 3. Bug triage

```gherkin
Scenario: Activate and run Behavior Bug triage
  Given the built-in "bug-triage" Role is absent or exact-compatible
  And a healthy backend is selected
  When I select Behavior "Bug triage"
  And click "Activate"
  Then Loom exact-ensures a "bug-triage" Role
  And its task_filter is "bug"
  And read_only is true
  And its built-in prompt forbids product-code changes
  And Loom creates an enabled prompt AgentService
  And attaches an "internal.task.ready" binding

  When an Open issue has canonical issue_type "bug"
  Then the agent claims that exact bug
  And rejects a non-bug before invoking the model
  And asks for reproduction evidence, likely root cause, severity,
      blast radius, labels, priority, and a triage comment

  When triage succeeds
  Then the card has the "triaged" handoff marker
  And the card becomes Review and unassigned
  And no product-code diff is expected
  And Runs exposes a Completed DriverRun and real backend transcript

  When the backend exits zero but the handoff contract is incomplete
  Then the run is not accepted as successful triage
  And the card remains eligible for policy-owned recovery
```

### 4. New Role

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

### 5. Bug-fix

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

### 6. Review loop

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

### 7. Local review

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

### 8. Lead

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

### 9. PR Review

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

### 10. Custom prompt

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

## Retired Advanced templates

Phase 6 removed Advanced Planner, Advanced Task Runner, and Advanced Bug
triage together with the daemon/supervisor runtime that owned them. Planner,
Coder, and Bug triage now enter through the Behavior templates above, so the UI
must not recreate a second `auto=true` assignment or display an Advanced
section.

```gherkin
Scenario: Keep retired Advanced templates absent
  Given the packaged product is Phase 6 or later
  When I open "New Agent"
  Then no Advanced section is shown
  And no template creates a daemon-supervised auto assignment
  And planning, coding, and bug triage remain available as Behavior templates
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

## Proof status through 2026-08-05

The Phase 7 packaged-Desktop proof selected two completed real-Codex
executions for every local template: 16 passes across eight templates. The
four execution rows belonging to the two GitHub-mutating templates were
operator-waived. The three former Advanced templates are retired and are not
acceptance rows.

| Evidence level | Templates | Execution rows |
| -------------- | --------- | -------------: |
| Full UI-created real-Codex execution | #1 Planner, #2 Coder, #3 Bug triage, #4 New Role, #7 Local review, #8 Lead, #9 PR Review, #10 Custom prompt | 16 passed |
| Authorized GitHub mutation waived | #5 Bug-fix, #6 Review loop | 4 waived |
| Retired in Phase 6 | Advanced Planner, Advanced Task Runner, Advanced Bug triage | N/A |

Selected durable run identities include:

- Planner: `automation-run-fddd2a2e0a5592c7f3636ad6ef88beb1` and
  `automation-run-9a0f429fff5b1c5a802999df639e872d`.
- Coder: `automation-run-a6b3c6b7d85f0630523cc1e0189a9e0b` and
  `automation-run-3cad1c8a6cf6ebfedaf5e24d67fc9d46`.
- Bug triage: `automation-run-2a5f8dfa25de1950f330e3c97d0e0dfc` and
  `automation-run-cdf01ca75148949bbb03f187087eb310`.
- Review-triggered New Role:
  `automation-run-6149786fcbc8bd133cbaea615b1c338e` and
  `automation-run-488655a1f3182f166caccdfb7a050c1a`.
- Local review: the selected task 1 and task 2 real-Codex children under
  parent `automation-run-c70bf90ad179d43543b591f96fcd493a`.
- Lead: `session-311ef6c8-4c4a-4722-874f-0a0395e90147` and
  `session-2f535fc6-9352-40fb-9e2a-26ac14865462`.
- PR Review: `session-2ce6545c-9c1b-4168-9540-eb40187d86a6` and
  `session-076b3639-4741-4274-9853-4066e1815219`.
- Custom prompt: `session-149eac19-f60f-44f6-903b-930f0814dd53` and
  `session-ebf25836-b952-4b5a-bbfa-516cb2e60c3e`.

The same product proof created Planner agent
`agt-phase7-codex-unavailable-planner-d8836e85`, temporarily made both Codex
installations unavailable, and dispatched task `PHASE7-PROOF-20260804-8`.
DriverRun `automation-run-37a1c0219b9cabf07b6f02d0b106e946` failed in 1.6
seconds with `local_backend_unavailable`; the task remained unassigned in
Blocked with no transcript or diff. Restarting the app preserved that exact
state, and both Codex binaries were restored immediately after the probe.

## Source anchors

- [`CreateAgentModal.tsx`](../../internal/webui/frontend/src/components/CreateAgentModal/CreateAgentModal.tsx)
- [`agentTemplates.ts`](../../internal/webui/frontend/src/components/CreateAgentModal/agentTemplates.ts)
- [`prompt-agent.ts`](../../internal/infra/workflowdistribution/builtin/prompt-agent.ts)
- [`bug-fix-agent.ts`](../../internal/infra/workflowdistribution/builtin/bug-fix-agent.ts)
- [`review-loop-agent.ts`](../../internal/infra/workflowdistribution/builtin/review-loop-agent.ts)
- [`local-review-agent.ts`](../../internal/infra/workflowdistribution/builtin/local-review-agent.ts)
- [`interactive_prompt.go`](../../internal/domain/interactive_prompt.go)
