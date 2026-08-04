# Dogfood to Playwright Coverage

> **Status:** Partially stale — the `dogfood-output/` corpus this page maps
> *from* is no longer in the repo, so the Finding column is historical and no
> longer auditable. Only one of the three source reports
> (`dogfood-output/fleetdb-ui/report.md`) was ever tracked; it was removed from
> git on 2026-05-07 (`fdb709d61`). The other two were untracked working
> artifacts that were never committed. The automation column, the promotion rule
> and the real-stack suites are live and verified. *audited 2026-07-24*

This page maps manual dogfood findings to automated coverage. The goal is to
promote deterministic product regressions into the real Playwright stack while
keeping true local-mode/Codex execution runs as manual or harness-level
evidence.

## Inputs Compared (historical)

These three reports were produced by manual dogfood sessions in May 2026 and
are no longer in the repo. Their findings are summarised in the Coverage Matrix
below; the reports themselves cannot be re-read. Only `fleetdb-ui/report.md` was
ever committed (removed in `fdb709d61`, 2026-05-07); the two local-mode reports
were untracked working artifacts and never appear in git history.

| Dogfood run                                                             | Focus                                                                                 |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `dogfood-output/loom-localmode-podman-20260504-1753/report.md` (never tracked) | Local-mode Podman UI, agent task flow, workspace isolation, terminal/session evidence |
| `dogfood-output/loom-regression-20260505-10issues/regression-report.md` (never tracked) | Retest of the ten local-mode issues                                  |
| `dogfood-output/fleetdb-ui/report.md` (deleted in `fdb709d61`)          | FleetDB-backed workspace, issue, agent, worktree, files, git, diff, and lifecycle UI  |

New dogfood runs come from the `dogfood` skill (`.claude/skills/dogfood/`) or
the local-mode runbook ([local-mode-podman-e2e.md](local-mode-podman-e2e.md));
their output is a working artifact, not a tracked one.

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

| Suite                           | Command | Purpose                                                                    |
| ------------------------------- | --- | -------------------------------------------------------------------------- |
| Real smoke      | `make test-e2e-real-smoke` (`Makefile:401`) | Fast fleet-db-backed gate for connection health and no-refresh live updates (SSE) |
| Real regression | `make test-e2e-real-regression` (`Makefile:411`) | Slower fleet-db-backed browser/API regressions promoted from dogfood        |
| Local-mode Podman dogfood       | `make local-mode-up` (`Makefile:168`) + `make local-mode-verify` (`Makefile:206`) | End-to-end daemon, agent, transcript, and worktree evidence against the **deterministic** `localdogfood` agent-backend (`test/local-mode/docker-compose.yml:80`) — not real Codex |

Real-Codex evidence is a **separate** stack, not `local-mode-up`: run
`make local-mode-codex-up` (`Makefile:174`) + `make local-mode-codex-verify`
(`Makefile:224`), which layers `test/local-mode/docker-compose.codex.yml`
(`LOOM_BACKEND: codex`, `:14`) over the base compose. `make local-mode-verify`
exercises only the deterministic `localdogfood` backend.

**How a spec joins a suite.** Both real-stack targets run
`RUN_INTEGRATION_TESTS=1 npx playwright test` against Playwright projects that
select by tag, not by filename: `integration-smoke` and `api-smoke` grep
`/@smoke/` (`playwright.config.ts:158`, `:224`), `integration-regression` and
`api-regression` grep `/@regression/` (`:176`, `:237`). To promote a spec, tag
its `test(...)` title. The `-local` variants of both targets
(`Makefile:406`, `:416`) run the same projects with `LOOM_LOCAL_SERVER=1`
against a `loom serve` you started yourself.

"Live updates" here means SSE delivered a change without a page refresh — not
the `live` evidence class, which means a real external/paid service. See
[../testing-terminology.md](../testing-terminology.md) §Trap words.

## Next Coverage Targets

1. Expand local-mode harness coverage from seeded planner/coder tasks into the
   Talk-to-Lead setup flow once that flow has a stable non-interactive trigger.
2. Promote the real-stack epic/parent creation flow into Playwright so UI-created
   tasks are grouped under their epic without relying only on rendering tests.
3. Continue promoting FleetDB UI journeys for workspace, agent, worktree, files,
   git, and diff tabs when they are deterministic in the self-contained stack.

## Related

- [local-mode-podman-e2e.md](local-mode-podman-e2e.md) — the harness that produces new dogfood evidence
- [frontend-tests.md](frontend-tests.md) — where the Playwright specs live
- [test-infrastructure.md](test-infrastructure.md) — the Playwright project table and CI wiring
- [fleetdb-acceptance-gates.md](fleetdb-acceptance-gates.md) — G2/G3 name these suites
