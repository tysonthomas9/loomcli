# Frontend Tests

Complete breakdown of all frontend test files (Vitest unit tests + Playwright E2E tests).

**Location**: `internal/webui/frontend/`

---

## Table of Contents

1. [Testing Framework](#testing-framework)
2. [Unit Tests (Vitest)](#unit-tests-vitest)
   - [API Layer](#api-layer-tests)
   - [Custom Hooks](#custom-hook-tests)
   - [Components](#component-tests)
   - [Utilities & Types](#utility--type-tests)
3. [E2E Tests (Playwright)](#e2e-tests-playwright)
   - [Browser E2E](#browser-e2e-tests)
   - [API E2E](#api-e2e-tests)
   - [Integration Tests](#integration-tests)
4. [Test Infrastructure](#test-infrastructure)

---

## Testing Framework

| Tool | Purpose | Version |
|------|---------|---------|
| Vitest | Unit/integration test runner | v4.0.18 |
| React Testing Library | Component rendering/querying | v16.3.2 |
| Playwright | Browser E2E automation | v1.58.0 |
| jsdom | DOM simulation for unit tests | (via Vitest) |

### Configuration Files

- **Vitest**: `vite.config.ts` (lines 60-64) - test environment, globals
- **Playwright**: `playwright.config.ts` - projects, base URLs, CI settings

### Test Commands

```json
{
  "test": "vitest run && playwright test",
  "test:unit": "vitest run",
  "test:watch": "vitest",
  "test:e2e": "playwright test",
  "test:e2e:ui": "playwright test --ui",
  "test:e2e:headed": "playwright test --headed",
  "test:e2e:debug": "playwright test --debug",
  "test:visual": "playwright test visual-regression",
  "test:visual:update": "playwright test visual-regression --update-snapshots",
  "test:e2e:integration": "RUN_INTEGRATION_TESTS=1 playwright test --project=integration",
  "test:e2e:api": "RUN_INTEGRATION_TESTS=1 playwright test --project=api"
}
```

---

## Unit Tests (Vitest)

### API Layer Tests

#### `src/api/__tests__/agents.test.ts`

**Purpose**: Tests the agent API module (fetchAgents, agent status endpoints).

**Why**: Agent data drives the monitoring dashboard.

#### `src/api/__tests__/config.test.ts`

**Purpose**: Tests the configuration API module (daemon config read/write).

**Why**: Config API is used by the settings view.

#### `src/api/issues.test.ts`

**Purpose**: Tests the issues API module (CRUD operations, list filtering, dependency management).

**Why**: Issues API is the core data layer for the entire frontend.

#### `src/api/client.test.ts` (~750 lines)

**Purpose**: Tests the HTTP client that communicates with the Loom API.

| Test Area | What It Validates |
|---|---|
| Basic requests | GET, POST, PATCH, DELETE with correct headers |
| Authentication | API key attached to requests, auth header format |
| Retry logic | Automatic retry on 5xx errors, exponential backoff |
| Timeout handling | Request timeout enforcement, AbortController cleanup |
| AbortSignal support | Request cancellation propagation |
| Error responses | 4xx/5xx error parsing and throwing |
| Base URL configuration | Correct URL construction |

**Why**: Every API call flows through this client. Auth, retry, and timeout bugs affect all API operations.

#### `src/api/sse.test.ts` (~629 lines)

**Purpose**: Tests the SSE client that handles real-time event streams.

| Test Area | What It Validates |
|---|---|
| EventSource lifecycle | Connection open, close, error states |
| Event parsing | Message format, event type extraction |
| Reconnection logic | Auto-reconnect with Last-Event-ID |
| Event ID tracking | Tracks last event ID for resume |
| State management | CONNECTING, OPEN, CLOSED states |
| Callback invocation | onMutation, onError, onOpen callbacks |

**Why**: SSE is the real-time update mechanism. Reconnection bugs cause the UI to show stale data after network blips.

**Patterns**: Custom `MockEventSource` class with static constants (CONNECTING, OPEN, CLOSED), `simulateOpen()`, `simulateError()`, `simulateMutation()` helpers.

---

### Custom Hook Tests

#### `src/hooks/useAgents.test.ts` (~560 lines)

**Purpose**: Tests the agent polling hook that fetches agent status.

| Test Area | What It Validates |
|---|---|
| Retry scheduling | Exponential backoff timing |
| Countdown timer | Visual countdown between retries |
| Race condition prevention | Timer deduplication (no double-scheduling) |
| Error handling | Failed fetch recovery |
| Cleanup | Timer cleanup on unmount |

**Why**: Agent polling drives the monitoring dashboard. Race conditions cause memory leaks (orphaned timers) and duplicate requests.

**Patterns**: `vi.useFakeTimers()`, `vi.advanceTimersByTime()`, spy on `setTimeout`/`setInterval`.

#### `src/hooks/useBlockedChain.test.ts`

**Purpose**: Tests dependency graph traversal for blocked issues.

| Test Area | What It Validates |
|---|---|
| Dependency graph traversal | Finds all transitive blockers |
| Circular dependency handling | Doesn't infinite-loop on cycles |
| Blocker/blocked computation | Correct sets for each issue |

**Why**: Blocked chain visualization helps users understand why work is stuck. Graph bugs cause wrong or missing dependency information.

#### `src/hooks/useIssues.test.ts` (~1000 lines)

**Purpose**: Tests the primary data hook that manages issue state.

| Test Area | What It Validates |
|---|---|
| Issue fetching | Initial load, error handling, loading states |
| SSE integration | Live updates merged into local state |
| Optimistic updates | Immediate UI update before API response |
| Rollback on failure | Reverts optimistic update if API fails |
| Race condition prevention | SSE mutations during refetch preserved |
| Functional state updates | Prevents snapshot clobbering |

**Why**: This is the most complex hook - it syncs server state, SSE events, and optimistic updates. Race conditions here cause data loss or stale UI.

**Patterns**: `renderHook()`, `act()`, `waitFor()`, mock API + mock SSE.

#### `src/hooks/useSSE.test.ts` (~744 lines)

**Purpose**: Tests the SSE connection management hook.

| Test Area | What It Validates |
|---|---|
| Auto-connect | Connects on mount |
| State management | Connection state transitions |
| Reconnection | Auto-reconnect on error |
| Event dispatching | Routes events to correct handlers |
| Cleanup | Closes connection on unmount |

**Why**: SSE hook manages the persistent connection to the server. Cleanup bugs cause connection leaks.

#### `src/hooks/useDragEnd.test.ts`

**Purpose**: Tests drag-and-drop validation for Kanban board.

| Test Area | What It Validates |
|---|---|
| Valid drops | Card moved to valid column triggers status update |
| Invalid drops | Card dropped on same column is ignored |
| Status mapping | Column maps to correct issue status |

**Why**: Drag-and-drop is the primary Kanban interaction. Incorrect status mapping corrupts issue state.

#### `src/hooks/useDebounce.test.ts`

**Purpose**: Tests debouncing hook for search/filter inputs.

**Why**: Debounce prevents excessive API calls while typing. Too short = wasted calls; too long = sluggish UI.

#### `src/hooks/useSelection.test.ts`

**Purpose**: Tests multi-select behavior for bulk operations.

**Why**: Selection state must be consistent for bulk actions (close, move, delete).

#### `src/hooks/useSort.test.ts`

**Purpose**: Tests sort state management for table views.

**Why**: Sort must be stable and handle all data types correctly.

#### `src/hooks/useGraphData.test.ts`

**Purpose**: Tests graph data transformation for dependency visualization.

**Why**: Graph data must correctly represent issue relationships for the dependency graph view.

#### Additional Hook Tests

| File | What It Tests |
|---|---|
| `src/hooks/__tests__/useAgentContext.test.tsx` | Agent context provider and consumer |
| `src/hooks/__tests__/useBackendConfig.test.ts` | Backend configuration loading and caching |
| `src/hooks/__tests__/useFilterState.test.ts` | Filter state management and URL sync |
| `src/hooks/__tests__/useRecentAssignees.test.ts` | Recently-used assignee tracking |
| `src/hooks/__tests__/useStats.test.ts` | Project statistics fetching |
| `src/hooks/__tests__/useToast.test.tsx` | Toast notification context and dispatching |
| `src/hooks/__tests__/useRouteView.test.ts` | Route-backed view state (Kanban/table/graph) |
| `src/hooks/useAutoLayout.test.ts` | Graph auto-layout algorithm |
| `src/hooks/useBlockedIssues.test.ts` | Blocked issue filtering and display |
| `src/hooks/useBulkClose.test.ts` | Bulk close operation for multiple issues |
| `src/hooks/useBulkPriority.test.tsx` | Bulk priority change operation |
| `src/hooks/useFallbackPolling.test.ts` | Fallback polling when SSE is unavailable |
| `src/hooks/useFilteredSelection.test.ts` | Selection filtered by current view |
| `src/hooks/useIssueDetail.test.ts` | Single issue detail fetching |
| `src/hooks/useIssueFilter.test.ts` | Issue filtering logic (status, priority, type) |
| `src/hooks/useLogStream.test.ts` | Agent log streaming via WebSocket |
| `src/hooks/useOptimisticStatusUpdate.test.ts` | Optimistic status updates with rollback |

---

### Component Tests

#### `src/components/IssueCard/__tests__/IssueCard.test.tsx` (~987 lines)

**Purpose**: Comprehensive tests for the issue card component displayed in Kanban columns.

| Test Area | What It Validates |
|---|---|
| Basic rendering | Title, ID, priority badge, type badge |
| Priority display | P0-P4 with correct colors and labels |
| Accessibility | ARIA labels, role attributes, keyboard navigation |
| Blocked badge | Shows when issue has blockers |
| Review badge | Shows when issue needs review |
| Deferred state | Visual distinction for deferred issues |
| Click handling | Card selection, detail panel opening |
| Truncation | Long titles truncated properly |

**Why**: IssueCard is the most rendered component (appears for every issue). Visual bugs affect the entire UI.

**Patterns**: `screen.getByRole()`, `screen.getByLabelText()` (accessibility-first queries), `userEvent` for interactions.

#### `src/components/App.test.tsx`

**Purpose**: Tests the root App component.

| Test Area | What It Validates |
|---|---|
| Initial render | App loads without crashing |
| Route handling | Correct view rendered per route |
| Error boundaries | Errors caught and displayed |
| Loading states | Skeleton shown during data fetch |

**Why**: If App breaks, nothing works.

**Patterns**: Heavy mocking (GraphView, MonitorDashboard, TerminalPanel mocked to avoid browser dependency issues in jsdom).

#### `src/components/KanbanBoard.test.tsx` / `SwimLaneBoard.test.tsx`

**Purpose**: Tests board view rendering and interaction.

| Test Area | What It Validates |
|---|---|
| Column rendering | Correct columns for status values |
| Card placement | Issues in correct columns |
| Empty columns | Empty state display |
| Drag-and-drop | DnD context and handlers |

**Why**: Board views are the primary work management interface.

#### `src/components/IssueDetailPanel.test.tsx`

**Purpose**: Tests the detail panel shown when clicking an issue.

| Test Area | What It Validates |
|---|---|
| Field display | All issue fields rendered correctly |
| Comment form | Comment creation and submission |
| Dependency section | Blocked by / blocks lists |
| Markdown rendering | Description markdown parsed correctly |
| Edit mode | Inline editing of fields |

**Why**: Detail panel is the primary issue editing interface.

#### `src/components/MonitorDashboard.test.tsx`

**Purpose**: Tests the agent monitoring dashboard.

| Test Area | What It Validates |
|---|---|
| Agent activity | Active agent cards |
| Blocking dependencies | Canvas-rendered dependency graph |
| Metrics display | Stats and health indicators |
| Auto-refresh | Data polling and update |

**Why**: Monitor dashboard is how users track agent progress.

#### All Component Tests (Complete Listing)

| Component | Test File | What It Tests |
|---|---|---|
| AgentCard | `AgentCard/AgentCard.test.tsx` | Agent status card rendering, activity indicators |
| AgentRow | `IssueCard/__tests__/AgentRow.test.tsx` | Agent row within issue cards |
| AgentRow (issue) | `IssueCard/__tests__/IssueCard.agentRow.test.tsx` | Agent row integration in issue card context |
| AppLayout | `AppLayout/__tests__/AppLayout.test.tsx` | Main layout structure, sidebar, content area |
| AssigneePrompt | `AssigneePrompt/__tests__/AssigneePrompt.test.tsx` | Assignee selection prompt/dropdown |
| BlockedBadge | `BlockedBadge/__tests__/BlockedBadge.test.tsx` | Blocked status badge rendering |
| BlockedSummary | `BlockedSummary/__tests__/BlockedSummary.test.tsx` | Summary of blocked issues |
| BulkActionToolbar | `BulkActionToolbar/__tests__/BulkActionToolbar.test.tsx` | Bulk action buttons (close, priority, etc.) |
| ConnectionStatus | `ConnectionStatus/__tests__/ConnectionStatus.test.tsx` | SSE connection indicator |
| DependencyEdge | `DependencyEdge/__tests__/DependencyEdge.test.tsx` | Graph edge rendering between nodes |
| DraggableIssueCard | `DraggableIssueCard/__tests__/DraggableIssueCard.test.tsx` | DnD wrapper for issue cards |
| EditableTitle | `EditableTitle/__tests__/EditableTitle.test.tsx` | Inline title editing |
| EmptyColumn | `EmptyColumn/__tests__/EmptyColumn.test.tsx` | Empty Kanban column state |
| EmptyState | `EmptyState/__tests__/EmptyState.test.tsx` | Empty state for no issues |
| ErrorBoundary | `ErrorBoundary/__tests__/ErrorBoundary.test.tsx` | React error boundary catch/display |
| ErrorDisplay | `ErrorDisplay/__tests__/ErrorDisplay.test.tsx` | Error message component |
| ErrorToast | `ErrorToast/__tests__/ErrorToast.test.tsx` | Error toast notification |
| FilterBar | `FilterBar/__tests__/FilterBar.test.tsx` | Filter controls bar |
| GraphControls | `GraphControls/__tests__/GraphControls.test.tsx` | Graph zoom, pan, layout controls |
| GraphLegend | `GraphLegend/__tests__/GraphLegend.test.tsx` | Graph color/shape legend |
| GraphView | `GraphView/__tests__/GraphView.test.tsx` | Full dependency graph view |
| GraphViewContainer | `GraphViewContainer/__tests__/GraphViewContainer.test.tsx` | Graph view wrapper/provider |
| IssueCard | `IssueCard/__tests__/IssueCard.test.tsx` (~987 lines) | Comprehensive card tests (see above) |
| IssueDetailPanel | `IssueDetailPanel/__tests__/IssueDetailPanel.test.tsx` | Full detail panel |
| IssueHeader | `IssueDetailPanel/__tests__/IssueHeader.test.tsx` | Detail panel header section |
| CommentForm | `IssueDetailPanel/__tests__/CommentForm.test.tsx` | Comment creation form |
| CommentsSection | `IssueDetailPanel/__tests__/CommentsSection.test.tsx` | Comments list and display |
| DependencySection | `IssueDetailPanel/__tests__/DependencySection.test.tsx` | Blocked by / blocks lists |
| EditableDescription | `IssueDetailPanel/__tests__/EditableDescription.test.tsx` | Inline description editing |
| MarkdownRenderer | `IssueDetailPanel/__tests__/MarkdownRenderer.test.tsx` | Markdown to HTML rendering |
| PriorityDropdown | `IssueDetailPanel/__tests__/PriorityDropdown.test.tsx` | Priority selector |
| RejectCommentForm | `IssueDetailPanel/__tests__/RejectCommentForm.test.tsx` | Review rejection comment |
| TypeDropdown | `IssueDetailPanel/__tests__/TypeDropdown.test.tsx` | Issue type selector |
| IssueNode | `IssueNode/__tests__/IssueNode.test.tsx` | Graph node for issues |
| KanbanBoard | `KanbanBoard/__tests__/KanbanBoard.test.tsx` | Full Kanban board |
| columnConfigs | `KanbanBoard/__tests__/columnConfigs.test.ts` | Column configuration logic |
| useDragEnd | `KanbanBoard/__tests__/useDragEnd.test.ts` | Drag-end handler |
| LoadingSkeleton | `LoadingSkeleton/__tests__/LoadingSkeleton.test.tsx` | Loading placeholder |
| MonitorDashboard | `MonitorDashboard/__tests__/MonitorDashboard.test.tsx` | Full monitor view |
| AgentActivityPanel | `MonitorDashboard/__tests__/AgentActivityPanel.test.tsx` | Agent activity section |
| BlockingDepsCanvas | `MonitorDashboard/__tests__/BlockingDependenciesCanvas.test.tsx` | Blocking graph canvas |
| BlockingEdge | `MonitorDashboard/__tests__/BlockingEdge.test.tsx` | Blocking graph edges |
| BlockingNode | `MonitorDashboard/__tests__/BlockingNode.test.tsx` | Blocking graph nodes |
| ConnectionBanner | `MonitorDashboard/__tests__/ConnectionBanner.test.tsx` | Connection status banner |
| ProjectHealthPanel | `MonitorDashboard/__tests__/ProjectHealthPanel.test.tsx` | Project health metrics |
| NavRail | `NavRail/__tests__/NavRail.test.tsx` | Navigation sidebar rail |
| NodeTooltip | `NodeTooltip/__tests__/NodeTooltip.test.tsx` | Graph node hover tooltip |
| SearchInput | `search/__tests__/SearchInput.test.tsx` | Text search input |
| SettingsView | `SettingsView/__tests__/SettingsView.test.tsx` | Settings page |
| StatusColumn | `StatusColumn/__tests__/StatusColumn.test.tsx` | Kanban status column |
| StatusDropdown | `StatusDropdown/__tests__/StatusDropdown.test.tsx` | Status selector |
| SwimLane | `SwimLane/__tests__/SwimLane.test.tsx` | Individual swim lane |
| SwimLaneBoard | `SwimLaneBoard/__tests__/SwimLaneBoard.test.tsx` | Full swimlane board |
| SwimLaneBoard (persist) | `SwimLaneBoard/__tests__/SwimLaneBoard.persistence.test.tsx` | Swimlane collapse state persistence |
| groupingUtils | `SwimLaneBoard/__tests__/groupingUtils.test.ts` | Grouping logic utilities |
| TalkToLeadButton | `TalkToLeadButton/TalkToLeadButton.test.tsx` | Lead communication button |
| TerminalPanel | `TerminalPanel/__tests__/TerminalPanel.test.tsx` | Terminal emulator panel |
| Toast | `Toast/__tests__/Toast.test.tsx` | Individual toast notification |
| ToastContainer | `Toast/__tests__/ToastContainer.test.tsx` | Toast stack container |
| TypeIcon | `TypeIcon/__tests__/TypeIcon.test.tsx` | Issue type icon |
| ViewSwitcher | `ViewSwitcher/__tests__/ViewSwitcher.test.tsx` | Kanban/table/graph toggle |
| IssueTable | `table/IssueTable.test.tsx` | Full table view |
| BlockedCell | `table/__tests__/BlockedCell.test.tsx` | Table blocked indicator cell |
| IssueRow | `table/__tests__/IssueRow.test.tsx` | Table issue row |
| TableHeader | `table/__tests__/TableHeader.test.tsx` | Table sortable headers |

---

### Utility & Type Tests

#### `src/styles/__tests__/colors.test.ts`

**Purpose**: Tests color utility functions for priority/status colors.

**Why**: Color consistency is important for visual priority identification.

#### `src/types/__tests__/types.test.ts` / `graph.test.ts`

**Purpose**: Tests type guard functions and type validation.

**Why**: TypeScript type guards protect against runtime type errors.

#### `src/test-utils/__tests__/chrome-visual-helpers.test.ts` (~516 lines)

**Purpose**: Tests helper functions for Chrome automation visual testing.

**Why**: These helpers are used in manual Chrome-based testing workflows.

#### `src/utils/__tests__/reconnectBackoff.test.ts`

**Purpose**: Tests exponential backoff calculation for SSE reconnection.

**Why**: Backoff prevents overwhelming the server during outages.

#### `src/utils/__tests__/openStatus.test.ts`

**Purpose**: Tests open/closed status determination logic.

**Why**: Status logic controls column placement and filtering.

#### `src/utils/__tests__/ansiStrip.test.ts`

**Purpose**: Tests ANSI escape code stripping from terminal output.

**Why**: Raw terminal output needs ANSI codes removed for display in non-terminal contexts.

#### `src/utils/__tests__/formatIssueId.test.ts`

**Purpose**: Tests issue ID formatting (e.g., `loomcli-abc` display format).

**Why**: Consistent ID display across all views.

#### `src/utils/__tests__/reviewType.test.ts`

**Purpose**: Tests review type determination (code review, plan review, etc.).

**Why**: Review type controls badge display and available actions.

#### `src/__tests__/e2e-infrastructure.test.ts`

**Purpose**: Unit tests for the E2E test infrastructure itself (fixtures, helpers, page objects).

**Why**: Ensures test infrastructure works correctly before running E2E tests.

---

## E2E Tests (Playwright)

### Browser E2E Tests

Located in `tests/e2e/`. These test the full UI in a real browser against mocked API responses.

#### Smoke & Core

| File | What It Tests | Why |
|---|---|---|
| `smoke.spec.ts` | Fixtures, mocks, Page Object Models work | Foundation for all E2E tests |
| `app.spec.ts` | Main app navigation and layout | Basic app functionality |
| `error.spec.ts` | Error handling and display | Users see helpful error messages |
| `error-boundary.spec.ts` | React error boundary behavior | App doesn't crash on component errors |
| `skeleton.spec.ts` | Loading state skeletons | Users see feedback during loading |

#### Kanban Board

| File | What It Tests | Why |
|---|---|---|
| `kanban.spec.ts` | Core Kanban board interactions | Primary work management view |
| `kanban-redesign.spec.ts` | Redesigned Kanban features | Updated UI elements |
| `kanban-ui-redesign.spec.ts` | Kanban UI polish | Visual refinements |

#### Swimlane View

| File | What It Tests | Why |
|---|---|---|
| `swimlane-board.spec.ts` | Swimlane board rendering | Alternative board view |
| `swimlane-wiring.spec.ts` | Swimlane data wiring | Data flows correctly to swimlane |
| `swimlane-collapse.spec.ts` | Swimlane collapse/expand | UX for large boards |

#### Filtering & Search

| File | What It Tests | Why |
|---|---|---|
| `filter.spec.ts` | Filter functionality | Users can narrow issue list |
| `filter-bar.spec.ts` | Filter bar UI | Filter controls work correctly |
| `filter-url-sync.spec.ts` | Filter state in URL | Shareable filtered views |
| `search.spec.ts` | Text search | Quick issue finding |

#### Group By (8 files)

| File | What It Tests |
|---|---|
| `groupby-assignee.spec.ts` | Group by assignee |
| `groupby-epic.spec.ts` | Group by epic |
| `groupby-label.spec.ts` | Group by label |
| `groupby-priority.spec.ts` | Group by priority |
| `groupby-type.spec.ts` | Group by type |
| `groupby-none.spec.ts` | No grouping (flat list) |
| + 2 more variants | Additional group-by combinations |

**Why**: Group-by is heavily used for different workflow perspectives. Each dimension has unique rendering logic.

#### Detail Panel

| File | What It Tests | Why |
|---|---|---|
| `issue-detail-panel.spec.ts` | Detail panel UI | Correct issue data display |
| `issue-detail-panel-wiring.spec.ts` | Detail panel data flow | API integration works |

#### Real-Time Updates

| File | What It Tests | Why |
|---|---|---|
| `server-push.spec.ts` | SSE push events | Real-time updates render |
| `server-push-integration.spec.ts` | SSE + UI integration | Events update the correct components |

#### Monitoring

| File | What It Tests | Why |
|---|---|---|
| `monitor-dashboard.spec.ts` | Dashboard rendering | Agent status visible |
| `monitor-panels.spec.ts` | Dashboard panels | Individual metric panels |
| `monitor-degradation.spec.ts` | Graceful degradation | Dashboard works with partial data |
| `monitor-visual-regression.spec.ts` | Visual regression | Dashboard appearance stable |

#### Detail Panel Components

| File | What It Tests | Why |
|---|---|---|
| `comment-form.spec.ts` | Comment form submission | Users can add comments |
| `comments-section.spec.ts` | Comments list rendering | Comment display and ordering |
| `dependencies-section.spec.ts` | Dependencies UI in detail panel | Dependency management UX |
| `editable-description.spec.ts` | Inline description editing | Users can edit descriptions |
| `editable-title.spec.ts` | Inline title editing | Users can edit titles |
| `priority-dropdown.spec.ts` | Priority selection dropdown | Priority changes work |
| `status-dropdown.spec.ts` | Status selection dropdown | Status changes work |
| `type-dropdown.spec.ts` | Type selection dropdown | Type changes work |

#### Graph View

| File | What It Tests | Why |
|---|---|---|
| `graph-status-filter.spec.ts` | Status filtering in graph view | Graph responds to filters |
| `dependency-edge-styling.spec.ts` | Edge visual styling | Dependency lines styled correctly |
| `dependency-type-filter.spec.ts` | Filter by dependency type | Users can filter graph edges |
| `issue-node-styling.spec.ts` | Node visual styling | Graph nodes styled by priority/status |

#### Miscellaneous E2E

| File | What It Tests | Why |
|---|---|---|
| `assembled-views.spec.ts` | Full view assembly with all components | Integration of all UI pieces |
| `backlog-column.spec.ts` | Backlog column behavior | Backlog-specific UX |
| `kanban-column-redesign.spec.ts` | Redesigned column layout | Updated column visuals |
| `log-streaming.spec.ts` | Agent log streaming in browser | Real-time log display |
| `monitor-backlog-label.spec.ts` | Monitor dashboard backlog labels | Correct backlog data display |
| `show-closed-toggle.spec.ts` | Show/hide closed issues toggle | Closed issue visibility |
| `stats-header.spec.ts` | Project stats in header | Summary stats display |
| `task-log-tabs.spec.ts` | Task log tab navigation | Multiple log streams |
| `toast-notifications.spec.ts` | Toast notification display | User feedback for actions |
| `groupby-dropdown.spec.ts` | Group-by dropdown UI | Group-by selector interaction |
| `groupby-url-sync.spec.ts` | Group-by state in URL | Shareable grouped views |
| `table-sort.spec.ts` | Table column sorting | Data ordering works |
| `view-switcher.spec.ts` | Switching between views | Kanban/table/graph transitions |
| `visual-regression.spec.ts` | Screenshot comparison | Catches unintended visual changes |

---

### API E2E Tests

Located in `tests/e2e/api/`. These test the API directly using a typed client.

#### `api-client.ts` (~600+ lines)

**Purpose**: Typed API client wrapping Playwright's `APIRequestContext`.

**Key methods**: `createIssue()`, `updateIssue()`, `closeIssue()`, `deleteIssue()`, `listIssues()`, `getIssue()`, `getReadyIssues()`, `getBlockedIssues()`, `addDependency()`, `removeDependency()`, `addComment()`, `getComments()`, `getStats()`, `healthCheck()`

#### API Test Files

| File | What It Tests | Why |
|---|---|---|
| `issue-lifecycle.api.spec.ts` | Issue CRUD lifecycle | Core data operations work end-to-end |
| `issue-triage.api.spec.ts` | Issue triage workflows | Prioritization and categorization |
| `dependency-management.api.spec.ts` | Dependency add/remove | Blocking relationships correct |
| `finding-work.api.spec.ts` | Work queue algorithms | Ready work filtered correctly |
| `review-workflow.api.spec.ts` | Code/plan review flows | Review state transitions |
| `team-collaboration.api.spec.ts` | Multi-user scenarios | Concurrent user operations |
| `agent-monitoring.api.spec.ts` | Agent monitoring endpoints | Agent status data correct |
| `agent-logs.api.spec.ts` | Agent log streaming | Log data accessible |
| `task-logs.api.spec.ts` | Task log retrieval | Task-level logs work |
| `daemon-config.api.spec.ts` | Daemon configuration API | Config read/write |
| `project-health.api.spec.ts` | Project health metrics | Stats and health data |
| `realtime-updates.api.spec.ts` | SSE event verification | Events match API changes |

**Why**: API tests verify the backend contract independently of the UI. They catch API regressions before they reach the frontend.

---

### Integration Tests

Located in `tests/e2e/integration/`. These run against a real Podman Compose stack.

#### Global Setup (`tests/e2e/global-setup.ts`)

**What it does**:
1. Starts Podman Compose stack (`compose.e2e.yml`)
2. Waits for health checks on `localhost:8080` (consolidated server) and `localhost:9000` (Loom API)
3. Timeout: 2 minutes for container builds
4. Writes state file (`.e2e-state.json`) with URLs for tests

#### Integration Test Files

| File | What It Tests | Why |
|---|---|---|
| `kanban-crud.integration.spec.ts` | Kanban CRUD against real backend | Full stack validation |
| `sse-updates.integration.spec.ts` | Real-time SSE with real backend | SSE works end-to-end |

**Why**: Integration tests are the final validation layer. They catch issues that mocked tests miss (serialization mismatches, timing issues, database constraints).

### Playwright Configuration

**Projects**:
- `chromium` - Standard E2E tests (excludes integration by default)
- `integration` - Real backend tests (requires `RUN_INTEGRATION_TESTS=1`)
- `api` - API-level tests (requires `RUN_INTEGRATION_TESTS=1`)

**CI Settings**: Single worker, 2 retries, GitHub reporter.

---

## Test Infrastructure

### Fixtures (`tests/e2e/fixtures/`)

| Fixture | Purpose |
|---|---|
| `mockApi` | API response mocking with request tracking |
| `mockSSE` | SSE connection simulation with event injection |
| `appPage` | Pre-configured page with auth and SSE setup |

**Why**: Fixtures provide consistent test setup. `mockApi` tracks requests for assertion, `mockSSE` injects events for testing real-time updates.

### Page Object Models (`tests/e2e/pages/`)

| POM | Purpose |
|---|---|
| `AppPage` | Main app navigation and connection status |
| `KanbanPage` | Column interaction, card counts, drag operations |

**Why**: POMs encapsulate DOM selectors and common actions. When UI changes, only POMs need updating.

### Test Helpers (`tests/e2e/helpers/`)

| Helper | Purpose |
|---|---|
| `createIssue()` | Factory for test issues with sensible defaults |
| `createStats()` | Mock statistics objects |
| `createKanbanData()` | Complete Kanban board data set |
| `resetIdCounter()` | Reset ID generation between tests |

### Mocking Strategies

| Strategy | Where Used | Why |
|---|---|---|
| `vi.mock('@/api')` | Hook tests | Isolate hooks from network calls |
| Custom MockEventSource | SSE tests | Simulate browser EventSource API |
| `vi.hoisted()` mock pattern | Component tests | Consistent mock access across test files |
| Heavy component mocking | App tests | Prevent ResizeObserver/terminal renderer issues in jsdom |
| Playwright route interception | E2E tests | Control API responses in browser |

### Key Testing Patterns

1. **Race condition prevention**: `useAgents` tests verify timer deduplication; `useIssues` tests verify SSE mutations during refetch
2. **Fake timers**: `vi.useFakeTimers()` / `vi.advanceTimersByTime()` for controlled async
3. **Accessibility-first queries**: `getByRole()`, `getByLabelText()` over `getByTestId()`
4. **Optimistic update testing**: Verify immediate UI update, then verify rollback on API failure
5. **Functional state updates**: Tests verify `setState(prev => ...)` pattern prevents snapshot clobbering
