/**
 * Review Workflow API E2E Tests
 *
 * Story 4: As a reviewer, I want to approve or reject submitted work.
 * Tests approve/reject operations used by IssueDetailPanel buttons.
 *
 * Review Conventions:
 * - Plan review: Title prefixed with '[Need Review]'
 * - Code review: Status set to 'review'
 * - Approve plan: Remove prefix, set status to 'open' (ready)
 * - Approve code: Set status to 'closed' (done)
 * - Reject: Add comment with feedback, set status appropriately
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests modify shared state
test.describe.configure({ mode: 'serial' })

test.describe('Review Workflow', () => {
  // Track created issues for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up all created issues
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Review Status', () => {
    test('issue in review status appears correctly', async ({ api }) => {
      // Create an issue and set status to review
      const created = await api.createIssue({
        title: `Code Review Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Set status to review
      await api.updateIssue(created.id, { status: 'review' })

      // Verify status via GET
      const details = await api.getIssue(created.id)
      expect(details.status).toBe('review')

      // Verify issue appears in review-filtered list
      const reviewList = await api.listIssues({ status: 'review' })
      const found = reviewList.some((i) => i.id === created.id)
      expect(found).toBe(true)
    })
  })

  test.describe('Plan Review (Title-Based)', () => {
    test('add [Need Review] prefix for plan review', async ({ api }) => {
      // Create an issue
      const originalTitle = `Plan Implementation ${generateTestId()}`
      const created = await api.createIssue({
        title: originalTitle,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Add [Need Review] prefix
      const prefixedTitle = `[Need Review] ${originalTitle}`
      await api.updateIssue(created.id, { title: prefixedTitle })

      // Verify title has prefix
      const details = await api.getIssue(created.id)
      expect(details.title).toBe(prefixedTitle)
      expect(details.title).toContain('[Need Review]')
    })

    test('approve plan: removes [Need Review], sets status to open', async ({ api }) => {
      // Create issue with [Need Review] prefix
      const baseName = `Plan Approve Test ${generateTestId()}`
      const prefixedTitle = `[Need Review] ${baseName}`
      const created = await api.createIssue({
        title: prefixedTitle,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Simulate approve: remove prefix, set status to open
      await api.updateIssue(created.id, {
        title: baseName,
        status: 'open',
      })

      // Verify the changes
      const details = await api.getIssue(created.id)
      expect(details.title).toBe(baseName)
      expect(details.title).not.toContain('[Need Review]')
      expect(details.status).toBe('open')
    })
  })

  test.describe('Code Review (Status-Based)', () => {
    test('approve code: review → closed', async ({ api }) => {
      // Create issue in review status
      const created = await api.createIssue({
        title: `Code Approve Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Move to review status
      await api.updateIssue(created.id, { status: 'review' })

      // Verify in review
      let details = await api.getIssue(created.id)
      expect(details.status).toBe('review')

      // Approve: close the issue
      await api.closeIssue(created.id)

      // Verify closed
      details = await api.getIssue(created.id)
      expect(details.status).toBe('closed')
    })
  })

  test.describe('Rejection', () => {
    test('reject: adds comment with feedback', async ({ api }) => {
      // Create issue in review
      const created = await api.createIssue({
        title: `Reject Comment Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      await api.updateIssue(created.id, { status: 'review' })

      // Add rejection comment
      const feedbackText = 'Please address the edge case handling in the validation logic.'
      const comment = await api.addComment(created.id, { text: feedbackText })

      expect(comment.text).toBe(feedbackText)
      expect(comment.id).toBeDefined()

      // Verify comment appears in issue details
      const details = await api.getIssue(created.id)
      expect(details.comments).toBeDefined()
      expect(Array.isArray(details.comments)).toBe(true)
      const hasComment = details.comments.some((c) => c.text === feedbackText)
      expect(hasComment).toBe(true)
    })

    test('reject: moves issue back to in_progress', async ({ api }) => {
      // Create issue in review status
      const created = await api.createIssue({
        title: `Reject Status Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(created.id)

      // Move to review
      await api.updateIssue(created.id, { status: 'review' })

      // Simulate rejection: add comment and move to in_progress
      await api.addComment(created.id, { text: 'Needs more work on error handling.' })
      await api.updateIssue(created.id, { status: 'in_progress' })

      // Verify status is in_progress
      const details = await api.getIssue(created.id)
      expect(details.status).toBe('in_progress')
    })
  })

  test.describe('Filtering', () => {
    test('filter issues by review status', async ({ api }) => {
      // Create several issues with different statuses
      const reviewIssue = await api.createIssue({
        title: `Filter Review Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(reviewIssue.id)

      const openIssue = await api.createIssue({
        title: `Filter Open Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(openIssue.id)

      // Set one to review status
      await api.updateIssue(reviewIssue.id, { status: 'review' })
      // openIssue stays in 'open' status (default)

      // Filter by status=review
      const reviewList = await api.listIssues({ status: 'review' })

      // Review issue should be in list
      const hasReviewIssue = reviewList.some((i) => i.id === reviewIssue.id)
      expect(hasReviewIssue).toBe(true)

      // Open issue should NOT be in review list
      const hasOpenIssue = reviewList.some((i) => i.id === openIssue.id)
      expect(hasOpenIssue).toBe(false)
    })
  })
})
