/**
 * Issue Triage API E2E Tests
 *
 * Story 7: As a triage lead, I want to categorize and prioritize incoming issues.
 * Tests type, priority, label, and defer operations used by FilterBar and priority badges.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests create/modify shared state
test.describe.configure({ mode: 'serial' })

test.describe('Issue Triage', () => {
  // Track created issues for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up all created issues
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Issue Type', () => {
    test('set issue type: bug, feature, task, epic, chore', async ({ api }) => {
      // Create a task issue
      const issue = await api.createIssue({
        title: `Type Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify initial type
      let details = await api.getIssue(issue.id)
      expect(details.issue_type).toBe('task')

      // Test all issue types by changing one at a time
      const types: Array<'bug' | 'feature' | 'task' | 'epic' | 'chore'> = [
        'bug',
        'feature',
        'epic',
        'chore',
        'task', // back to task
      ]

      for (const issueType of types) {
        await api.updateIssue(issue.id, { issue_type: issueType })
        details = await api.getIssue(issue.id)
        expect(details.issue_type).toBe(issueType)
      }
    })
  })

  test.describe('Priority', () => {
    test('set priority: 0 (critical) to 4 (backlog)', async ({ api }) => {
      // Create issue with default priority
      const issue = await api.createIssue({
        title: `Priority Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify initial priority
      let details = await api.getIssue(issue.id)
      expect(details.priority).toBe(2)

      // Test all priority levels
      const priorities: Array<0 | 1 | 2 | 3 | 4> = [0, 1, 2, 3, 4]

      for (const priority of priorities) {
        await api.updateIssue(issue.id, { priority })
        details = await api.getIssue(issue.id)
        expect(details.priority).toBe(priority)
      }
    })
  })

  test.describe('Labels', () => {
    test('add labels to issue', async ({ api }) => {
      // Create issue without labels
      const issue = await api.createIssue({
        title: `Add Labels Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify no labels initially
      let details = await api.getIssue(issue.id)
      expect(details.labels || []).toHaveLength(0)

      // Add first label
      await api.updateIssue(issue.id, { add_labels: ['urgent'] })
      details = await api.getIssue(issue.id)
      expect(details.labels).toContain('urgent')
      expect(details.labels).toHaveLength(1)

      // Add more labels
      await api.updateIssue(issue.id, { add_labels: ['api', 'backend'] })
      details = await api.getIssue(issue.id)
      expect(details.labels).toContain('urgent')
      expect(details.labels).toContain('api')
      expect(details.labels).toContain('backend')
      expect(details.labels).toHaveLength(3)
    })

    test('remove label from issue', async ({ api }) => {
      // Create issue with labels
      const issue = await api.createIssue({
        title: `Remove Labels Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
        labels: ['urgent', 'api', 'backend'],
      })
      createdIssueIds.push(issue.id)

      // Verify initial labels
      let details = await api.getIssue(issue.id)
      expect(details.labels).toHaveLength(3)
      expect(details.labels).toContain('urgent')
      expect(details.labels).toContain('api')
      expect(details.labels).toContain('backend')

      // Remove one label
      await api.updateIssue(issue.id, { remove_labels: ['api'] })
      details = await api.getIssue(issue.id)
      expect(details.labels).not.toContain('api')
      expect(details.labels).toContain('urgent')
      expect(details.labels).toContain('backend')
      expect(details.labels).toHaveLength(2)

      // Remove another label
      await api.updateIssue(issue.id, { remove_labels: ['urgent'] })
      details = await api.getIssue(issue.id)
      expect(details.labels).not.toContain('urgent')
      expect(details.labels).toContain('backend')
      expect(details.labels).toHaveLength(1)
    })

    test('filter issues by label', async ({ api }) => {
      // Use unique label for test isolation
      const testLabel = `test-filter-${generateTestId()}`

      // Create issues with different labels
      const taggedIssue = await api.createIssue({
        title: `Filter Label Test Tagged ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
        labels: [testLabel],
      })
      createdIssueIds.push(taggedIssue.id)

      const untaggedIssue = await api.createIssue({
        title: `Filter Label Test Untagged ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(untaggedIssue.id)

      // Filter by label using ready endpoint
      const readyWithLabel = await api.ready({ labels: testLabel })

      // Tagged issue should be in results
      const hasTagged = readyWithLabel.some((i) => i.id === taggedIssue.id)
      expect(hasTagged).toBe(true)

      // Untagged issue should NOT be in filtered results
      const hasUntagged = readyWithLabel.some((i) => i.id === untaggedIssue.id)
      expect(hasUntagged).toBe(false)

      // Also test via listIssues endpoint
      const issuesWithLabel = await api.listIssues({ labels: testLabel })
      expect(issuesWithLabel.some((i) => i.id === taggedIssue.id)).toBe(true)
      expect(issuesWithLabel.some((i) => i.id === untaggedIssue.id)).toBe(false)
    })
  })

  test.describe('Defer', () => {
    test('defer issue: sets status to deferred', async ({ api }) => {
      // Create an open issue
      const issue = await api.createIssue({
        title: `Defer Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify initial status
      let details = await api.getIssue(issue.id)
      expect(details.status).toBe('open')

      // Defer the issue
      await api.updateIssue(issue.id, { status: 'deferred' })

      // Verify deferred status
      details = await api.getIssue(issue.id)
      expect(details.status).toBe('deferred')
    })

    test('deferred issues hidden from ready list', async ({ api }) => {
      // Create two issues with a unique label for isolation
      const testLabel = `test-deferred-${generateTestId()}`

      const deferredIssue = await api.createIssue({
        title: `Deferred Hidden Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
        labels: [testLabel],
      })
      createdIssueIds.push(deferredIssue.id)

      const openIssue = await api.createIssue({
        title: `Open Hidden Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
        labels: [testLabel],
      })
      createdIssueIds.push(openIssue.id)

      // Verify both initially in ready list (filtered by label for isolation)
      let readyList = await api.ready({ labels: testLabel })
      expect(readyList.some((i) => i.id === deferredIssue.id)).toBe(true)
      expect(readyList.some((i) => i.id === openIssue.id)).toBe(true)

      // Defer one issue
      await api.updateIssue(deferredIssue.id, { status: 'deferred' })

      // Verify deferred issue is NOT in ready list (status='deferred' is excluded)
      readyList = await api.ready({ labels: testLabel })
      expect(readyList.some((i) => i.id === deferredIssue.id)).toBe(false)
      expect(readyList.some((i) => i.id === openIssue.id)).toBe(true)

      // Verify deferred issue still exists via listIssues (it's in the database, just not "ready")
      const allIssues = await api.listIssues({ labels: testLabel })
      const deferredInList = allIssues.find((i) => i.id === deferredIssue.id)
      expect(deferredInList).toBeDefined()
      expect(deferredInList!.status).toBe('deferred')
    })
  })

  test.describe('Bulk Operations', () => {
    test('bulk close multiple issues', async ({ api }) => {
      // Create multiple issues
      const issue1 = await api.createIssue({
        title: `Bulk Close 1 ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue1.id)

      const issue2 = await api.createIssue({
        title: `Bulk Close 2 ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue2.id)

      const issue3 = await api.createIssue({
        title: `Bulk Close 3 ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue3.id)

      // Verify all are open
      for (const id of [issue1.id, issue2.id, issue3.id]) {
        const details = await api.getIssue(id)
        expect(details.status).toBe('open')
      }

      // Close all issues (bulk operation via parallel calls)
      const closeResults = await Promise.all([
        api.closeIssue(issue1.id),
        api.closeIssue(issue2.id),
        api.closeIssue(issue3.id),
      ])

      // Verify all closes succeeded
      expect(closeResults).toHaveLength(3)
      for (const result of closeResults) {
        expect(result.status).toBe('closed')
      }

      // Verify all are closed
      for (const id of [issue1.id, issue2.id, issue3.id]) {
        const details = await api.getIssue(id)
        expect(details.status).toBe('closed')
      }
      // Let afterEach handle cleanup consistently (cleanupIssue is idempotent)
    })
  })
})
