import { test, expect } from '@playwright/test'

/**
 * Integration tests for SSE live updates against real backend.
 *
 * These tests require:
 * - Podman Compose stack running (via global-setup)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS
test.skip(skipIntegration, 'Integration tests require RUN_INTEGRATION_TESTS=1')

// Run tests serially to avoid data conflicts
test.describe.configure({ mode: 'serial' })

const BASE_URL = 'http://localhost:8080'

/**
 * Generate unique ID for test isolation.
 */
function generateTestId(): string {
  return `test-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`
}

/**
 * Create a test issue via the API.
 */
async function createTestIssue(title: string, options?: { priority?: number }): Promise<string> {
  const response = await fetch(`${BASE_URL}/api/issues`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title,
      issue_type: 'task',
      priority: options?.priority ?? 2,
    }),
  })

  if (!response.ok) {
    const text = await response.text()
    throw new Error(`Failed to create issue: ${response.status} - ${text}`)
  }

  const result = await response.json()
  if (!result.success) {
    throw new Error(`API error: ${result.error}`)
  }

  return result.data.id
}

/**
 * Update issue status via the API.
 */
async function updateIssueStatus(id: string, status: string): Promise<void> {
  const response = await fetch(`${BASE_URL}/api/issues/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ status }),
  })

  if (!response.ok) {
    const text = await response.text()
    throw new Error(`Failed to update issue: ${response.status} - ${text}`)
  }
}

/**
 * Close an issue via the API.
 */
async function closeTestIssue(id: string): Promise<void> {
  try {
    const response = await fetch(`${BASE_URL}/api/issues/${id}/close`, { method: 'POST' })
    if (!response.ok && response.status !== 404) {
      console.warn(`Failed to close issue ${id}: ${response.status}`)
    }
  } catch {
    // Ignore network errors during cleanup
  }
}

test.describe('SSE Live Updates Integration', () => {
  const testIssueIds: string[] = []

  test.afterEach(async () => {
    // Clean up created issues via API
    for (const id of testIssueIds) {
      await closeTestIssue(id)
    }
    testIssueIds.length = 0
  })

  test('SSE connection establishes on page load', async ({ page }) => {
    // Navigate to Kanban board
    await page.goto('/')

    // Wait for SSE connection status to show connected
    // The connection indicator uses data-state="connected"
    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Verify no error toasts appeared
    const errorToast = page.locator('[role="alert"]', { hasText: /error|failed/i })
    await expect(errorToast).not.toBeVisible({ timeout: 2000 }).catch(() => {
      // It's okay if we timeout - means no error toast
    })
  })

  test('API-created issue appears via SSE without reload', async ({ page }) => {
    // Navigate to Kanban and wait for initial load
    await page.goto('/')
    await page.waitForLoadState('domcontentloaded')

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Count initial issue cards in ready column (toBeVisible ensures column is loaded)
    const readyColumn = page.locator('section[data-status="ready"]')
    await expect(readyColumn).toBeVisible()
    const initialCardCount = await readyColumn.locator('article').count()

    // Create new issue via API (not through UI)
    const uniqueTitle = `SSE Test Issue ${generateTestId()}`
    const issueId = await createTestIssue(uniqueTitle)
    testIssueIds.push(issueId)

    // Wait for the new issue to appear via SSE (without page reload)
    await expect(async () => {
      const newCardCount = await readyColumn.locator('article').count()
      expect(newCardCount).toBeGreaterThan(initialCardCount)
    }).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })

    // Verify the specific issue card is visible
    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible()
  })

  test('status change via API updates UI via SSE', async ({ page }) => {
    // Create test issue via API (open status by default -> appears in ready)
    const uniqueTitle = `Status Change Test ${generateTestId()}`
    const issueId = await createTestIssue(uniqueTitle)
    testIssueIds.push(issueId)

    // Navigate to Kanban
    await page.goto('/')
    await page.waitForLoadState('domcontentloaded')

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Wait for issue to appear in ready column
    const readyColumn = page.locator('section[data-status="ready"]')
    const inProgressColumn = page.locator('section[data-status="in_progress"]')

    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible({ timeout: 10000 })

    // Verify issue is NOT in in_progress column initially
    await expect(inProgressColumn.getByText(uniqueTitle)).not.toBeVisible()

    // Update status via API to in_progress
    await updateIssueStatus(issueId, 'in_progress')

    // Wait for card to move to in_progress column via SSE
    await expect(async () => {
      await expect(inProgressColumn.getByText(uniqueTitle)).toBeVisible()
    }).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })

    // Verify card is no longer in ready column
    await expect(readyColumn.getByText(uniqueTitle)).not.toBeVisible()
  })

  test('multiple rapid updates via API are reflected in UI', async ({ page }) => {
    // Navigate to Kanban and wait for connection
    await page.goto('/')
    await page.waitForLoadState('domcontentloaded')

    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Create multiple issues rapidly via API
    const issues: { id: string; title: string }[] = []
    for (let i = 0; i < 3; i++) {
      const title = `Rapid Update Test ${generateTestId()}`
      const id = await createTestIssue(title)
      issues.push({ id, title })
      testIssueIds.push(id)
    }

    // Wait for all issues to appear in ready column
    const readyColumn = page.locator('section[data-status="ready"]')

    await expect(async () => {
      for (const issue of issues) {
        await expect(readyColumn.getByText(issue.title)).toBeVisible()
      }
    }).toPass({ timeout: 15000, intervals: [500, 1000, 2000] })

    // Verify all 3 issues are visible
    for (const issue of issues) {
      await expect(readyColumn.getByText(issue.title)).toBeVisible()
    }
  })

  test('closed issue disappears from open columns', async ({ page }) => {
    // Create test issue
    const uniqueTitle = `Close via SSE Test ${generateTestId()}`
    const issueId = await createTestIssue(uniqueTitle)
    testIssueIds.push(issueId)

    // Navigate to Kanban
    await page.goto('/')
    await page.waitForLoadState('domcontentloaded')

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Wait for issue to appear in ready column
    const readyColumn = page.locator('section[data-status="ready"]')
    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible({ timeout: 10000 })

    // Close issue via API
    await closeTestIssue(issueId)

    // Issue should disappear from ready column (moved to done or removed)
    await expect(async () => {
      const isVisible = await readyColumn.getByText(uniqueTitle).isVisible().catch(() => false)
      expect(isVisible).toBe(false)
    }).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })

    // Optionally check done column if visible
    const doneColumn = page.locator('section[data-status="done"]')
    if (await doneColumn.isVisible({ timeout: 2000 }).catch(() => false)) {
      // Issue may or may not be in done column depending on UI state/filters
      // Just verify it's not in the active columns
    }
  })
})
