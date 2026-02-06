/**
 * Agent Monitoring (Loom) API E2E Tests
 *
 * Story 11: As an operator, I want to monitor agent status via loom.
 * Tests loom proxy endpoints used by MonitorDashboard Agent Sidebar.
 *
 * Prerequisites:
 * - bd-imhr.1 (API test infrastructure)
 * - bd-imhr.2 (loom service in E2E stack)
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

const LOOM_PROXY_BASE = 'http://localhost:8080/api/loom'

// Serial mode: integration test creates issues that affect loom state
test.describe.configure({ mode: 'serial' })

test.describe('Agent Monitoring (Loom)', () => {
  // Track created issues for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up created issues using api fixture for consistency
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Health Endpoint', () => {
    test('GET /api/loom/health returns ok', async () => {
      const response = await fetch(`${LOOM_PROXY_BASE}/health`)

      expect(response.ok).toBe(true)

      const body = await response.json()

      // Loom health endpoint returns status field
      expect(body.status).toBe('ok')
    })
  })

  test.describe('Agents Endpoint', () => {
    test('GET /api/loom/api/agents returns agent list', async () => {
      const response = await fetch(`${LOOM_PROXY_BASE}/api/agents`)

      expect(response.ok).toBe(true)

      const body = await response.json()

      // Response contains agents array (may be empty or null in E2E env)
      expect(body).toHaveProperty('agents')
      // agents can be null or an array
      if (body.agents !== null) {
        expect(Array.isArray(body.agents)).toBe(true)
      }

      // Response contains timestamp
      expect(body).toHaveProperty('timestamp')
      expect(typeof body.timestamp).toBe('string')
    })

    test('agent data includes name, branch, status fields', async () => {
      const response = await fetch(`${LOOM_PROXY_BASE}/api/agents`)

      expect(response.ok).toBe(true)

      const body = await response.json()

      // If there are agents, verify their structure
      if (body.agents && body.agents.length > 0) {
        const agent = body.agents[0]

        // Required fields per LoomAgentStatus type
        expect(agent).toHaveProperty('name')
        expect(typeof agent.name).toBe('string')

        expect(agent).toHaveProperty('branch')
        expect(typeof agent.branch).toBe('string')

        expect(agent).toHaveProperty('status')
        expect(typeof agent.status).toBe('string')

        expect(agent).toHaveProperty('ahead')
        expect(typeof agent.ahead).toBe('number')

        expect(agent).toHaveProperty('behind')
        expect(typeof agent.behind).toBe('number')
      }
      // Note: No agents running is valid in E2E environment
      // Test passes as long as endpoint works - agent structure verified if present
    })
  })

  test.describe('Status Endpoint', () => {
    test('GET /api/loom/api/status returns system overview', async () => {
      const response = await fetch(`${LOOM_PROXY_BASE}/api/status`)

      expect(response.ok).toBe(true)

      const body = await response.json()

      // Verify top-level structure
      expect(body).toHaveProperty('agents')
      expect(body).toHaveProperty('tasks')
      expect(body).toHaveProperty('sync')
      expect(body).toHaveProperty('stats')
      expect(body).toHaveProperty('timestamp')

      // Verify sync structure
      expect(body.sync).toHaveProperty('db_synced')
      expect(typeof body.sync.db_synced).toBe('boolean')
      expect(body.sync).toHaveProperty('db_last_sync')

      // Verify stats structure
      expect(typeof body.stats.open).toBe('number')
      expect(typeof body.stats.closed).toBe('number')
      expect(typeof body.stats.total).toBe('number')
      expect(typeof body.stats.completion).toBe('number')
    })

    test('status includes task counts by workflow state', async () => {
      const response = await fetch(`${LOOM_PROXY_BASE}/api/status`)

      expect(response.ok).toBe(true)

      const body = await response.json()

      // Verify tasks structure matches LoomTaskSummary
      // Note: API uses "backlog" but frontend maps to "blocked"
      expect(body.tasks).toHaveProperty('needs_planning')
      expect(typeof body.tasks.needs_planning).toBe('number')

      expect(body.tasks).toHaveProperty('ready_to_implement')
      expect(typeof body.tasks.ready_to_implement).toBe('number')

      expect(body.tasks).toHaveProperty('in_progress')
      expect(typeof body.tasks.in_progress).toBe('number')

      expect(body.tasks).toHaveProperty('need_review')
      expect(typeof body.tasks.need_review).toBe('number')

      // API returns "backlog" field (frontend maps this to "blocked")
      expect(body.tasks).toHaveProperty('backlog')
      expect(typeof body.tasks.backlog).toBe('number')

      // All counts should be non-negative
      expect(body.tasks.needs_planning).toBeGreaterThanOrEqual(0)
      expect(body.tasks.ready_to_implement).toBeGreaterThanOrEqual(0)
      expect(body.tasks.in_progress).toBeGreaterThanOrEqual(0)
      expect(body.tasks.need_review).toBeGreaterThanOrEqual(0)
      expect(body.tasks.backlog).toBeGreaterThanOrEqual(0)
    })
  })

  test.describe('Tasks Endpoint', () => {
    test('GET /api/loom/api/tasks returns task distribution', async () => {
      const response = await fetch(`${LOOM_PROXY_BASE}/api/tasks`)

      expect(response.ok).toBe(true)

      const body = await response.json()

      // Verify response contains task lists by category
      // LoomTaskLists structure: arrays or null for each workflow state
      // Helper to check array-or-null pattern
      const isArrayOrNull = (val: unknown) => val === null || Array.isArray(val)

      expect(body).toHaveProperty('needs_planning')
      expect(isArrayOrNull(body.needs_planning)).toBe(true)

      expect(body).toHaveProperty('ready_to_implement')
      expect(isArrayOrNull(body.ready_to_implement)).toBe(true)

      expect(body).toHaveProperty('in_progress')
      expect(isArrayOrNull(body.in_progress)).toBe(true)

      // Note: /api/tasks uses "needs_review" while /api/status uses "need_review"
      expect(body).toHaveProperty('needs_review')
      expect(isArrayOrNull(body.needs_review)).toBe(true)

      // API uses "backlog" for blocked tasks
      expect(body).toHaveProperty('backlog')
      expect(isArrayOrNull(body.backlog)).toBe(true)

      // Verify summary structure is present with counts
      expect(body).toHaveProperty('summary')
      expect(typeof body.summary.needs_planning).toBe('number')
      expect(typeof body.summary.ready_to_implement).toBe('number')
      expect(typeof body.summary.in_progress).toBe('number')
    })
  })

  test.describe('Beads-Loom Integration', () => {
    test('loom reflects issue changes from beads daemon', async ({ api }) => {
      // Capture initial stats from loom
      const initialStatus = await fetch(`${LOOM_PROXY_BASE}/api/status`)
      const initialBody = await initialStatus.json()
      const initialTotal = initialBody.stats.total

      // Create a new issue via beads API using api fixture
      const uniqueTitle = `Loom Integration Test ${generateTestId()}`
      const created = await api.createIssue({
        title: uniqueTitle,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Wait for loom to sync with beads daemon
      // Loom polls periodically, so we need to retry
      await expect(async () => {
        const response = await fetch(`${LOOM_PROXY_BASE}/api/status`)
        const body = await response.json()

        // Total should increase by 1
        expect(body.stats.total).toBe(initialTotal + 1)
      }).toPass({
        timeout: 15000, // Loom may take time to sync
        intervals: [500, 1000, 1500, 2000, 2500], // More retry attempts
      })
    })
  })
})
