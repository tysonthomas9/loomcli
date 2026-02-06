import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for Dependencies section in IssueDetailPanel.
 *
 * Tests the DependencySection component which displays and manages
 * issue dependencies (Blocked By) and the read-only Blocks section
 * for dependents.
 */

// Mock issues with various dependency configurations
const mockDependencies = [
  {
    id: "dep-1",
    title: "Blocking issue one",
    status: "open",
    priority: 2,
    issue_type: "task",
    dependency_type: "blocks",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "dep-2",
    title: "Blocking issue two (closed)",
    status: "closed",
    priority: 1,
    issue_type: "bug",
    dependency_type: "blocks",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "dep-3",
    title: "Parent epic dependency",
    status: "open",
    priority: 2,
    issue_type: "epic",
    dependency_type: "parent-child",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
]

const mockDependents = [
  {
    id: "dependent-1",
    title: "Child task waiting on this",
    status: "open",
    priority: 2,
    issue_type: "task",
    dependency_type: "blocks",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "dependent-2",
    title: "Another blocked task (closed)",
    status: "closed",
    priority: 3,
    issue_type: "task",
    dependency_type: "blocks",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
]

// Test issue configurations
const mockIssues = {
  withDependencies: {
    id: "test-with-deps",
    title: "Issue with dependencies",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "This issue has blocking dependencies",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  withDependents: {
    id: "test-with-dependents",
    title: "Issue that blocks others",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    description: "This issue blocks other issues",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  withBoth: {
    id: "test-with-both",
    title: "Issue with both deps and dependents",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "This issue has both",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  withNone: {
    id: "test-no-deps",
    title: "Issue without dependencies",
    status: "open",
    priority: 3,
    issue_type: "task",
    description: "This issue has no dependencies",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  withClosedDeps: {
    id: "test-closed-deps",
    title: "Issue with closed dependencies only",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "All dependencies are closed",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
  forAddTest: {
    id: "test-add-dep",
    title: "Issue for add dependency test",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "Used for testing add dependency",
    created_at: "2026-01-27T15:00:00Z",
    updated_at: "2026-01-27T15:00:00Z",
  },
}

// All mock issues as array for /api/ready endpoint
const allMockIssues = [
  mockIssues.withDependencies,
  mockIssues.withDependents,
  mockIssues.withBoth,
  mockIssues.withNone,
  mockIssues.withClosedDeps,
  mockIssues.forAddTest,
  // Include dependency issues so they can be found
  ...mockDependencies.map((d) => ({
    id: d.id,
    title: d.title,
    status: d.status,
    priority: d.priority,
    issue_type: d.issue_type,
    created_at: d.created_at,
    updated_at: d.updated_at,
  })),
]

/**
 * Get issue details based on issue ID.
 * Returns the issue with appropriate dependencies/dependents.
 */
function getMockIssueDetails(issueId: string) {
  switch (issueId) {
    case "test-with-deps":
      return {
        ...mockIssues.withDependencies,
        dependencies: mockDependencies,
        dependents: [],
      }
    case "test-with-dependents":
      return {
        ...mockIssues.withDependents,
        dependencies: [],
        dependents: mockDependents,
      }
    case "test-with-both":
      return {
        ...mockIssues.withBoth,
        dependencies: mockDependencies.slice(0, 2),
        dependents: mockDependents.slice(0, 1),
      }
    case "test-no-deps":
      return {
        ...mockIssues.withNone,
        dependencies: [],
        dependents: [],
      }
    case "test-closed-deps":
      return {
        ...mockIssues.withClosedDeps,
        dependencies: [mockDependencies[1]], // Only the closed one
        dependents: [],
      }
    case "test-add-dep":
      return {
        ...mockIssues.forAddTest,
        dependencies: [],
        dependents: [],
      }
    default:
      // Return basic issue details for dependency issues
      const depIssue = mockDependencies.find((d) => d.id === issueId)
      if (depIssue) {
        return {
          ...depIssue,
          dependencies: [],
          dependents: [],
        }
      }
      return null
  }
}

/**
 * Setup API mocks for testing.
 */
async function setupMocks(
  page: Page,
  options?: {
    getDelay?: number
    getError?: boolean
    addError?: boolean
    removeError?: boolean
  }
) {
  // Track current issue details (mutable for add/remove operations)
  const issueDetailsCache: Record<string, ReturnType<typeof getMockIssueDetails>> = {}

  // Mock /api/ready to return our test issues
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: allMockIssues,
      }),
    })
  })

  // Mock GET/POST/DELETE /api/issues/* endpoints
  await page.route("**/api/issues/**", async (route) => {
    const request = route.request()
    const method = request.method()
    const url = request.url()

    // Extract issue ID from URL
    const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
    const issueId = idMatch ? idMatch[1] : null

    if (method === "GET" && issueId && !url.includes("/dependencies")) {
      // GET /api/issues/{id} - fetch issue details
      if (options?.getDelay) {
        await new Promise((r) => setTimeout(r, options.getDelay))
      }

      if (options?.getError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: "Internal server error" }),
        })
        return
      }

      // Get from cache or initial mock
      const issue = issueDetailsCache[issueId] ?? getMockIssueDetails(issueId)

      if (issue) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(issue),
        })
      } else {
        await route.fulfill({
          status: 404,
          contentType: "application/json",
          body: JSON.stringify({ error: "Not found" }),
        })
      }
    } else if (method === "POST" && url.includes("/dependencies")) {
      // POST /api/issues/{id}/dependencies - add dependency
      if (options?.addError) {
        await route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({ error: "Failed to add dependency" }),
        })
        return
      }

      const body = JSON.parse(await request.postData() || "{}")
      const dependsOnId = body.depends_on_id
      const depType = body.type || "blocks"

      // Simulate successful add
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { issue_id: issueId, depends_on_id: dependsOnId, type: depType },
        }),
      })
    } else if (method === "DELETE" && url.includes("/dependencies")) {
      // DELETE /api/issues/{id}/dependencies/{depId} - remove dependency
      if (options?.removeError) {
        await route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({ error: "Failed to remove dependency" }),
        })
        return
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      })
    } else {
      await route.continue()
    }
  })

  return { issueDetailsCache }
}

/**
 * Navigate to the app and wait for issues to load.
 */
async function navigateToApp(page: Page) {
  await Promise.all([
    page.waitForResponse((res) => res.url().includes("/api/ready")),
    page.goto("/"),
  ])

  // Wait for loading to complete
  await expect(page.getByTestId("loading-container")).not.toBeVisible({
    timeout: 5000,
  })
}

/**
 * Open the detail panel for a specific issue.
 */
async function openPanelForIssue(page: Page, issueTitle: string) {
  const issueCard = page.locator("article").filter({ hasText: issueTitle })
  await expect(issueCard).toBeVisible()
  await issueCard.click()

  // Wait for panel to open
  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toHaveAttribute("data-state", "open")
  await expect(panel).toHaveAttribute("data-loading", "false")
}

/**
 * Get dependency section elements.
 */
function getDependencyElements(page: Page) {
  return {
    dependencySection: page.getByTestId("dependency-section"),
    dependencyList: page.getByTestId("dependency-list"),
    noDependencies: page.getByTestId("no-dependencies"),
    addButton: page.getByTestId("add-dependency-button"),
    addForm: page.getByTestId("add-dependency-form"),
    dependencyInput: page.getByTestId("dependency-input"),
    confirmAdd: page.getByTestId("confirm-add-dependency"),
    cancelAdd: page.getByTestId("cancel-add-dependency"),
    dependencyError: page.getByTestId("dependency-error"),
  }
}

// ============= Suite 1: Display Tests =============

test.describe("Dependencies Section - Display", () => {
  test("Blocked By section renders when dependencies exist", async ({
    page,
  }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const { dependencySection } = getDependencyElements(page)
    await expect(dependencySection).toBeVisible()

    // Verify section title contains "Blocked By"
    await expect(dependencySection.locator("h3")).toContainText("Blocked By")
  })

  test("Blocks section renders when dependents exist", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue that blocks others")

    // The Blocks section is rendered in IssueDetailPanel, not DependencySection
    const blocksSection = page.locator("section").filter({ hasText: /^Blocks \(/ })
    await expect(blocksSection).toBeVisible()
    await expect(blocksSection.locator("h3")).toContainText("Blocks")
  })

  test("Both sections render when issue has both deps and dependents", async ({
    page,
  }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with both deps and dependents")

    const { dependencySection } = getDependencyElements(page)
    await expect(dependencySection).toBeVisible()

    const blocksSection = page.locator("section").filter({ hasText: /^Blocks \(/ })
    await expect(blocksSection).toBeVisible()
  })

  test("Dependency section shows empty state when no dependencies", async ({
    page,
  }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue without dependencies")

    const { dependencySection, noDependencies } = getDependencyElements(page)
    await expect(dependencySection).toBeVisible()
    await expect(noDependencies).toBeVisible()
    await expect(noDependencies).toContainText("No blocking dependencies")
  })
})

// ============= Suite 2: List Tests =============

test.describe("Dependencies Section - List", () => {
  test("Each dependency shows ID", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    // Check first dependency item shows ID
    const depItem = page.getByTestId("dependency-item-dep-1")
    await expect(depItem).toBeVisible()
    await expect(depItem).toContainText("dep-1")
  })

  test("Each dependency shows title", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-1")
    await expect(depItem).toContainText("Blocking issue one")
  })

  test("Each dependency shows type badge", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-1")
    await expect(depItem).toContainText("blocks")
  })

  test("Multiple dependencies render in list", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    // Should have 3 dependency items
    await expect(page.getByTestId("dependency-item-dep-1")).toBeVisible()
    await expect(page.getByTestId("dependency-item-dep-2")).toBeVisible()
    await expect(page.getByTestId("dependency-item-dep-3")).toBeVisible()
  })

  test("Dependency count shown in section header", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const { dependencySection } = getDependencyElements(page)
    // Header should show "Blocked By (3)"
    await expect(dependencySection.locator("h3")).toContainText("Blocked By (3)")
  })
})

// ============= Suite 3: Type Badges Tests =============

test.describe("Dependencies Section - Type Badges", () => {
  test("'blocks' type shows correct badge", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-1")
    await expect(depItem).toContainText("blocks")
  })

  test("'parent-child' type shows correct badge", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-3")
    await expect(depItem).toContainText("parent-child")
  })
})

// ============= Suite 4: Closed Dependency Styling Tests =============

test.describe("Dependencies Section - Closed Styling", () => {
  test("Closed dependency has strikethrough styling", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    // dep-2 is closed
    const closedDepItem = page.getByTestId("dependency-item-dep-2")
    await expect(closedDepItem).toBeVisible()

    // Verify the title element has line-through style
    const titleSpan = closedDepItem.locator("span").filter({ hasText: "Blocking issue two" })
    await expect(titleSpan).toHaveCSS("text-decoration-line", "line-through")
  })

  test("Open dependency has normal styling (no strikethrough)", async ({
    page,
  }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    // dep-1 is open
    const openDepItem = page.getByTestId("dependency-item-dep-1")
    await expect(openDepItem).toBeVisible()

    // Verify the title element does NOT have line-through
    const titleSpan = openDepItem.locator("span").filter({ hasText: "Blocking issue one" })
    await expect(titleSpan).not.toHaveCSS("text-decoration-line", "line-through")
  })
})

// ============= Suite 5: Remove Dependency Tests =============

test.describe("Dependencies Section - Remove", () => {
  test("Remove button visible on dependency item hover", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-1")
    const removeButton = page.getByTestId("remove-dependency-dep-1")

    // Remove button should exist but be hidden initially (opacity: 0)
    await expect(removeButton).toBeAttached()

    // Hover over item to reveal button
    await depItem.hover()
    await expect(removeButton).toBeVisible()
  })

  test("Click remove calls DELETE API", async ({ page }) => {
    let deleteEndpointCalled = false
    let deletedDepId = ""

    await setupMocks(page)

    // Add route listener to track DELETE call
    await page.route("**/api/issues/test-with-deps/dependencies/dep-1", async (route) => {
      if (route.request().method() === "DELETE") {
        deleteEndpointCalled = true
        deletedDepId = "dep-1"
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true }),
        })
      } else {
        await route.continue()
      }
    })

    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-1")
    await depItem.hover()

    const removeButton = page.getByTestId("remove-dependency-dep-1")

    // Set up response promise before clicking
    const responsePromise = page.waitForResponse((res) =>
      res.url().includes("/dependencies/dep-1") &&
      res.request().method() === "DELETE"
    )

    await removeButton.click()
    await responsePromise

    expect(deleteEndpointCalled).toBe(true)
    expect(deletedDepId).toBe("dep-1")
  })

  test("Failed remove shows error message", async ({ page }) => {
    await setupMocks(page, { removeError: true })
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const depItem = page.getByTestId("dependency-item-dep-1")
    await depItem.hover()

    const removeButton = page.getByTestId("remove-dependency-dep-1")
    await removeButton.click()

    // Verify error message appears
    const { dependencyError } = getDependencyElements(page)
    await expect(dependencyError).toBeVisible()
    // API error format includes status code
    await expect(dependencyError).toContainText(/error|Error/i)
  })
})

// ============= Suite 6: Add Dependency Tests =============

test.describe("Dependencies Section - Add", () => {
  test("Add button visible", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton } = getDependencyElements(page)
    await expect(addButton).toBeVisible()
    await expect(addButton).toContainText("Add")
  })

  test("Click add shows input form", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, addForm, dependencyInput } = getDependencyElements(page)

    // Click add button
    await addButton.click()

    // Form should appear
    await expect(addForm).toBeVisible()
    await expect(dependencyInput).toBeVisible()
    await expect(dependencyInput).toBeFocused()
  })

  test("Can cancel add form", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, addForm, cancelAdd } = getDependencyElements(page)

    await addButton.click()
    await expect(addForm).toBeVisible()

    // Click cancel
    await cancelAdd.click()

    // Form should be hidden, add button visible again
    await expect(addForm).not.toBeVisible()
    await expect(addButton).toBeVisible()
  })

  test("Can cancel add form with Escape key", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, addForm, dependencyInput } = getDependencyElements(page)

    await addButton.click()
    await expect(addForm).toBeVisible()

    // Press Escape
    await dependencyInput.press("Escape")

    // Form should be hidden
    await expect(addForm).not.toBeVisible()
  })

  test("Submit button disabled when input empty", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, confirmAdd, dependencyInput } = getDependencyElements(page)

    await addButton.click()

    // Button should be disabled with empty input
    await expect(confirmAdd).toBeDisabled()

    // Type something
    await dependencyInput.fill("dep-1")

    // Button should now be enabled
    await expect(confirmAdd).toBeEnabled()
  })

  test("Submit with Enter key calls POST API", async ({ page }) => {
    let postEndpointCalled = false

    await setupMocks(page)

    // Add route listener to track POST call
    await page.route("**/api/issues/test-add-dep/dependencies", async (route) => {
      if (route.request().method() === "POST") {
        postEndpointCalled = true
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true }),
        })
      } else {
        await route.continue()
      }
    })

    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, dependencyInput } = getDependencyElements(page)

    await addButton.click()
    await dependencyInput.fill("dep-1")

    // Set up response promise before pressing Enter
    const responsePromise = page.waitForResponse((res) =>
      res.url().includes("/api/issues/test-add-dep/dependencies") &&
      res.request().method() === "POST"
    )

    await dependencyInput.press("Enter")
    await responsePromise

    expect(postEndpointCalled).toBe(true)
  })

  test("Self-dependency prevented with error", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, dependencyInput, confirmAdd, dependencyError } =
      getDependencyElements(page)

    await addButton.click()
    // Try to add self as dependency
    await dependencyInput.fill("test-add-dep")
    await confirmAdd.click()

    // Should show error
    await expect(dependencyError).toBeVisible()
    await expect(dependencyError).toContainText("Cannot add self as dependency")
  })

  test("Add duplicate dependency shows error", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue with dependencies")

    const { addButton, dependencyInput, confirmAdd, dependencyError } =
      getDependencyElements(page)

    await addButton.click()
    // Try to add an existing dependency
    await dependencyInput.fill("dep-1")
    await confirmAdd.click()

    // Should show error
    await expect(dependencyError).toBeVisible()
    await expect(dependencyError).toContainText("Already a dependency")
  })

  test("Failed add shows error message", async ({ page }) => {
    await setupMocks(page, { addError: true })
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue for add dependency test")

    const { addButton, dependencyInput, confirmAdd, dependencyError } =
      getDependencyElements(page)

    await addButton.click()
    await dependencyInput.fill("some-new-dep")
    await confirmAdd.click()

    // Should show API error
    await expect(dependencyError).toBeVisible()
    // API error format includes status code
    await expect(dependencyError).toContainText(/error|Error/i)
  })
})

// ============= Suite 7: Empty State Tests =============

test.describe("Dependencies Section - Empty State", () => {
  test("Empty state message shows when no dependencies", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue without dependencies")

    const { noDependencies, dependencyList } = getDependencyElements(page)

    // List should not be visible, empty message should
    await expect(dependencyList).not.toBeVisible()
    await expect(noDependencies).toBeVisible()
    await expect(noDependencies).toContainText("No blocking dependencies")
  })

  test("Add button present in empty state", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue without dependencies")

    const { addButton, noDependencies } = getDependencyElements(page)

    // Both empty message and add button should be visible
    await expect(noDependencies).toBeVisible()
    await expect(addButton).toBeVisible()
  })

  test("Blocks section hidden when no dependents", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue without dependencies")

    // The Blocks section should not be visible
    const blocksSection = page.locator("section").filter({ hasText: /^Blocks \(/ })
    await expect(blocksSection).not.toBeVisible()
  })
})

// ============= Suite 8: Error Handling Tests =============

test.describe("Dependencies Section - Error Handling", () => {
  test("API error on load shows error state", async ({ page }) => {
    await setupMocks(page, { getError: true })
    await navigateToApp(page)

    // Try to open the panel
    const issueCard = page.locator("article").filter({ hasText: "Issue with dependencies" })
    await issueCard.click()

    // Panel should show error
    const panel = page.getByTestId("issue-detail-panel")
    await expect(panel).toHaveAttribute("data-state", "open")
    await expect(panel).toHaveAttribute("data-error", "true")

    const error = page.getByTestId("panel-error")
    await expect(error).toBeVisible()
  })
})

// ============= Suite 9: Dependents (Blocks) Section Tests =============

test.describe("Dependencies Section - Blocks (Dependents)", () => {
  test("Blocks section shows correct count", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue that blocks others")

    const blocksSection = page.locator("section").filter({ hasText: /^Blocks \(/ })
    await expect(blocksSection.locator("h3")).toContainText("Blocks (2)")
  })

  test("Blocks section shows dependent ID and title", async ({ page }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue that blocks others")

    const blocksSection = page.locator("section").filter({ hasText: /^Blocks \(/ })
    await expect(blocksSection).toContainText("dependent-1")
    await expect(blocksSection).toContainText("Child task waiting on this")
  })

  test("Blocks section shows closed dependents with strikethrough", async ({
    page,
  }) => {
    await setupMocks(page)
    await navigateToApp(page)
    await openPanelForIssue(page, "Issue that blocks others")

    const blocksSection = page.locator("section").filter({ hasText: /^Blocks \(/ })
    const closedDependent = blocksSection.locator("li").filter({ hasText: "Another blocked task" })

    await expect(closedDependent).toBeVisible()
    // The title should have line-through
    const titleSpan = closedDependent.locator("span").filter({ hasText: "Another blocked task" })
    await expect(titleSpan).toHaveCSS("text-decoration-line", "line-through")
  })
})
