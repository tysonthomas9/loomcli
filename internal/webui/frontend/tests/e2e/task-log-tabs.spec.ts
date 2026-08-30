import { test, expect, Page } from '@playwright/test'

import { setupFleetMocks, workspaceApi } from './helpers/fleet'

/**
 * E2E tests for IssueDetailPanel task log tabs using live SSE streams.
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

type TaskLogPhase = 'planning' | 'implementation'

function taskLogStreamPath(api: string, phase: TaskLogPhase) {
  return `${api}/tasks/log-task-1/logs/${phase}/stream`
}

function logChunkStream(text: string) {
  const bytes = Buffer.from(text, 'utf8')
  return (
    'retry: 5000\n\n' +
    'id: 1\n' +
    'event: log-chunk\n' +
    `data: ${JSON.stringify({
      chunk_b64: bytes.toString('base64'),
      byte_offset: bytes.length,
      timestamp: '2026-08-30T00:00:00Z',
    })}\n\n`
  )
}

async function setupBaseMocks(
  page: Page,
  options?: {
    phases?: string[]
    phasesError?: boolean
    planningStreamStatus?: number
    implementationStreamStatus?: number
  }
) {
  const phases = options?.phases ?? ['planning', 'implementation']
  const api = workspaceApi()

  await page.route('**/api/auth/token', async (route) => {
    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Not found' }),
    })
  })

  await setupFleetMocks(page, allMockIssues)

  // setupFleetMocks aborts event endpoints by default. Log streams run in
  // open mode here, so the token exchange must explicitly report disabled.
  await page.route(
    (url) => url.pathname === `${api}/events/token`,
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ disabled: true }),
      })
    }
  )

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
    (url) => url.pathname === `${api}/tasks/log-task-1/logs`,
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

  for (const phase of ['planning', 'implementation'] as const) {
    await page.route(
      (url) => url.pathname === taskLogStreamPath(api, phase),
      async (route) => {
        const status = phase === 'planning' ? (options?.planningStreamStatus ?? 200) : (options?.implementationStreamStatus ?? 200)
        if (status !== 200) {
          await route.fulfill({
            status,
            contentType: 'application/json',
            body: JSON.stringify({
              success: false,
              error: `${phase} log stream error`,
            }),
          })
          return
        }

        const label = phase === 'planning' ? 'Planning' : 'Implementation'
        await route.fulfill({
          status: 200,
          headers: {
            'content-type': 'text/event-stream',
            'cache-control': 'no-cache',
          },
          body: logChunkStream(`${label} stream\n`),
        })
      }
    )
  }
}

async function navigateToApp(page: Page) {
  await page.goto('/ws/default/kanban')
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

  test('planning tab opens one live stream and renders its content', async ({ page }) => {
    await setupBaseMocks(page)
    const streamPath = taskLogStreamPath(workspaceApi(), 'planning')
    let planningRequests = 0
    page.on('request', (request) => {
      if (new URL(request.url()).pathname === streamPath) planningRequests += 1
    })

    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    const streamRequest = page.waitForRequest((request) => new URL(request.url()).pathname === streamPath)
    await panel.getByRole('tab', { name: 'Planning' }).click()
    await streamRequest
    await expect(page.getByTestId('log-viewer')).toBeVisible()
    await expect(page.getByTestId('agent-log-content')).toContainText('Planning stream')
    await page.waitForTimeout(DOM_SETTLE_MS)
    expect(planningRequests).toBe(1)
  })

  test('switching phases abandons planning and opens implementation stream', async ({ page }) => {
    await setupBaseMocks(page)
    const api = workspaceApi()
    const planningPath = taskLogStreamPath(api, 'planning')
    const implementationPath = taskLogStreamPath(api, 'implementation')
    let planningRequests = 0
    let implementationRequests = 0
    page.on('request', (request) => {
      const path = new URL(request.url()).pathname
      if (path === planningPath) planningRequests += 1
      if (path === implementationPath) implementationRequests += 1
    })

    await navigateToApp(page)
    await openIssuePanel(page, 'Task With Logs')

    const panel = page.getByTestId('issue-detail-panel')
    const planningRequest = page.waitForRequest((request) => new URL(request.url()).pathname === planningPath)
    await panel.getByRole('tab', { name: 'Planning' }).click()
    await planningRequest
    await expect(page.getByTestId('agent-log-content')).toContainText('Planning stream')

    const implementationRequest = page.waitForRequest((request) => new URL(request.url()).pathname === implementationPath)
    await panel.getByRole('tab', { name: 'Implementation' }).click()
    await implementationRequest
    await expect(page.getByTestId('agent-log-content')).toContainText('Implementation stream')

    // The finite planning fixture schedules a 5s reconnect. Waiting beyond it
    // proves the hidden pane canceled that retry when its EventSource closed.
    await page.waitForTimeout(5200)
    expect(planningRequests).toBe(1)
    expect(implementationRequests).toBeGreaterThanOrEqual(1)
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

  test('stream endpoint failure shows non-connected state in log viewer', async ({ page }) => {
    await setupBaseMocks(page, {
      phases: ['planning'],
      planningStreamStatus: 500,
    })
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
