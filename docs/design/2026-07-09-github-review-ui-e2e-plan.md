# Live GitHub Review from the UI

**Status:** Proposed implementation plan  
**Date:** 2026-07-09  
**Target:** A user can provision and run the `github-review-agent`, observe its
run, and open real inline GitHub feedback without invoking a shell script.

## Outcome

The product path should be:

1. Save a GitHub token in Loom Settings.
2. Create a **GitHub PR reviewer** agent for one repository.
3. Open a PR in Loom and click **Review now**.
4. Follow the standard agent run/session view while it executes.
5. See a completed result with the GitHub review link and inline-comment count.

Real GitHub webhooks should invoke the same binding automatically when Loom has
a public ingress URL. The manual UI action is the fastest local/desktop path
because GitHub cannot deliver a webhook to `localhost`.

The shell scripts remain live regression tooling. They stop being required for
normal product use.

## What Exists

| Capability | Current state |
|---|---|
| GitHub token entry | Settings stores a sealed runtime credential. |
| Workflow | Builtin `github-review-agent` and generic review task runner. |
| Connector | UI API can create a GitHub connector by reusing the Settings token. |
| Policy grants | UI API can grant PR read, compare read, and review post per binding/repo. |
| Trigger binding | UI API can create GitHub bindings and self-heal the builtin workflow. |
| Run visibility | Trigger-binding agent detail already shows run history and live status. |
| PR surface | PR queue/review workspace exists, but it does not invoke this workflow. |
| Create-agent UI | It exposes scheduled `review-loop-agent`, not event-driven `github-review-agent`. |
| Local ingress | The live script synthesizes a signed webhook; the UI has no equivalent action. |
| Feedback proof | The workflow returns a review ID/URL; it now also returns inline-comment count. |

## Architecture

Use one `Agent`/trigger binding as the stable reviewer identity for a repository.
Both invocation sources project into the same execution path:

```text
GitHub webhook ─┐
                ├─> TriggerBinding(github-review-agent)
Review now UI ──┘       -> DriverRun -> TaskRun -> AgentSession
                              -> github.review.post -> inline comments
```

The UI must not call GitHub directly and must not invoke Codex directly. The
backend creates an auditable manual trigger delivery associated with the same
binding. The workflow continues to use Loom connector grants, credentials,
head-SHA freshness checks, retry policy, and idempotency.

The manual action is a distinct invocation source, not a fake signed webhook:

- source kind: `manual`
- source reference: authenticated user + PR identity
- subject key: `owner/repo#number`
- target: the repository's GitHub reviewer binding
- payload: server-read current PR identity (`repo`, number, head SHA, base ref,
  draft state), never client-authoritative SHA/state

## Delivery Plan

### 1. Provision the reviewer from Create Agent

Add a **GitHub PR reviewer** behavior card backed by
`github-review-agent`. The form asks for:

- display name;
- target repository;
- trigger mode: manual only, or manual + GitHub webhook;
- optional review instruction override later (not required for v1).

On submit, one backend transaction should:

1. ensure the GitHub connector using the Settings credential;
2. create the binding with the standard PR event patterns;
3. grant exactly `github.pull_request.read`, `github.compare.read`, and
   `github.review.post` on `repo:owner/name`;
4. return the durable agent/binding record.

Do not leave the current three independent frontend mutations as the final
contract. Add a purpose-specific ensure endpoint so failure cannot leave a
half-provisioned connector, binding, or grant set.

### 2. Add the manual UI invocation

Add:

```text
POST /api/workspaces/{ws}/pull-requests/{owner}/{repo}/{number}/review-runs
```

The handler:

1. resolves the enabled reviewer binding scoped to that repository;
2. reads the current PR on the server and pins its head SHA;
3. creates a manual trigger event/delivery for that binding;
4. applies the binding's replace-per-subject concurrency policy;
5. returns the `DriverRun` ID and agent/binding identity.

The PR page gets **Review now**. After submission it navigates to the standard
workflow-agent detail view rather than introducing a GitHub-specific transcript
UI.

### 3. Surface useful completion evidence

On the agent detail and PR page, render the terminal run output:

- review status;
- reviewed head SHA;
- top-level summary;
- inline-comment count;
- **Open review on GitHub** link;
- stale/draft skip reason or error class.

Do not label a run successful merely because GitHub created a `COMMENTED`
review. For the sandbox acceptance PR, success requires at least one actual
review-comment record with path, line, and body.

### 4. Add real webhook onboarding

For deployed Loom instances with public ingress, the reviewer detail page shows:

- webhook URL;
- generated secret with copy/rotate controls;
- required GitHub event: pull requests;
- delivery health and last verified delivery.

Fast v1: the user copies URL and secret into GitHub repository settings.

Later: add a policy-governed `github.webhook.ensure` connector action or GitHub
App installation flow. Do not have the browser create repository webhooks with
the raw token. Local desktop should eventually use a Loom relay/GitHub App,
not require users to operate an ad-hoc tunnel.

### 5. Browser-driven live acceptance

The product E2E should drive only visible UI actions:

1. open Settings and save the sandbox GitHub token;
2. create a GitHub PR reviewer for `tysonthomas9/loom-review-sandbox`;
3. open sandbox PR 1 and click **Review now**;
4. follow the created run until terminal;
5. assert the UI shows a nonzero inline count and GitHub review URL;
6. verify GitHub exposes at least one line comment belonging to that review.

Evidence class: live external E2E (real Codex invocation, real GitHub mutation).
The deterministic UI suite should mock only the external boundary and verify
the provisioning transaction, run creation, navigation, and terminal rendering.

## Recommended Stack Order

1. Current inline-comment connector fix and stronger live assertion.
2. Transactional reviewer-provisioning endpoint plus Create Agent card.
3. Manual review-run endpoint plus PR-page **Review now** action.
4. Terminal result rendering and browser live E2E.
5. Public webhook onboarding and delivery health.
6. Optional GitHub App/automatic webhook registration.

Steps 2–4 are the shortest path to a complete local UI flow. They reuse the
existing agent, binding, connector, grant, execution, and transcript models;
no new agent runtime model is needed.
