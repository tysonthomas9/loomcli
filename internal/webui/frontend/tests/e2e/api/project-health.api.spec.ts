/**
 * Project Health API E2E Tests
 *
 * Story 6: As an operator, I want to monitor system health and statistics.
 * Tests health and stats endpoints used by MonitorDashboard.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: stats tests modify shared state
test.describe.configure({ mode: 'serial' })

test.describe('Project Health', () => {
  // Test data tracking for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up all issues created during tests
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Health Endpoints', () => {
    test('GET /health returns ok status', async ({ api }) => {
      const response = await api.health()
      expect(response.status).toBe('ok')
    })

    test('GET /api/health shows daemon connection status', async ({ api }) => {
      const response = await api.apiHealth()

      // Verify status field exists and is one of expected values
      expect(['ok', 'degraded', 'unhealthy']).toContain(response.status)

      // Verify daemon connection info is present
      expect(response.daemon).toBeDefined()
      expect(response.daemon.connected).toBe(true)
      expect(['ok', 'healthy']).toContain(response.daemon.status)
      expect(typeof response.daemon.uptime).toBe('number')
    })
  })

  test.describe('Stats Endpoint', () => {
    test('GET /api/stats returns issue counts', async ({ api }) => {
      const stats = await api.stats()

      // Verify stats fields exist and are numbers
      expect(typeof stats.open_issues).toBe('number')
      expect(typeof stats.closed_issues).toBe('number')
      expect(typeof stats.total_issues).toBe('number')
      expect(typeof stats.blocked_issues).toBe('number')
      expect(typeof stats.in_progress_issues).toBe('number')

      // All counts should be non-negative
      expect(stats.open_issues).toBeGreaterThanOrEqual(0)
      expect(stats.closed_issues).toBeGreaterThanOrEqual(0)
      expect(stats.total_issues).toBeGreaterThanOrEqual(0)
    })

    test('stats updates after creating an issue', async ({ api }) => {
      // Capture initial counts
      const before = await api.stats()
      const initialOpen = before.open_issues
      const initialTotal = before.total_issues

      // Create a new issue
      const title = `Stats Test Issue ${generateTestId()}`
      const created = await api.createIssue({
        title,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Verify counts increased
      const after = await api.stats()
      expect(after.open_issues).toBe(initialOpen + 1)
      expect(after.total_issues).toBe(initialTotal + 1)
    })

    test('stats updates after closing an issue', async ({ api }) => {
      // Create an issue first
      const title = `Close Stats Test ${generateTestId()}`
      const created = await api.createIssue({
        title,
        issue_type: 'task',
        priority: 2,
      })
      const issueId = created.id
      createdIssueIds.push(issueId) // Track for cleanup if test fails early

      // Capture counts after creation
      const before = await api.stats()
      const initialOpen = before.open_issues
      const initialClosed = before.closed_issues

      // Close the issue
      await api.closeIssue(issueId)

      // Verify counts shifted (open decreased, closed increased)
      const after = await api.stats()
      expect(after.open_issues).toBe(initialOpen - 1)
      expect(after.closed_issues).toBe(initialClosed + 1)
      // Total should remain the same
      expect(after.total_issues).toBe(before.total_issues)
    })

    test('stats reflects blocked issue count', async ({ api }) => {
      // Create two issues: one will block the other
      const blockerTitle = `Blocker Issue ${generateTestId()}`
      const blockedTitle = `Blocked Issue ${generateTestId()}`

      const blocker = await api.createIssue({
        title: blockerTitle,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: blockedTitle,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Add dependency: blocked depends on blocker
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
      })

      // Verify blocked issues endpoint sees the blocked issue
      const blockedIssues = await api.blocked()

      // Find our blocked issue in the response
      const foundBlocked = blockedIssues.some(
        (issue) => issue.id === blocked.id
      )
      expect(foundBlocked).toBe(true)

      // Verify the blocked issue has the blocker in its blocked_by list
      const ourBlocked = blockedIssues.find(issue => issue.id === blocked.id)
      expect(ourBlocked?.blocked_by).toContain(blocker.id)
    })
  })
})
