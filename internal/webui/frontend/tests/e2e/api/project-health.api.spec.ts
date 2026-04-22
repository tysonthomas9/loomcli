/**
 * Project Health API E2E Tests
 *
 * Story 6: As an operator, I want to monitor system health and statistics.
 * Tests health and stats endpoints used by MonitorDashboard.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

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

  test.describe('Blocked Issues', () => {
    test('blocked endpoint reflects dependency relationships', async ({ api }) => {
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

      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
      })

      const blockedIssues = await api.blocked()

      const ourBlocked = blockedIssues.find((issue) => issue.id === blocked.id)
      expect(ourBlocked).toBeDefined()
      expect(ourBlocked?.blocked_by).toContain(blocker.id)
    })
  })
})
