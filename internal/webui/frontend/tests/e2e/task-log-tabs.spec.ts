import { test, expect, Page } from '@playwright/test'

/**
 * E2E tests for IssueDetailPanel task log tabs using snapshot polling.
 */

const DOM_SETTLE_MS = 400

const mockTaskIssue = {
  id: 'log-task-1',
  title: 'Task With Logs',
  status: 'in_progress',
  priority: 1,
  issue_type: 'task',
  description: 'A task with log phases',
  created_at: '2026-02-01T10:00:00Z',
  updated_at: '2026-02-01T10:00:00Z',
}

const mockBugIssue = {
  id: 'log-bug-1',
  title: 'Bug Without Logs',
  status: 'open',
  priority: 2,
  issue_type: 'bug',
  description: 'A bug - no log tabs',
  created_at: '2026-02-01T11:00:00Z',
  updated_at: '2026-02-01T11:00:00Z',
}

const allMockIssues = [mockTaskIssue, mockBugIssue]

function getMockIssueDetails(issue: (typeof allMockIssues)[0]) {
  return {
    ...issue,
    dependencies: [],
    dependents: [],
    comments: [],
  }
}

async function setupBaseMocks(
  page: Page,
  options?: {
    phases?: string[]
    phasesError?: boolean
    planningLogStatus?: number
    implementationLogStatus?: number
  }
) {
  const phases = options?.phases ?? ['planning', 'implementation']

  await page.route('**/api/auth/token', async (route) => {
    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Not found' }),
    })
  })

  await page.route('**/api/ready', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: allMockIssues }),
    })
  })

  await page.route('**/api/blocked', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, data: [] }),
    })
  })

  await page.route('**/api/issues/graph', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, issues: allMockIssues }),
    })
  })

  await page.route(
    (url) => url.pathname === '/api/issues',
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, data: allMockIssues }),
      })
    }
  )

  await page.route('**/api/events', async (route) => {
    await route.abort()
  })

  await page.route('**/api/stats', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: { open: 2, closed: 0, total: 2, completion: 0 },
      }),
    })
  })

  await page.route('**/api/issues/*', async (route) => {
    const request = route.request()
    if (request.method() !== 'GET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true }),
      })
      return
    }

    const url = request.url()
    const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
    const id = idMatch ? idMatch[1] : null
    const issue = allMockIssues.find((i) => i.id === id)

    if (issue) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, data: getMockIssueDetails(issue) }),
      })
      return
    }

    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ success: false, error: 'Not found' }),
    })
  })

  await page.route('**/api/monitor/agents', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ agents: [] }),
    })
  })

  await page.route('**/api/monitor/status', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        agents: [],
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 0,
        },
        agent_tasks: {},
        sync: { db_synced: true, db_last_sync: '', git_needs_push: 0, git_needs_pull: 0 },
        stats: { open: 0, closed: 0, total: 0, completion: 0 },
        timestamp: new Date().toISOString(),
      }),
    })
  })

  await page.route('**/api/monitor/tasks', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        needs_planning: [],
        ready_to_implement: [],
        in_progress: [],
        needs_review: [],
        backlog: [],
      }),
    })
  })

  await page.route(
    (url) => /^\/api\/tasks\/[^/]+\/logs$/.test(url.pathname),
    async (route) => {
      if (options?.phasesError) {
        await route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({ success: false, error: 'Internal error' }),
        })
        return
      }

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, data: { phases } }),
      })
    }
  )

  await page.route('**/api/tasks/log-task-1/logs/planning**', async (route) => {
    const status = options?.planningLogStatus ?? 200
    if (status !== 200) {
      await route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, error: 'planning log error' }),
      })
      return
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: { lines: ['Planning line 1', 'Planning line 2'], line_count: 2 },
      }),
    })
  })

  await page.route('**/api/tasks/log-task-1/logs/implementation**', async (route) => {
    const status = options?.implementationLogStatus ?? 200
    if (status !== 200) {
      await route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, error: 'implementation log error' }),
      })
      return
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: { lines: ['Implementation line 1', 'Implementation line 2'], line_count: 2 },
      }),
    })
  })
}

async function navigateToApp(page: Page) {
  await page.goto('/')
  await expect(page.getByTestId('loading-container')).not.toBeVisible({ timeout: 5000 })
  await expect(page.locator('article').first()).toBeVisible({ timeout: 5000 })
}

async function openIssuePanel(page: Page, issueTitle: string) {
  const card = page.locator('article').filter({ hasText: issueTitle })
  await expect(card).toBeVisible()
  await card.click()
  await expect(page.getByTestId('issue-detail-panel')).toHaveAttribute('data-state', 'open')
  await expect(page.getByTestId('issue-id')).toBeVisible({ timeout: 5000 })
}

test.describe('Task Log Tabs', () => {
  test('task issue shows Details/Planning/Implementation tabs', async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    await expect(panel.getByRole('tab', { name: 'Details' })).toBeVisible({ timeout: 5000 })
    await expect(panel.getByRole('tab', { name: 'Planning' })).toBeVisible()
    await expect(panel.getByRole('tab', { name: 'Implementation' })).toBeVisible()
  })

  test('non-task issue does not show log tabs', async ({ page }) => {
    await setupBaseMocks(page)
    await navigateToApp(page)
    await openIssuePanel(page, 'Bug Without Logs')

    const panel = page.getByTestId('issue-detail-panel')
    await page.waitForTimeout(DOM_SETTLE_MS)
    await expect(panel.getByRole('tab', { name: 'Planning' })).not.toBeVisible()
    await expect(panel.getByRole('tab', { name: 'Implementation' })).not.toBeVisible()
  })

  test('planning tab mounts LogViewer and polls planning endpoint', async ({ page }) => {
    let planningCalls = 0
    await setupBaseMocks(page)

    await page.route('**/api/tasks/log-task-1/logs/planning**', async (route) => {
      planningCalls += 1
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { lines: ['Planning line 1'], line_count: 1 },
        }),
      })
    })

    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    await panel.getByRole('tab', { name: 'Planning' }).click()
    await expect(page.getByTestId('log-viewer')).toBeVisible()

    await expect
      .poll(() => planningCalls, { timeout: 5000 })
      .toBeGreaterThan(0)
  })

  test('switching from planning to implementation changes polled endpoint', async ({ page }) => {
    let planningCalls = 0
    let implementationCalls = 0

    await setupBaseMocks(page)

    await page.route('**/api/tasks/log-task-1/logs/planning**', async (route) => {
      planningCalls += 1
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { lines: ['Planning line'], line_count: 1 },
        }),
      })
    })

    await page.route('**/api/tasks/log-task-1/logs/implementation**', async (route) => {
      implementationCalls += 1
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: { lines: ['Implementation line'], line_count: 1 },
        }),
      })
    })

    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    await panel.getByRole('tab', { name: 'Planning' }).click()
    await expect(page.getByTestId('log-viewer')).toBeVisible()
    await expect.poll(() => planningCalls, { timeout: 5000 }).toBeGreaterThan(0)

    await panel.getByRole('tab', { name: 'Implementation' }).click()
    await expect.poll(() => implementationCalls, { timeout: 5000 }).toBeGreaterThan(0)
  })

  test('phase discovery error keeps panel usable and hides log tabs', async ({ page }) => {
    await setupBaseMocks(page, { phasesError: true })
    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    await page.waitForTimeout(DOM_SETTLE_MS)

    await expect(panel.getByRole('tab', { name: 'Planning' })).not.toBeVisible()
    await expect(panel.getByRole('tab', { name: 'Implementation' })).not.toBeVisible()
    await expect(page.getByText('A task with log phases')).toBeVisible()
  })

  test('snapshot endpoint failure shows non-connected state in log viewer', async ({ page }) => {
    await setupBaseMocks(page, { phases: ['planning'], planningLogStatus: 500 })
    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    await panel.getByRole('tab', { name: 'Planning' }).click()

    const logViewer = page.getByTestId('log-viewer')
    await expect(logViewer).toBeVisible()

    const statusDot = logViewer.locator('[data-state]')
    const state = await statusDot.getAttribute('data-state')
    expect(['disconnected', 'reconnecting', 'connecting']).toContain(state)
  })
})
