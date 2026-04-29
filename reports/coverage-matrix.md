# Behavioral Contract Validation — Coverage Matrix

_Generated: 2026-04-29T06:04:15Z — 21 contracts, 1741 assertions._

**Column meanings:**
- **Skip** = validation `blocked` (assertion could not be executed because a prerequisite Layer failed or the environment lacked a dependency)
- **Gap** = E2E audit `partial` (a Playwright/Go test exists but does not fully verify the assertion's evidence)
- **E2E Covered** / **E2E Uncovered** = strict covered / uncovered from the E2E audit (excludes Gap)
- **Coverage %** = `E2E Covered / Pass × 100`

| Contract | Total | Pass | Fail | Skip | Deferred | Gap | E2E Covered | E2E Uncovered | Coverage % |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| frontend/agent-detail-terminals.md | 107 | 62 | 0 | 43 | 2 | 11 | 32 | 19 | 51.6% |
| cross-area/auth-flow.md | 58 | 33 | 3 | 22 | 0 | 3 | 25 | 5 | 75.8% |
| cross-area/e2e-agent-lifecycle.md | 75 | 49 | 4 | 20 | 2 | 19 | 27 | 3 | 55.1% |
| frontend/files-editor.md | 85 | 52 | 2 | 31 | 0 | 4 | 0 | 48 | 0.0% |
| frontend/filters-bulk.md | 97 | 61 | 5 | 29 | 2 | 9 | 34 | 18 | 55.7% |
| frontend/graph.md | 100 | 55 | 36 | 9 | 0 | 8 | 16 | 31 | 29.1% |
| cross-area/integration-misc.md | 30 | 10 | 1 | 17 | 2 | 1 | 5 | 4 | 50.0% |
| frontend/issue-detail-crud.md | 131 | 66 | 10 | 55 | 0 | 8 | 34 | 24 | 51.5% |
| frontend/kanban.md | 111 | 81 | 6 | 24 | 0 | 14 | 17 | 50 | 21.0% |
| frontend/monitor-agents.md | 115 | 23 | 2 | 90 | 0 | 4 | 15 | 4 | 65.2% |
| cross-area/multi-agent-parallel.md | 64 | 43 | 1 | 20 | 0 | 14 | 27 | 2 | 62.8% |
| cross-area/multi-workspace-fleet.md | 86 | 45 | 4 | 37 | 0 | 9 | 34 | 2 | 75.6% |
| cross-area/operability-slo.md | 66 | 46 | 8 | 7 | 5 | 8 | 33 | 5 | 71.7% |
| cross-area/performance-slo.md | 42 | 5 | 1 | 35 | 1 | 5 | 0 | 0 | 0.0% |
| cross-area/security-hardening.md | 55 | 49 | 1 | 4 | 1 | 9 | 15 | 25 | 30.6% |
| frontend/settings-obs-usage.md | 115 | 42 | 3 | 68 | 2 | 6 | 2 | 34 | 4.8% |
| frontend/shell-auth-errors.md | 145 | 42 | 0 | 94 | 9 | 9 | 19 | 14 | 45.2% |
| cross-area/sse-to-ui.md | 38 | 16 | 7 | 15 | 0 | 8 | 4 | 4 | 25.0% |
| frontend/table.md | 90 | 59 | 13 | 18 | 0 | 6 | 15 | 38 | 25.4% |
| cross-area/upgrade-disaster-multi-tenant.md | 41 | 20 | 10 | 3 | 8 | 4 | 12 | 4 | 60.0% |
| frontend/workspace-ui.md | 90 | 46 | 6 | 37 | 1 | 8 | 28 | 10 | 60.9% |
| **TOTAL** | **1741** | **905** | **123** | **678** | **35** | **167** | **394** | **344** | **43.5%** |
