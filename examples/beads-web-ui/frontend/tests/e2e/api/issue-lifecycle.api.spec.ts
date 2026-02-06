/**
 * Issue Lifecycle API E2E Tests
 *
 * Story 1: As a developer, I want to manage issues through the Kanban status flow.
 * Tests issue CRUD and status transitions used by SwimLaneBoard and IssueDetailPanel.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests create/modify shared state
test.describe.configure({ mode: 'serial' })

test.describe('Issue Lifecycle', () => {
  // Track created issues for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up all created issues
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Create Operations', () => {
    test('create bug report with title and type', async ({ api }) => {
      const title = `Bug Report ${generateTestId()}`
      const created = await api.createIssue({
        title,
        issue_type: 'bug',
        priority: 1, // High priority bug
      })

      createdIssueIds.push(created.id)

      // Verify created issue fields
      expect(created.id).toMatch(/^bd-[a-z0-9]+$/)
      expect(created.title).toBe(title)
      expect(created.issue_type).toBe('bug')
      expect(created.priority).toBe(1)
      expect(created.status).toBe('open') // Default status
    })

    test('create feature with description and design field', async ({ api }) => {
      const title = `Feature Request ${generateTestId()}`
      const description = 'This feature will add new functionality to the system.'
      const design = '## Technical Approach\n\nImplement using React hooks.'

      const created = await api.createIssue({
        title,
        issue_type: 'feature',
        priority: 2,
        description,
        design,
      })

      createdIssueIds.push(created.id)

      expect(created.title).toBe(title)
      expect(created.issue_type).toBe('feature')
      expect(created.description).toBe(description)
      expect(created.design).toBe(design)
    })
  })

  test.describe('Read Operations', () => {
    test('view issue details including all fields', async ({ api }) => {
      // Create an issue with many fields populated
      const title = `Full Details Issue ${generateTestId()}`
      const created = await api.createIssue({
        title,
        issue_type: 'task',
        priority: 3,
        description: 'Issue description text',
        design: 'Design notes here',
        labels: ['api', 'test'],
      })
      createdIssueIds.push(created.id)

      // Fetch the issue details
      const issue = await api.getIssue(created.id)

      // Verify all expected fields are present
      expect(issue.id).toBe(created.id)
      expect(issue.title).toBe(title)
      expect(issue.issue_type).toBe('task')
      expect(issue.priority).toBe(3)
      expect(issue.status).toBe('open')
      expect(issue.description).toBe('Issue description text')
      expect(issue.design).toBe('Design notes here')
      expect(issue.labels).toEqual(['api', 'test'])

      // Verify timestamps are present
      expect(issue.created_at).toBeDefined()
      expect(issue.updated_at).toBeDefined()
    })
  })

  test.describe('Update Operations', () => {
    test('update title via PATCH', async ({ api }) => {
      // Create issue
      const originalTitle = `Original Title ${generateTestId()}`
      const created = await api.createIssue({
        title: originalTitle,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Update title
      const newTitle = `Updated Title ${generateTestId()}`
      const patchResponse = await api.updateIssue(created.id, {
        title: newTitle,
      })

      expect(patchResponse.id).toBe(created.id)

      // Verify by fetching
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.title).toBe(newTitle)
    })

    test('update description via PATCH', async ({ api }) => {
      // Create issue without description
      const created = await api.createIssue({
        title: `Description Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Add description via PATCH
      const description = 'This is the updated description text.'
      const patchResponse = await api.updateIssue(created.id, {
        description,
      })

      expect(patchResponse.id).toBe(created.id)

      // Verify
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.description).toBe(description)
    })
  })

  test.describe('Status Transitions (Kanban Flow)', () => {
    test('transition: open -> in_progress (claim work)', async ({ api }) => {
      // Create open issue
      const created = await api.createIssue({
        title: `Claim Work Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)
      expect(created.status).toBe('open')

      // Transition to in_progress
      await api.updateIssue(created.id, {
        status: 'in_progress',
      })

      // Verify new status
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.status).toBe('in_progress')
    })

    test('transition: in_progress -> review (submit for review)', async ({ api }) => {
      // Create and move to in_progress
      const created = await api.createIssue({
        title: `Review Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      await api.updateIssue(created.id, { status: 'in_progress' })

      // Transition to review
      await api.updateIssue(created.id, {
        status: 'review',
      })

      // Verify
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.status).toBe('review')
    })

    test('transition: review -> closed (approve)', async ({ api }) => {
      // Create and move through workflow to review
      const created = await api.createIssue({
        title: `Approve Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      await api.updateIssue(created.id, { status: 'in_progress' })
      await api.updateIssue(created.id, { status: 'review' })

      // Close via the close endpoint (which properly sets closed_at)
      await api.closeIssue(created.id)

      // Verify closed
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.status).toBe('closed')
      expect(getResponse.closed_at).toBeDefined()
    })

    test('transition: in_progress -> blocked (hit blocker)', async ({ api }) => {
      // Create and move to in_progress
      const created = await api.createIssue({
        title: `Blocked Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      await api.updateIssue(created.id, { status: 'in_progress' })

      // Transition to blocked
      await api.updateIssue(created.id, {
        status: 'blocked',
      })

      // Verify
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.status).toBe('blocked')
    })

    test('transition: open -> deferred (defer to later)', async ({ api }) => {
      // Create open issue
      const created = await api.createIssue({
        title: `Deferred Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 4, // Low priority
      })
      createdIssueIds.push(created.id)

      // Transition directly to deferred
      await api.updateIssue(created.id, {
        status: 'deferred',
      })

      // Verify
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.status).toBe('deferred')
    })
  })

  test.describe('Close Operations', () => {
    test('close issue with reason', async ({ api }) => {
      // Create issue
      const created = await api.createIssue({
        title: `Close With Reason ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Close with reason
      await api.closeIssue(created.id, {
        reason: 'Completed successfully after implementation',
      })

      // Verify closed
      const getResponse = await api.getIssue(created.id)
      expect(getResponse.status).toBe('closed')
      expect(getResponse.closed_at).toBeDefined()
    })
  })

  test.describe('Validation Errors', () => {
    test('reject: missing title returns 400', async ({ request }) => {
      // Attempt to create issue without title using raw request
      const response = await request.post('http://localhost:8080/api/issues', {
        data: {
          title: '', // Empty title
          issue_type: 'task',
          priority: 2,
        },
      })

      expect(response.status()).toBe(400)

      const body = await response.json()
      expect(body.success).toBe(false)
      expect(body.error).toContain('title')
    })

    test('reject: invalid status value returns 400', async ({ api, request }) => {
      // Create a valid issue first
      const created = await api.createIssue({
        title: `Invalid Status Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Try to update with invalid status using raw request
      const response = await request.patch(`http://localhost:8080/api/issues/${created.id}`, {
        data: {
          status: 'invalid_status',
        },
      })

      // Backend should reject invalid status
      expect(response.ok()).toBe(false)
      // Could be 400 or 500 depending on validation layer
      expect([400, 500]).toContain(response.status())

      const body = await response.json()
      expect(body.success).toBe(false)
    })
  })
})
