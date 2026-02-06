/**
 * Dependency Management API E2E Tests
 *
 * Story 3: As a team lead, I want to manage blockers and dependencies.
 * Tests dependency CRUD operations used by GraphView and IssueDetailPanel Dependencies section.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests create/modify shared state
test.describe.configure({ mode: 'serial' })

test.describe('Dependency Management', () => {
  // Track created issues for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up all created issues (in reverse order to handle dependencies)
    for (const id of [...createdIssueIds].reverse()) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Add Dependencies', () => {
    test('add blocking dependency (POST /api/issues/:id/dependencies)', async ({ api }) => {
      // Create two issues: A will block B
      const issueA = await api.createIssue({
        title: `Blocker Issue ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueA.id)

      const issueB = await api.createIssue({
        title: `Blocked Issue ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueB.id)

      // Add dependency: B depends on A (A blocks B)
      // addDependency returns void on success, throws on error
      await api.addDependency(issueB.id, {
        depends_on_id: issueA.id,
        dep_type: 'blocks',
      })

      // Verify by checking that B now has A as a dependency
      const issueDetails = await api.getIssue(issueB.id)
      expect(issueDetails.dependencies).toBeDefined()
      expect(Array.isArray(issueDetails.dependencies)).toBe(true)
      expect(issueDetails.dependencies.length).toBeGreaterThan(0)
    })

    test('dependency appears in issue details', async ({ api }) => {
      // Create blocker and blocked issues
      const blocker = await api.createIssue({
        title: `Blocker for Details ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: `Blocked for Details ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Add dependency
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
        dep_type: 'blocks',
      })

      // Get blocked issue details
      const details = await api.getIssue(blocked.id)

      expect(details.dependencies).toBeDefined()
      expect(Array.isArray(details.dependencies)).toBe(true)

      // Find the dependency we created
      const dep = details.dependencies.find((d) => d.id === blocker.id)
      expect(dep).toBeDefined()
      expect(dep!.dependency_type).toBe('blocks')
    })
  })

  test.describe('Blocking Behavior', () => {
    test('blocked issue moves to blocked list', async ({ api }) => {
      // Create blocker and blocked issues
      const blocker = await api.createIssue({
        title: `Blocker Move Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: `Blocked Move Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Add blocking dependency
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
        dep_type: 'blocks',
      })

      // Check blocked list
      const blockedList = await api.blocked()

      expect(Array.isArray(blockedList)).toBe(true)

      // Our blocked issue should be in the blocked list
      const foundBlocked = blockedList.some((issue) => issue.id === blocked.id)
      expect(foundBlocked).toBe(true)
    })

    test('blocked issue removed from ready list', async ({ api }) => {
      // Create blocker and blocked issues
      const blocker = await api.createIssue({
        title: `Blocker Ready Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: `Blocked Ready Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Verify blocked issue is in ready list BEFORE adding dependency
      const beforeReady = await api.ready()
      const inReadyBefore = beforeReady.some((issue) => issue.id === blocked.id)
      expect(inReadyBefore).toBe(true)

      // Add blocking dependency
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
        dep_type: 'blocks',
      })

      // Verify blocked issue is NOT in ready list AFTER adding dependency
      const afterReady = await api.ready()
      const inReadyAfter = afterReady.some((issue) => issue.id === blocked.id)
      expect(inReadyAfter).toBe(false)
    })
  })

  test.describe('Remove Dependencies', () => {
    test('remove dependency (DELETE /api/issues/:id/dependencies/:depId)', async ({ api }) => {
      // Create blocker and blocked issues
      const blocker = await api.createIssue({
        title: `Blocker Remove Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: `Blocked Remove Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Add dependency
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
        dep_type: 'blocks',
      })

      // Verify blocked
      const blockedBefore = await api.blocked()
      expect(blockedBefore.some((i) => i.id === blocked.id)).toBe(true)

      // Remove dependency
      await api.removeDependency(blocked.id, blocker.id)

      // Verify no longer blocked
      const blockedAfter = await api.blocked()
      expect(blockedAfter.some((i) => i.id === blocked.id)).toBe(false)
    })

    test('closing blocker unblocks dependent', async ({ api }) => {
      // Create blocker and blocked issues
      const blocker = await api.createIssue({
        title: `Blocker Close Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: `Blocked Close Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Add blocking dependency
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
        dep_type: 'blocks',
      })

      // Verify blocked
      const blockedBefore = await api.blocked()
      expect(blockedBefore.some((i) => i.id === blocked.id)).toBe(true)

      // Close the blocker
      await api.closeIssue(blocker.id)

      // Verify no longer blocked (should be back in ready)
      const blockedAfter = await api.blocked()
      expect(blockedAfter.some((i) => i.id === blocked.id)).toBe(false)

      // Verify now in ready list
      const readyAfter = await api.ready()
      expect(readyAfter.some((i) => i.id === blocked.id)).toBe(true)
    })
  })

  test.describe('Blocked Endpoint', () => {
    test('GET /api/blocked returns issues with open deps', async ({ api }) => {
      // Create blocker and blocked issues
      const blocker = await api.createIssue({
        title: `Blocker Endpoint Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocker.id)

      const blocked = await api.createIssue({
        title: `Blocked Endpoint Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(blocked.id)

      // Add blocking dependency
      await api.addDependency(blocked.id, {
        depends_on_id: blocker.id,
        dep_type: 'blocks',
      })

      // Get blocked issues
      const blockedList = await api.blocked()

      expect(Array.isArray(blockedList)).toBe(true)

      // Find our blocked issue
      const foundIssue = blockedList.find((i) => i.id === blocked.id)
      expect(foundIssue).toBeDefined()

      // Verify blocked_by information
      expect(foundIssue!.blocked_by).toBeDefined()
      expect(Array.isArray(foundIssue!.blocked_by)).toBe(true)
      expect(foundIssue!.blocked_by).toContain(blocker.id)
    })
  })

  test.describe('Graph Endpoint', () => {
    test('GET /api/issues/graph returns nodes and edges', async ({ api }) => {
      // Create issues with dependencies
      const issueA = await api.createIssue({
        title: `Graph Node A ${generateTestId()}`,
        issue_type: 'task',
        priority: 1,
      })
      createdIssueIds.push(issueA.id)

      const issueB = await api.createIssue({
        title: `Graph Node B ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueB.id)

      // Add dependency: B depends on A
      await api.addDependency(issueB.id, {
        depends_on_id: issueA.id,
        dep_type: 'blocks',
      })

      // Get graph
      const graphIssues = await api.graph()

      expect(Array.isArray(graphIssues)).toBe(true)

      // Find our issues in the graph
      const nodeA = graphIssues.find((i) => i.id === issueA.id)
      const nodeB = graphIssues.find((i) => i.id === issueB.id)

      expect(nodeA).toBeDefined()
      expect(nodeB).toBeDefined()

      // Verify node structure
      expect(nodeA!.title).toContain('Graph Node A')
      expect(nodeA!.priority).toBe(1)
      expect(nodeA!.issue_type).toBe('task')

      // Verify nodeB has dependency on nodeA
      expect(nodeB!.dependencies).toBeDefined()
      expect(Array.isArray(nodeB!.dependencies)).toBe(true)

      const depEdge = nodeB!.dependencies!.find((d) => d.depends_on_id === issueA.id)
      expect(depEdge).toBeDefined()
    })

    test('graph includes dependency type (blocks vs related)', async ({ api }) => {
      // Create three issues for different dependency types
      const issueA = await api.createIssue({
        title: `Graph Type A ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueA.id)

      const issueB = await api.createIssue({
        title: `Graph Type B ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueB.id)

      const issueC = await api.createIssue({
        title: `Graph Type C ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueC.id)

      // Add blocking dependency: B depends on A (blocking)
      await api.addDependency(issueB.id, {
        depends_on_id: issueA.id,
        dep_type: 'blocks',
      })

      // Add related dependency: C related to A (non-blocking)
      await api.addDependency(issueC.id, {
        depends_on_id: issueA.id,
        dep_type: 'related',
      })

      // Get graph
      const graphIssues = await api.graph()

      // Find nodeB and nodeC
      const nodeB = graphIssues.find((i) => i.id === issueB.id)
      const nodeC = graphIssues.find((i) => i.id === issueC.id)

      expect(nodeB).toBeDefined()
      expect(nodeC).toBeDefined()

      // Verify dependency types
      const blockingDep = nodeB!.dependencies!.find((d) => d.depends_on_id === issueA.id)
      expect(blockingDep).toBeDefined()
      expect(blockingDep!.type).toBe('blocks')

      const relatedDep = nodeC!.dependencies!.find((d) => d.depends_on_id === issueA.id)
      expect(relatedDep).toBeDefined()
      expect(relatedDep!.type).toBe('related')
    })
  })

  test.describe('Validation', () => {
    test('reject: circular dependency returns error', async ({ api, request }) => {
      // Create two issues
      const issueA = await api.createIssue({
        title: `Circular A ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueA.id)

      const issueB = await api.createIssue({
        title: `Circular B ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issueB.id)

      // Add dependency: B depends on A
      await api.addDependency(issueB.id, {
        depends_on_id: issueA.id,
        dep_type: 'blocks',
      })

      // Try to add circular dependency: A depends on B (should fail)
      // Use raw request to check status code
      const response = await request.post(
        `http://localhost:8080/api/issues/${issueA.id}/dependencies`,
        {
          data: {
            depends_on_id: issueB.id,
            dep_type: 'blocks',
          },
        }
      )

      // Should return 409 Conflict for circular dependency
      expect(response.status()).toBe(409)

      const body = await response.json()
      expect(body.success).toBe(false)
      expect(body.error.toLowerCase()).toContain('cycle')
    })
  })
})
