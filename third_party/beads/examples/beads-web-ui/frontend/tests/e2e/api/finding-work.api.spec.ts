/**
 * Finding Work API E2E Tests
 *
 * Story 2: As a developer, I want to find issues ready to pick up.
 * Tests /api/ready endpoint with filtering capabilities used by FilterBar
 * and the Kanban Ready column.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests depend on shared test data
test.describe.configure({ mode: 'serial' })

test.describe('Finding Work', () => {
  // Test data tracking
  interface TestIssue {
    id: string
    title: string
    priority: number
    type: string
    assignee?: string
    labels?: string[]
  }

  const testIssues: TestIssue[] = []
  let blockedIssueId: string
  let blockerIssueId: string
  let deferredIssueId: string
  let closedIssueId: string

  test.beforeAll(async ({ api }) => {
    const testId = generateTestId()

    // Create issues with different priorities
    const p0Issue = await api.createIssue({
      title: `P0 Critical Bug ${testId}`,
      issue_type: 'bug',
      priority: 0,
      labels: ['urgent', 'api'],
      assignee: 'developer1',
    })
    testIssues.push({
      id: p0Issue.id,
      title: p0Issue.title,
      priority: 0,
      type: 'bug',
      assignee: 'developer1',
      labels: ['urgent', 'api'],
    })

    const p1Issue = await api.createIssue({
      title: `P1 Feature Request ${testId}`,
      issue_type: 'feature',
      priority: 1,
      labels: ['frontend'],
      assignee: 'developer2',
    })
    testIssues.push({
      id: p1Issue.id,
      title: p1Issue.title,
      priority: 1,
      type: 'feature',
      assignee: 'developer2',
      labels: ['frontend'],
    })

    const p2Task = await api.createIssue({
      title: `P2 Backend Task ${testId}`,
      issue_type: 'task',
      priority: 2,
      labels: ['backend', 'api'],
    })
    testIssues.push({
      id: p2Task.id,
      title: p2Task.title,
      priority: 2,
      type: 'task',
      labels: ['backend', 'api'],
    })

    const p3Bug = await api.createIssue({
      title: `P3 Minor Bug ${testId}`,
      issue_type: 'bug',
      priority: 3,
      labels: ['api'],
      assignee: 'developer1',
    })
    testIssues.push({
      id: p3Bug.id,
      title: p3Bug.title,
      priority: 3,
      type: 'bug',
      assignee: 'developer1',
      labels: ['api'],
    })

    const p4Task = await api.createIssue({
      title: `P4 Backlog Task ${testId}`,
      issue_type: 'task',
      priority: 4,
      assignee: 'developer2',
    })
    testIssues.push({
      id: p4Task.id,
      title: p4Task.title,
      priority: 4,
      type: 'task',
      assignee: 'developer2',
    })

    // Create blocker and blocked issue
    const blocker = await api.createIssue({
      title: `Blocker Issue ${testId}`,
      issue_type: 'task',
      priority: 2,
    })
    blockerIssueId = blocker.id
    testIssues.push({
      id: blockerIssueId,
      title: blocker.title,
      priority: 2,
      type: 'task',
    })

    const blocked = await api.createIssue({
      title: `Blocked Issue ${testId}`,
      issue_type: 'task',
      priority: 2,
    })
    blockedIssueId = blocked.id
    // Add dependency: blocked depends on blocker
    await api.addDependency(blockedIssueId, {
      depends_on_id: blockerIssueId,
    })

    // Create deferred issue (defer_until in future)
    const futureDate = new Date()
    futureDate.setDate(futureDate.getDate() + 7) // 7 days from now
    const deferred = await api.createIssue({
      title: `Deferred Issue ${testId}`,
      issue_type: 'task',
      priority: 2,
      defer_until: futureDate.toISOString(),
    })
    deferredIssueId = deferred.id

    // Create and close an issue
    const toClose = await api.createIssue({
      title: `Closed Issue ${testId}`,
      issue_type: 'task',
      priority: 2,
    })
    closedIssueId = toClose.id
    await api.closeIssue(closedIssueId)
  })

  test.afterAll(async ({ api }) => {
    // Clean up all test issues
    for (const issue of testIssues) {
      await api.cleanupIssue(issue.id)
    }
    await api.cleanupIssue(blockedIssueId)
    await api.cleanupIssue(deferredIssueId)
    await api.cleanupIssue(blockerIssueId)
    await api.cleanupIssue(closedIssueId)
  })

  test.describe('Basic Ready List', () => {
    test('GET /api/ready returns open unblocked issues', async ({ api }) => {
      const response = await api.ready()

      // Response should be an array of issues
      expect(Array.isArray(response)).toBe(true)

      // All returned issues should be workable (not closed, blocked, or deferred)
      for (const issue of response) {
        expect(['open', 'in_progress', 'review']).toContain(issue.status)
      }

      // Our test issues should be in the ready list
      const ids = response.map((i) => i.id)
      for (const testIssue of testIssues) {
        expect(ids).toContain(testIssue.id)
      }
    })
  })

  test.describe('Priority Filtering', () => {
    test('filter by priority (?priority=0)', async ({ api }) => {
      const response = await api.ready({ priority: 0 })

      // All returned issues should have priority 0
      for (const issue of response) {
        expect(issue.priority).toBe(0)
      }

      // Our P0 bug should be present
      const p0Issue = testIssues.find((i) => i.priority === 0)
      const ids = response.map((i) => i.id)
      expect(ids).toContain(p0Issue?.id)
    })
  })

  test.describe('Type Filtering', () => {
    test('filter by type (?type=bug)', async ({ api }) => {
      const response = await api.ready({ type: 'bug' })

      // All returned issues should be bugs
      for (const issue of response) {
        expect(issue.issue_type).toBe('bug')
      }

      // Our bug issues should be present
      const bugIds = testIssues.filter((i) => i.type === 'bug').map((i) => i.id)
      const returnedIds = response.map((i) => i.id)
      for (const bugId of bugIds) {
        expect(returnedIds).toContain(bugId)
      }
    })
  })

  test.describe('Assignee Filtering', () => {
    test('filter by assignee (?assignee=developer1)', async ({ api }) => {
      const response = await api.ready({ assignee: 'developer1' })

      // All returned issues should be assigned to developer1
      for (const issue of response) {
        expect(issue.assignee).toBe('developer1')
      }

      // Our developer1 issues should be present
      const dev1Ids = testIssues
        .filter((i) => i.assignee === 'developer1')
        .map((i) => i.id)
      const returnedIds = response.map((i) => i.id)
      for (const dev1Id of dev1Ids) {
        expect(returnedIds).toContain(dev1Id)
      }
    })
  })

  test.describe('Label Filtering', () => {
    test('filter by labels (?labels=api,urgent)', async ({ api }) => {
      // Labels parameter is a comma-separated string for AND matching
      const response = await api.ready({ labels: 'api,urgent' })

      // All returned issues should have BOTH api AND urgent labels
      for (const issue of response) {
        expect(issue.labels).toContain('api')
        expect(issue.labels).toContain('urgent')
      }

      // Our P0 bug has both labels and should be present
      const p0Issue = testIssues.find(
        (i) => i.labels?.includes('api') && i.labels?.includes('urgent')
      )
      const returnedIds = response.map((i) => i.id)
      expect(returnedIds).toContain(p0Issue?.id)
    })
  })

  test.describe('Combined Filtering', () => {
    test('combine multiple filters', async ({ api }) => {
      // Filter: bugs assigned to developer1
      const response = await api.ready({
        type: 'bug',
        assignee: 'developer1',
      })

      // All returned issues should match ALL criteria
      for (const issue of response) {
        expect(issue.issue_type).toBe('bug')
        expect(issue.assignee).toBe('developer1')
      }

      // Our P0 and P3 bugs from developer1 should be present
      const matchingIds = testIssues
        .filter((i) => i.type === 'bug' && i.assignee === 'developer1')
        .map((i) => i.id)
      const returnedIds = response.map((i) => i.id)
      for (const matchId of matchingIds) {
        expect(returnedIds).toContain(matchId)
      }
    })
  })

  test.describe('Exclusions', () => {
    test('blocked issues excluded from ready', async ({ api }) => {
      const response = await api.ready()

      // Blocked issue should NOT be in the ready list
      const ids = response.map((i) => i.id)
      expect(ids).not.toContain(blockedIssueId)
    })

    test('deferred issues excluded from ready', async ({ api }) => {
      const response = await api.ready()

      // Deferred issue should NOT be in the ready list
      const ids = response.map((i) => i.id)
      expect(ids).not.toContain(deferredIssueId)
    })

    test('closed issues excluded from ready', async ({ api }) => {
      const response = await api.ready()

      // Closed issue should NOT be in the ready list
      const ids = response.map((i) => i.id)
      expect(ids).not.toContain(closedIssueId)
    })
  })

  test.describe('Sorting', () => {
    test('ready list sorted by priority then created', async ({ api }) => {
      const response = await api.ready({ sort: 'priority' })

      // Verify priority ordering: lower number = higher priority = comes first
      let lastPriority = -1
      for (const issue of response) {
        expect(issue.priority).toBeGreaterThanOrEqual(lastPriority)
        lastPriority = issue.priority
      }

      // Our P0 issue should come before our P4 issue
      const p0Issue = testIssues.find((i) => i.priority === 0)
      const p4Issue = testIssues.find((i) => i.priority === 4)
      if (p0Issue && p4Issue) {
        const p0Index = response.findIndex((i) => i.id === p0Issue.id)
        const p4Index = response.findIndex((i) => i.id === p4Issue.id)

        if (p0Index !== -1 && p4Index !== -1) {
          expect(p0Index).toBeLessThan(p4Index)
        }
      }
    })
  })
})
