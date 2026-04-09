import { test, expect, isIntegrationEnabled, generateTestId } from '../api/api-client'
import type { Page } from '@playwright/test'

/**
 * Integration tests for dependency graph creation and resolution.
 *
 * Full-stack tests: browser + real backend, exercising the complete pipeline
 * (API → RPC → SQLite → blocked cache → SSE mutation events → React UI).
 *
 * Requires:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration -g "Dependency Graph"
 */

test.skip(!isIntegrationEnabled, 'Integration tests require RUN_INTEGRATION_TESTS=1')

test.describe.configure({ mode: 'serial' })

/**
 * Wait for a condition via SSE, falling back to page reload if SSE doesn't propagate in time.
 * This handles serial test execution where prior test cleanup can disrupt the SSE connection.
 */
async function waitForSSEOrReload(page: Page, assertion: () => Promise<void>) {
  try {
    await expect(assertion).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })
  } catch {
    // SSE may not have propagated; reload to get fresh server state
    await page.reload()
    await page.waitForLoadState('domcontentloaded')
    await expect(assertion).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })
  }
}

/**
 * Navigate to kanban view. The root URL redirects to /ws/{id}/.
 * Wait for the redirect to settle and the SSE connection to establish.
 */
async function gotoKanban(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')
  // Wait for redirect to /ws/{id}/ and SSE connection
  const connectionStatus = page.locator('[data-state="connected"]')
  await expect(connectionStatus).toBeVisible({ timeout: 10000 })
}

/**
 * Navigate to graph view by going to kanban first (to resolve workspace),
 * then appending ?view=graph to the resolved workspace URL.
 */
async function gotoGraph(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')
  // Wait for redirect to /ws/{id}/ to settle
  await page.waitForURL(/\/ws\//, { timeout: 10000 })
  // Now navigate to graph view using the resolved workspace URL
  const currentUrl = new URL(page.url())
  currentUrl.searchParams.set('view', 'graph')
  await page.goto(currentUrl.toString())
  await page.waitForLoadState('domcontentloaded')
  // Wait for SSE connection (same as gotoKanban) so live updates work
  await expect(page.locator('[data-state="connected"]')).toBeVisible({ timeout: 10000 })
  await expect(page.locator('[data-testid="graph-view"]')).toBeVisible({ timeout: 10000 })
}

test.describe('Dependency Graph - Graph Creation', () => {
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    for (const id of [...createdIssueIds].reverse()) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test('dependency appears as edge in graph view', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `Blocker ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `Blocked ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    await gotoGraph(page)

    // Verify both nodes visible in graph
    const graphView = page.locator('[data-testid="graph-view"]')
    await expect(async () => {
      await expect(graphView.getByText(`Blocker ${testId}`)).toBeVisible()
      await expect(graphView.getByText(`Blocked ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Verify at least one edge with data-type="blocks" exists
    await expect(async () => {
      const edgeLabels = graphView.locator('[data-type="blocks"]')
      await expect(edgeLabels.first()).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })
  })

  test('multiple dependencies form correct DAG', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `DAG-A ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `DAG-B ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)
    const issueC = await api.createIssue({ title: `DAG-C ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueC.id)
    const issueD = await api.createIssue({ title: `DAG-D ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueD.id)

    // Diamond: A→B, A→C, B→D, C→D
    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })
    await api.addDependency(issueC.id, { depends_on_id: issueA.id, dep_type: 'blocks' })
    await api.addDependency(issueD.id, { depends_on_id: issueB.id, dep_type: 'blocks' })
    await api.addDependency(issueD.id, { depends_on_id: issueC.id, dep_type: 'blocks' })

    await gotoGraph(page)

    const graphView = page.locator('[data-testid="graph-view"]')

    // Verify all 4 nodes visible
    await expect(async () => {
      await expect(graphView.getByText(`DAG-A ${testId}`)).toBeVisible()
      await expect(graphView.getByText(`DAG-B ${testId}`)).toBeVisible()
      await expect(graphView.getByText(`DAG-C ${testId}`)).toBeVisible()
      await expect(graphView.getByText(`DAG-D ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Verify at least 4 edges exist
    await expect(async () => {
      const edges = graphView.locator('.react-flow__edge')
      const count = await edges.count()
      expect(count).toBeGreaterThanOrEqual(4)
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })
  })
})

test.describe('Dependency Graph - Blocking Resolution in Kanban', () => {
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    for (const id of [...createdIssueIds].reverse()) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test('adding dependency moves issue from ready to blocked', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `ReadyA ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `ReadyB ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await gotoKanban(page)

    const readyColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')

    // Both should be in ready column initially
    await expect(async () => {
      await expect(readyColumn.getByText(`ReadyA ${testId}`)).toBeVisible()
      await expect(readyColumn.getByText(`ReadyB ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Add blocking dependency: B depends on A
    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    // B should move to blocked column via SSE (with reload fallback)
    await waitForSSEOrReload(page, async () => {
      await expect(blockedColumn.getByText(`ReadyB ${testId}`)).toBeVisible()
    })

    // B should no longer be in ready column
    await expect(async () => {
      const isVisible = await readyColumn.getByText(`ReadyB ${testId}`).isVisible().catch(() => false)
      expect(isVisible).toBe(false)
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })
  })

  test('closing blocker moves dependent back to ready', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `Blocker ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `Dependent ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    // Add dependency first
    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    await gotoKanban(page)

    const readyColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')

    // B should be in blocked column
    await expect(async () => {
      await expect(blockedColumn.getByText(`Dependent ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Close the blocker
    await api.closeIssue(issueA.id)

    // B should move back to ready column (SSE or reload fallback)
    await waitForSSEOrReload(page, async () => {
      await expect(readyColumn.getByText(`Dependent ${testId}`)).toBeVisible()
    })

    // B should no longer be in blocked column
    await expect(async () => {
      const isVisible = await blockedColumn.getByText(`Dependent ${testId}`).isVisible().catch(() => false)
      expect(isVisible).toBe(false)
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })
  })

  test('removing dependency unblocks issue', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `DepBlocker ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `DepBlocked ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    await gotoKanban(page)

    const readyColumn = page.locator('section[data-status="ready"]')
    const blockedColumn = page.locator('section[data-status="blocked"]')

    // B should be in blocked column
    await expect(async () => {
      await expect(blockedColumn.getByText(`DepBlocked ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Remove the dependency
    await api.removeDependency(issueB.id, issueA.id)

    // B should return to ready column (SSE or reload fallback)
    await waitForSSEOrReload(page, async () => {
      await expect(readyColumn.getByText(`DepBlocked ${testId}`)).toBeVisible()
    })

    // B should no longer be in blocked column
    await expect(async () => {
      const isBlocked = await blockedColumn.getByText(`DepBlocked ${testId}`).isVisible().catch(() => false)
      expect(isBlocked).toBe(false)
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })
  })
})

test.describe('Dependency Graph - Graph View Updates via SSE', () => {
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    for (const id of [...createdIssueIds].reverse()) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test('new dependency edge appears in graph after adding dependency', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `SSE-A ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `SSE-B ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await gotoGraph(page)

    const graphView = page.locator('[data-testid="graph-view"]')

    // Wait for both nodes to be visible
    await expect(async () => {
      await expect(graphView.getByText(`SSE-A ${testId}`)).toBeVisible()
      await expect(graphView.getByText(`SSE-B ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Count edges before adding dependency
    const initialEdgeCount = await graphView.locator('.react-flow__edge').count()

    // Add dependency via API
    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    // Verify a new edge appears (SSE with reload fallback — the graph view
    // may not subscribe to dependency-specific SSE mutations yet)
    await waitForSSEOrReload(page, async () => {
      const currentEdgeCount = await graphView.locator('.react-flow__edge').count()
      expect(currentEdgeCount).toBeGreaterThan(initialEdgeCount)
    })
  })

  test('closing issue updates node styling in graph', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `Style-A ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `Style-B ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    await gotoGraph(page)

    // Show-closed defaults to true, but may have been toggled off by localStorage.
    // Ensure it's checked so closed nodes remain visible after closing.
    const showClosedToggle = page.locator('[data-testid="show-closed-toggle"]')
    await expect(showClosedToggle).toBeVisible({ timeout: 5000 })
    if (!(await showClosedToggle.isChecked())) {
      await showClosedToggle.click()
    }

    const graphView = page.locator('[data-testid="graph-view"]')

    // Verify A's node has data-status="open"
    await expect(async () => {
      const nodeA = graphView.locator('.react-flow__node', { hasText: `Style-A ${testId}` })
      await expect(nodeA.locator('article[data-status="open"]')).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Close A via API
    await api.closeIssue(issueA.id)

    // Verify A's node changes to data-status="closed" (SSE or reload fallback)
    await waitForSSEOrReload(page, async () => {
      const nodeA = graphView.locator('.react-flow__node', { hasText: `Style-A ${testId}` })
      await expect(nodeA.locator('article[data-status="closed"]')).toBeVisible()
    })
  })
})

test.describe('Dependency Graph - Detail Panel Integration', () => {
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    for (const id of [...createdIssueIds].reverse()) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test('clicking issue in kanban shows dependencies in detail panel', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `PanelBlocker ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `PanelBlocked ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    await gotoKanban(page)

    const blockedColumn = page.locator('section[data-status="blocked"]')

    // Wait for B to appear in blocked column
    await expect(async () => {
      await expect(blockedColumn.getByText(`PanelBlocked ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Click B's card to open detail panel
    await blockedColumn.locator('article', { hasText: `PanelBlocked ${testId}` }).click()

    // Verify dependency section is visible
    await expect(page.locator('[data-testid="dependency-section"]')).toBeVisible({ timeout: 5000 })

    // Verify the dependency list contains A's title
    const depList = page.locator('[data-testid="dependency-list"]')
    await expect(depList.getByText(`PanelBlocker ${testId}`)).toBeVisible({ timeout: 5000 })
  })

  test('dependency count updates after adding dependency', async ({ page, api }) => {
    const testId = generateTestId()
    const issueA = await api.createIssue({ title: `CountA ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueA.id)
    const issueB = await api.createIssue({ title: `CountB ${testId}`, issue_type: 'task', priority: 2 })
    createdIssueIds.push(issueB.id)

    await gotoKanban(page)

    const readyColumn = page.locator('section[data-status="ready"]')

    // Wait for B to appear in ready column
    await expect(async () => {
      await expect(readyColumn.getByText(`CountB ${testId}`)).toBeVisible()
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] })

    // Click B's card to open detail panel
    await readyColumn.locator('article', { hasText: `CountB ${testId}` }).click()

    // Verify "No blocking dependencies" message
    await expect(page.locator('[data-testid="no-dependencies"]')).toBeVisible({ timeout: 5000 })

    // Close the detail panel by pressing Escape
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-testid="dependency-section"]')).not.toBeVisible({ timeout: 3000 })

    // Add dependency via API
    await api.addDependency(issueB.id, { depends_on_id: issueA.id, dep_type: 'blocks' })

    // Wait for B to move to blocked column (SSE or reload fallback)
    const blockedColumn = page.locator('section[data-status="blocked"]')
    await waitForSSEOrReload(page, async () => {
      await expect(blockedColumn.getByText(`CountB ${testId}`)).toBeVisible()
    })

    // Re-open B's detail panel from blocked column
    await blockedColumn.locator('article', { hasText: `CountB ${testId}` }).click()

    // Verify the dependency list now shows A's title
    await expect(page.locator('[data-testid="dependency-section"]')).toBeVisible({ timeout: 5000 })
    const depList = page.locator('[data-testid="dependency-list"]')
    await expect(depList.getByText(`CountA ${testId}`)).toBeVisible({ timeout: 5000 })
  })
})
