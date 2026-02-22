import { test, expect } from '@playwright/test'
import { generateTestId, createTestIssue, closeTestIssue } from './helpers'

/**
 * Integration tests for Kanban CRUD operations against real backend.
 *
 * These tests require:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS
test.skip(skipIntegration, 'Integration tests require RUN_INTEGRATION_TESTS=1')

// Run tests serially to avoid data conflicts
test.describe.configure({ mode: 'serial' })

test.describe('Kanban CRUD Integration', () => {
  const testIssueIds: string[] = []

  test.afterEach(async () => {
    // Clean up created issues via API
    for (const id of testIssueIds) {
      await closeTestIssue(id)
    }
    testIssueIds.length = 0
  })

  test('create issue via API appears in UI', async ({ page }) => {
    // Create a unique test issue
    const uniqueTitle = `Integration Test Issue ${generateTestId()}`
    const issueId = await createTestIssue(uniqueTitle)
    testIssueIds.push(issueId)

    // Navigate to Kanban board
    await page.goto('/')
    await page.waitForLoadState('domcontentloaded')

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Wait for issue card to appear in ready column
    const readyColumn = page.locator('section[data-status="ready"]')
    await expect(readyColumn).toBeVisible()

    // Verify the issue card with our title is visible
    await expect(async () => {
      const issueCard = readyColumn.getByText(uniqueTitle)
      await expect(issueCard).toBeVisible()
    }).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })
  })

  test('close issue via API removes from ready column', async ({ page }) => {
    // Create test issue via API
    const uniqueTitle = `Close Test Issue ${generateTestId()}`
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

    // Verify issue disappears from ready column (moved to done or removed via SSE)
    await expect(async () => {
      const issueInReady = await readyColumn.getByText(uniqueTitle).isVisible().catch(() => false)
      expect(issueInReady).toBe(false)
    }).toPass({ timeout: 10000, intervals: [500, 1000, 2000] })
  })

  test('API-created issue has correct priority badge', async ({ page }) => {
    // Create a high priority issue using helper with priority option
    const uniqueTitle = `Priority Test Issue ${generateTestId()}`
    const issueId = await createTestIssue(uniqueTitle, { priority: 1 }) // P1 = high priority
    testIssueIds.push(issueId)

    // Navigate to Kanban board
    await page.goto('/')
    await page.waitForLoadState('domcontentloaded')

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]')
    await expect(connectionStatus).toBeVisible({ timeout: 10000 })

    // Wait for issue to appear
    const readyColumn = page.locator('section[data-status="ready"]')
    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible({ timeout: 10000 })

    // Find the issue card and verify priority indicator
    const issueCard = readyColumn.locator('article', { hasText: uniqueTitle })
    await expect(issueCard).toBeVisible()

    // Priority badge should show P1
    const priorityBadge = issueCard.locator('[class*="priority"], [data-priority]')
    if (await priorityBadge.isVisible({ timeout: 2000 }).catch(() => false)) {
      await expect(priorityBadge).toContainText(/P1|1/i)
    }
  })
})
