/**
 * Team Collaboration API E2E Tests
 *
 * Story 5: As a team member, I want to collaborate via comments and notes.
 * Tests comments, notes, design fields, and assignee management used by IssueDetailPanel.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests create/modify shared state
test.describe.configure({ mode: 'serial' })

test.describe('Team Collaboration', () => {
  // Track created issues for cleanup
  const createdIssueIds: string[] = []

  test.afterEach(async ({ api }) => {
    // Clean up all created issues
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
    createdIssueIds.length = 0
  })

  test.describe('Comments', () => {
    test('add comment to issue (POST /api/issues/:id/comments)', async ({ api }) => {
      // Create an issue
      const issue = await api.createIssue({
        title: `Comment Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Add a comment
      const commentText = 'This is a test comment for collaboration.'
      const comment = await api.addComment(issue.id, { text: commentText })

      // Verify comment response
      expect(comment.id).toBeDefined()
      expect(comment.text).toBe(commentText)
      expect(comment.issue_id).toBe(issue.id)
      expect(comment.created_at).toBeDefined()

      // Verify comment appears in issue details
      const details = await api.getIssue(issue.id)
      expect(details.comments).toBeDefined()
      expect(Array.isArray(details.comments)).toBe(true)
      expect(details.comments.length).toBeGreaterThanOrEqual(1)

      const foundComment = details.comments.find(c => c.text === commentText)
      expect(foundComment).toBeDefined()
    })

    test('comments appear in chronological order', async ({ api }) => {
      // Create an issue
      const issue = await api.createIssue({
        title: `Comment Order Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Add multiple comments with small delays to ensure distinct timestamps
      await api.addComment(issue.id, { text: 'First comment' })
      await new Promise(resolve => setTimeout(resolve, 50)) // Small delay
      await api.addComment(issue.id, { text: 'Second comment' })
      await new Promise(resolve => setTimeout(resolve, 50))
      await api.addComment(issue.id, { text: 'Third comment' })

      // Get issue details
      const details = await api.getIssue(issue.id)
      expect(details.comments.length).toBe(3)

      // Verify chronological order (oldest first)
      expect(details.comments[0].text).toBe('First comment')
      expect(details.comments[1].text).toBe('Second comment')
      expect(details.comments[2].text).toBe('Third comment')

      // Also verify by created_at timestamps
      const timestamps = details.comments.map(c => new Date(c.created_at).getTime())
      expect(timestamps[0]).toBeLessThanOrEqual(timestamps[1])
      expect(timestamps[1]).toBeLessThanOrEqual(timestamps[2])
    })
  })

  test.describe('Notes Field', () => {
    test('update notes field via PATCH', async ({ api }) => {
      // Create an issue without notes
      const issue = await api.createIssue({
        title: `Notes Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify no notes initially
      let details = await api.getIssue(issue.id)
      expect(details.notes ?? '').toBe('')

      // Update notes via PATCH
      const notesContent = 'Quick note: Remember to check edge cases.'
      await api.updateIssue(issue.id, { notes: notesContent })

      // Verify notes field is set
      details = await api.getIssue(issue.id)
      expect(details.notes).toBe(notesContent)

      // Update notes again (overwrite)
      const updatedNotes = 'Updated note with more details.'
      await api.updateIssue(issue.id, { notes: updatedNotes })

      details = await api.getIssue(issue.id)
      expect(details.notes).toBe(updatedNotes)
    })
  })

  test.describe('Design Field', () => {
    test('update design field (markdown) via PATCH', async ({ api }) => {
      // Create an issue without design
      const issue = await api.createIssue({
        title: `Design Test ${generateTestId()}`,
        issue_type: 'feature',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify no design initially
      let details = await api.getIssue(issue.id)
      expect(details.design ?? '').toBe('')

      // Update design with markdown content
      const designContent = `## Technical Approach

### Architecture
- Use React hooks for state management
- Implement lazy loading for performance

### Code Example
\`\`\`typescript
function Component() {
  return <div>Hello</div>
}
\`\`\`

### Edge Cases
1. Handle empty state
2. Handle error state
`
      await api.updateIssue(issue.id, { design: designContent })

      // Verify design field preserves markdown exactly
      details = await api.getIssue(issue.id)
      expect(details.design).toBe(designContent)
    })
  })

  test.describe('Assignee Management', () => {
    test('assign issue to team member', async ({ api }) => {
      // Create an unassigned issue
      const issue = await api.createIssue({
        title: `Assign Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
      })
      createdIssueIds.push(issue.id)

      // Verify not assigned initially
      let details = await api.getIssue(issue.id)
      expect(details.assignee ?? '').toBe('')

      // Assign to a team member
      await api.updateIssue(issue.id, { assignee: 'developer1' })

      // Verify assignment
      details = await api.getIssue(issue.id)
      expect(details.assignee).toBe('developer1')
    })

    test('reassign issue to different member', async ({ api }) => {
      // Create issue assigned to developer1
      const issue = await api.createIssue({
        title: `Reassign Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
        assignee: 'developer1',
      })
      createdIssueIds.push(issue.id)

      // Verify initial assignment
      let details = await api.getIssue(issue.id)
      expect(details.assignee).toBe('developer1')

      // Reassign to developer2
      await api.updateIssue(issue.id, { assignee: 'developer2' })

      // Verify reassignment
      details = await api.getIssue(issue.id)
      expect(details.assignee).toBe('developer2')
    })

    test('unassign issue (set assignee to null)', async ({ api }) => {
      // Create issue with assignee
      const issue = await api.createIssue({
        title: `Unassign Test ${generateTestId()}`,
        issue_type: 'task',
        priority: 2,
        assignee: 'developer1',
      })
      createdIssueIds.push(issue.id)

      // Verify initially assigned
      let details = await api.getIssue(issue.id)
      expect(details.assignee).toBe('developer1')

      // Unassign by setting assignee to empty string
      await api.updateIssue(issue.id, { assignee: '' })

      // Verify unassigned
      details = await api.getIssue(issue.id)
      expect(details.assignee ?? '').toBe('')
    })
  })
})
