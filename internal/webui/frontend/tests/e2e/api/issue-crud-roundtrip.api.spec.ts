/**
 * Issue CRUD Full Cycle with API Read-Back
 *
 * Walks through a single coherent journey: create with all fields -> read-back ->
 * multi-field update -> read-back -> add comments -> read-back -> label manipulation ->
 * read-back -> status transitions -> close -> read-back -> delete -> verify 404.
 *
 * This catches data-loss bugs, field-truncation issues, and mutation side-effects
 * that isolated tests miss.
 */

import { test, expect, isIntegrationEnabled, generateTestId } from './api-client'

// Skip if integration tests not enabled
test.skip(!isIntegrationEnabled, 'API E2E tests require RUN_INTEGRATION_TESTS=1')

// Serial mode: tests share a single issue through its lifecycle
test.describe.configure({ mode: 'serial' })

test.describe('Issue CRUD Full Cycle with API Read-Back', () => {
  const testId = generateTestId()
  const createdIssueIds: string[] = []

  // Shared state across serial tests
  let issueId: string
  let initialUpdatedAt: string
  let savedWsPrefix: string

  // Field values for create
  const createFields = {
    title: `CRUD Roundtrip ${testId}`,
    issue_type: 'feature' as const,
    priority: 1 as const,
    description: `Description for ${testId}: comprehensive CRUD test`,
    design: `## Design\n\nTechnical approach for ${testId}`,
    acceptance_criteria: `- All fields round-trip correctly\n- Delete returns 404`,
    notes: `Implementation notes for ${testId}`,
    assignee: `assignee-${testId}`,
    owner: `owner-${testId}`,
    labels: ['backend', 'api'],
    due_at: '2026-12-31T23:59:59Z',
    defer_until: '2026-06-15T00:00:00Z',
    external_ref: `ext-ref-${testId}`,
  }

  test.afterAll(async ({ api }) => {
    for (const id of createdIssueIds) {
      await api.cleanupIssue(id)
    }
  })

  test('1: create issue with all fields and read back', async ({ api }) => {
    // Create with all optional fields populated
    const created = await api.createIssue(createFields)
    issueId = created.id
    createdIssueIds.push(issueId)

    // Read back via GET
    const issue = await api.getIssue(issueId)

    // Verify ID format and default status
    expect(issue.id).toMatch(/^[a-z]+-[a-z0-9]+$/)
    expect(issue.status).toBe('open')

    // Verify all fields round-trip
    expect(issue.title).toBe(createFields.title)
    expect(issue.issue_type).toBe(createFields.issue_type)
    expect(issue.priority).toBe(createFields.priority)
    expect(issue.description).toBe(createFields.description)
    expect(issue.design).toBe(createFields.design)
    expect(issue.acceptance_criteria).toBe(createFields.acceptance_criteria)
    expect(issue.notes).toBe(createFields.notes)
    expect(issue.assignee).toBe(createFields.assignee)
    expect(issue.owner).toBe(createFields.owner)
    expect(issue.labels).toHaveLength(createFields.labels.length)
    expect(issue.labels).toEqual(expect.arrayContaining(createFields.labels))
    expect(issue.due_at).toBe(createFields.due_at)
    expect(issue.defer_until).toBe(createFields.defer_until)
    expect(issue.external_ref).toBe(createFields.external_ref)

    // Verify timestamps
    expect(issue.created_at).toBeDefined()
    expect(issue.updated_at).toBeDefined()
    expect(new Date(issue.created_at).getTime()).not.toBeNaN()
    expect(new Date(issue.updated_at).getTime()).not.toBeNaN()

    initialUpdatedAt = issue.updated_at
    savedWsPrefix = api.wsPrefix
  })

  test('2: update multiple fields via PATCH and read back', async ({ api }) => {
    const newTitle = `Updated CRUD Roundtrip ${testId}`
    const newDescription = `Updated description for ${testId}`
    const newAssignee = `new-assignee-${testId}`

    await api.updateIssue(issueId, {
      title: newTitle,
      description: newDescription,
      priority: 0,
      assignee: newAssignee,
    })

    const issue = await api.getIssue(issueId)

    // Verify patched fields
    expect(issue.title).toBe(newTitle)
    expect(issue.description).toBe(newDescription)
    expect(issue.priority).toBe(0)
    expect(issue.assignee).toBe(newAssignee)

    // Verify non-patched fields are UNCHANGED
    expect(issue.design).toBe(createFields.design)
    expect(issue.acceptance_criteria).toBe(createFields.acceptance_criteria)
    expect(issue.notes).toBe(createFields.notes)
    expect(issue.owner).toBe(createFields.owner)
    expect(issue.labels).toHaveLength(createFields.labels.length)
    expect(issue.labels).toEqual(expect.arrayContaining(createFields.labels))
    expect(issue.due_at).toBe(createFields.due_at)
    expect(issue.defer_until).toBe(createFields.defer_until)
    expect(issue.external_ref).toBe(createFields.external_ref)

    // updated_at should advance
    expect(issue.updated_at > initialUpdatedAt).toBe(true)
    initialUpdatedAt = issue.updated_at
  })

  test('3: add comments and verify via read-back', async ({ api }) => {
    await api.addComment(issueId, { text: 'First review comment' })
    await api.addComment(issueId, { text: 'Follow-up comment' })

    const issue = await api.getIssue(issueId)

    expect(issue.comments).toHaveLength(2)
    expect(issue.comments[0].text).toBe('First review comment')
    expect(issue.comments[1].text).toBe('Follow-up comment')

    // Verify comment structure
    for (const comment of issue.comments) {
      expect(comment.id).toBeDefined()
      expect(comment.issue_id).toBe(issueId)
      expect(comment.author).toBeDefined()
      expect(typeof comment.author).toBe('string')
      expect(comment.created_at).toBeDefined()
      expect(typeof comment.created_at).toBe('string')
    }
  })

  test('4: label manipulation and read-back', async ({ api }) => {
    // Add labels
    await api.updateIssue(issueId, { add_labels: ['urgent', 'regression'] })
    let issue = await api.getIssue(issueId)
    expect(issue.labels).toEqual(expect.arrayContaining(['backend', 'api', 'urgent', 'regression']))
    expect(issue.labels).toHaveLength(4)

    // Remove a label
    await api.updateIssue(issueId, { remove_labels: ['api'] })
    issue = await api.getIssue(issueId)
    expect(issue.labels).toEqual(expect.arrayContaining(['backend', 'urgent', 'regression']))
    expect(issue.labels).not.toContain('api')
    expect(issue.labels).toHaveLength(3)

    // Set labels (replaces all)
    await api.updateIssue(issueId, { set_labels: ['final-label'] })
    issue = await api.getIssue(issueId)
    expect(issue.labels).toEqual(['final-label'])
  })

  test('5: status transition lifecycle and read-back', async ({ api }) => {
    // open -> in_progress
    await api.updateIssue(issueId, { status: 'in_progress' })
    let issue = await api.getIssue(issueId)
    expect(issue.status).toBe('in_progress')

    // in_progress -> review
    await api.updateIssue(issueId, { status: 'review' })
    issue = await api.getIssue(issueId)
    expect(issue.status).toBe('review')

    // review -> in_progress (regression: re-open from review)
    await api.updateIssue(issueId, { status: 'in_progress' })
    issue = await api.getIssue(issueId)
    expect(issue.status).toBe('in_progress')
  })

  test('6: close issue with reason and read-back', async ({ api }) => {
    await api.closeIssue(issueId, { reason: 'Implementation verified' })

    const issue = await api.getIssue(issueId)
    expect(issue.status).toBe('closed')
    expect(issue.closed_at).toBeDefined()
    expect(new Date(issue.closed_at!).getTime()).not.toBeNaN()
  })

  test('7: list issues includes the closed issue', async ({ api }) => {
    const issues = await api.listIssues({ status: 'closed' })

    expect(issues.some(i => i.id === issueId)).toBe(true)

    const our = issues.find(i => i.id === issueId)!
    // Title and priority should match the values set in Test 2, not the original create
    expect(our.title).toBe(`Updated CRUD Roundtrip ${testId}`)
    expect(our.priority).toBe(0)
  })

  test('8: delete issue and verify tombstone', async ({ api, request }) => {
    // Delete the issue using the api client
    await api.deleteIssue(issueId)

    // The backend uses tombstone deletion — GET returns the issue with status 'tombstone'.
    // Use raw request with the wsPrefix captured in Test 1 to avoid fixture re-resolution races.
    const response = await request.get(`${savedWsPrefix}/issues/${issueId}`)
    if (response.status() === 404) {
      // Some backends truly remove the issue — 404 is acceptable
      expect(response.ok()).toBe(false)
    } else {
      // Tombstone: issue still exists but status is 'tombstone'
      expect(response.ok()).toBe(true)
      const body = await response.json()
      expect(body.success).toBe(true)
      expect(body.data.status).toBe('tombstone')
    }

    // Verify absent from normal list (tombstoned issues excluded from default list)
    const issues = await api.listIssues()
    expect(issues.every(i => i.id !== issueId)).toBe(true)
  })
})
