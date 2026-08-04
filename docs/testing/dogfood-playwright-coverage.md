# Dogfood to Playwright Coverage

This page maps manual dogfood findings under `dogfood-output/` to automated
coverage. The goal is to promote deterministic product regressions into the
real Playwright stack while keeping true local-mode/Codex execution runs as
manual or harness-level evidence.

## Inputs Compared

| Dogfood run                                                             | Focus                                                                                 |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `dogfood-output/loom-localmode-podman-20260504-1753/report.md`          | Local-mode Podman UI, agent task flow, workspace isolation, terminal/session evidence |
| `dogfood-output/loom-regression-20260505-10issues/regression-report.md` | Retest of the ten local-mode issues                                                   |
| `dogfood-output/fleetdb-ui/report.md`                                   | FleetDB-backed workspace, issue, agent, worktree, files, git, diff, and lifecycle UI  |

## Coverage Matrix

| Finding                                                                            | Current automation                                                                                                                         | Status                                |
| ---------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------- |
| Initial load hydration warning and missing asset `404`s                            | `dogfood-regressions.integration.spec.ts` asserts no console errors, page errors, or failed static resources on real Kanban load           | Covered                               |
| List view failed to load issue data                                                | `dogfood-regressions.integration.spec.ts` loads `/table` in the real stack and asserts an API-created issue row appears                    | Covered                               |
| Search no-match leaves visible cards or clears the query                           | Mocked search tests plus `tests/e2e/integration/dogfood-regressions.integration.spec.ts` real-stack no-match regression                    | Covered                               |
| New Issue in a single-repo workspace creates repo-less issues                      | Component unit coverage plus `dogfood-regressions.integration.spec.ts` real UI create and backend `source_repo` assertion                  | Covered                               |
| Terminal displays raw bracketed-paste control text                                 | `terminal-parity.integration.spec.ts` asserts terminal output does not contain `?2004` in the local integration path                       | Covered outside real smoke/regression |
| Mobile board clipped or CTA overlaps bottom nav                                    | `dogfood-regressions.integration.spec.ts` runs real Kanban at a narrow viewport and asserts no page overflow/CTA clipping                  | Covered                               |
| Empty workspace shows inherited repo or phantom agent                              | `dogfood-regressions.integration.spec.ts` creates an empty workspace and asserts no repos/agents                                           | Covered                               |
| Empty workspace Lead reads another workspace's tasks                               | Local-mode verifier covers daemon/session isolation for seeded tasks; broader Talk-to-Lead setup flow still needs product harness coverage | Partial harness                       |
| Lead setup request fails to create repo/agent/epic/tasks                           | Local-mode verifier covers deterministic daemon/backend execution; Talk-to-Lead repo/agent/epic creation remains a harness target          | Partial harness                       |
| Assigning repo in issue detail changes title to `undefined`                        | `dogfood-regressions.integration.spec.ts` changes repo from the real issue detail panel and asserts the title stays stable                 | Covered                               |
| UI-created epic/tasks remain ungrouped because parent/epic relationship is missing | Swimlane/grouping unit and E2E tests cover rendering; real-stack create/edit parent relationship flow is not covered                       | Pending                               |
| Agent-picked task card moves without page refresh                                  | `sse-updates.integration.spec.ts` smoke test updates status via API and asserts the card moves with no issue-list refetch                  | Covered                               |
| Workspace, repo, agent, worktree, files, git, and diff tabs from FleetDB UI run    | API and component coverage exists in pieces; not all FleetDB UI journeys are in the self-contained browser stack                           | Partial                               |

## Promotion Rule

Promote dogfood findings to Playwright when the scenario is deterministic in
the self-contained FleetDB stack and does not require external Codex execution.
Keep scenarios in the local-mode Podman runbook or a harness suite when they
depend on real agent execution, credentials, worktree side effects, transcripts,
or long-running daemon behavior.

## Real-Stack Suites

| Suite                           | Purpose                                                                    |
| ------------------------------- | -------------------------------------------------------------------------- |
| `make test-e2e-real-smoke`      | Fast FleetDB-backed gate for connection health and no-refresh live updates |
| `make test-e2e-real-regression` | Slower FleetDB-backed browser/API regressions promoted from dogfood        |
| Local-mode Podman dogfood       | End-to-end daemon, agent, transcript, worktree, and Codex backend evidence |

## Next Coverage Targets

1. Expand local-mode harness coverage from seeded planner/coder tasks into the
   Talk-to-Lead setup flow once that flow has a stable non-interactive trigger.
2. Promote the real-stack epic/parent creation flow into Playwright so UI-created
   tasks are grouped under their epic without relying only on rendering tests.
3. Continue promoting FleetDB UI journeys for workspace, agent, worktree, files,
   git, and diff tabs when they are deterministic in the self-contained stack.
