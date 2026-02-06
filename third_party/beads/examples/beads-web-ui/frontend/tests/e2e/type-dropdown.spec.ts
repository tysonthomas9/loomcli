import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for TypeDropdown component.
 *
 * These tests use a test fixture route (/test/issue-detail-panel) that renders
 * the IssueDetailPanel directly with URL-specified issue parameters.
 */

/**
 * Test issue data with different types.
 */
interface TestIssue {
  id: string
  title: string
  status: string
  priority: number
  issue_type: string
  description?: string
}

const testIssues: TestIssue[] = [
  {
    id: "type-test-bug",
    title: "Test Issue Bug",
    status: "open",
    priority: 2,
    issue_type: "bug",
    description: "Bug description",
  },
  {
    id: "type-test-feature",
    title: "Test Issue Feature",
    status: "in_progress",
    priority: 1,
    issue_type: "feature",
    description: "Feature description",
  },
  {
    id: "type-test-task",
    title: "Test Issue Task",
    status: "open",
    priority: 2,
    issue_type: "task",
    description: "Task description",
  },
  {
    id: "type-test-epic",
    title: "Test Issue Epic",
    status: "open",
    priority: 0,
    issue_type: "epic",
    description: "Epic description",
  },
  {
    id: "type-test-chore",
    title: "Test Issue Chore",
    status: "open",
    priority: 3,
    issue_type: "chore",
    description: "Chore description",
  },
]

/**
 * Build the test fixture URL for an issue.
 */
function buildTestUrl(issue: TestIssue): string {
  const params = new URLSearchParams({
    id: issue.id,
    title: issue.title,
    status: issue.status,
    priority: String(issue.priority),
    issue_type: issue.issue_type,
  })
  if (issue.description) params.set("description", issue.description)
  return `/test/issue-detail-panel?${params.toString()}`
}

/**
 * Setup API mocks for testing.
 */
async function setupMocks(
  page: Page,
  options?: {
    patchDelay?: number
    patchError?: boolean
    onPatch?: (body: { issue_type?: string }) => void
  }
) {
  // Track patch calls
  const patchCalls: { url: string; body: { issue_type?: string } }[] = []

  // Mock GET and PATCH /api/issues/{id}
  await page.route("**/api/issues/*", async (route) => {
    const request = route.request()
    const url = request.url()
    const method = request.method()

    if (method === "GET") {
      // GET /api/issues/{id} returns IssueDetails directly without wrapper
      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const issue = testIssues.find((i) => i.id === id)

      if (issue) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            ...issue,
            created_at: "2026-01-27T10:00:00Z",
            updated_at: "2026-01-27T10:00:00Z",
            dependencies: [],
            dependents: [],
          }),
        })
      } else {
        await route.fulfill({
          status: 404,
          contentType: "application/json",
          body: JSON.stringify({ error: "Not found" }),
        })
      }
    } else if (method === "PATCH") {
      if (options?.patchDelay) {
        await new Promise((r) => setTimeout(r, options.patchDelay))
      }

      const body = request.postDataJSON() as { issue_type?: string }
      patchCalls.push({ url, body })
      options?.onPatch?.(body)

      if (options?.patchError) {
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Server error" }),
        })
        return
      }

      const idMatch = url.match(/\/api\/issues\/([^/?]+)/)
      const id = idMatch ? idMatch[1] : null
      const issue = testIssues.find((i) => i.id === id)

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...issue,
            ...body,
            updated_at: new Date().toISOString(),
          },
        }),
      })
    } else {
      await route.continue()
    }
  })

  return { patchCalls }
}

/**
 * Navigate to the test fixture and wait for the panel to be visible.
 */
async function openTestPanel(page: Page, issueIndex: number = 0) {
  const issue = testIssues[issueIndex]
  const url = buildTestUrl(issue)

  await page.goto(url)

  // Wait for panel to be visible
  const panel = page.getByTestId("issue-detail-panel")
  await expect(panel).toBeVisible({ timeout: 5000 })

  return panel
}

/**
 * Get TypeDropdown elements from the page.
 */
function getTypeDropdown(page: Page) {
  return {
    trigger: page.getByTestId("type-dropdown-trigger"),
    menu: page.getByTestId("type-dropdown-menu"),
    getOption: (type: string) => page.getByTestId(`type-option-${type}`),
    savingIndicator: page.getByTestId("type-saving"),
    errorMessage: page.getByTestId("type-error"),
  }
}

test.describe("TypeDropdown", () => {
  test.describe("Display", () => {
    test("shows current type with icon", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toBeVisible()

      // Verify trigger shows "Bug" text
      await expect(trigger).toContainText("Bug")

      // Verify trigger has data-type attribute for styling
      await expect(trigger).toHaveAttribute("data-type", "bug")
    })

    test("shows dropdown arrow indicator", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger } = getTypeDropdown(page)

      // Verify arrow/chevron icon is present (▾)
      await expect(trigger).toContainText("▾")
    })

    test("displays correct type for bug", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0) // bug

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toContainText("Bug")
      await expect(trigger).toHaveAttribute("data-type", "bug")
    })

    test("displays correct type for feature", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 1) // feature

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toContainText("Feature")
      await expect(trigger).toHaveAttribute("data-type", "feature")
    })

    test("displays correct type for task", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 2) // task

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toContainText("Task")
      await expect(trigger).toHaveAttribute("data-type", "task")
    })

    test("displays correct type for epic", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 3) // epic

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toContainText("Epic")
      await expect(trigger).toHaveAttribute("data-type", "epic")
    })

    test("displays correct type for chore", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 4) // chore

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toContainText("Chore")
      await expect(trigger).toHaveAttribute("data-type", "chore")
    })
  })

  test.describe("Open/Close", () => {
    test("click opens dropdown menu", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)

      // Initially menu is not visible
      await expect(menu).not.toBeVisible()

      // Click trigger to open
      await trigger.click()

      // Menu should appear with role="listbox"
      await expect(menu).toBeVisible()
      await expect(menu).toHaveAttribute("role", "listbox")
    })

    test("Escape key closes dropdown", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)

      // Open dropdown
      await trigger.click()
      await expect(menu).toBeVisible()

      // Press Escape
      await page.keyboard.press("Escape")

      // Menu should be hidden
      await expect(menu).not.toBeVisible()
    })

    test("outside click closes dropdown", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)

      // Open dropdown
      await trigger.click()
      await expect(menu).toBeVisible()

      // Click outside (on the panel title area)
      await page.getByTestId("issue-detail-panel").click({ position: { x: 10, y: 10 } })

      // Menu should be hidden
      await expect(menu).not.toBeVisible()
    })

    test("selecting option closes dropdown", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu, getOption } = getTypeDropdown(page)

      // Open dropdown
      await trigger.click()
      await expect(menu).toBeVisible()

      // Click an option
      await getOption("feature").click()

      // Menu should close
      await expect(menu).not.toBeVisible()
    })
  })

  test.describe("All Options", () => {
    test("shows all 5 type options", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, getOption } = getTypeDropdown(page)

      // Open dropdown
      await trigger.click()

      // Verify all 5 options are visible
      await expect(getOption("bug")).toBeVisible()
      await expect(getOption("feature")).toBeVisible()
      await expect(getOption("task")).toBeVisible()
      await expect(getOption("epic")).toBeVisible()
      await expect(getOption("chore")).toBeVisible()
    })

    test("each option has role option", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, getOption } = getTypeDropdown(page)
      await trigger.click()

      // Verify each option has role="option"
      for (const type of ["bug", "feature", "task", "epic", "chore"]) {
        await expect(getOption(type)).toHaveAttribute("role", "option")
      }
    })

    test("each option displays correct icon via data-type", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, getOption } = getTypeDropdown(page)
      await trigger.click()

      // Each option should have data-type for styling the icon
      await expect(getOption("bug")).toHaveAttribute("data-type", "bug")
      await expect(getOption("feature")).toHaveAttribute("data-type", "feature")
      await expect(getOption("task")).toHaveAttribute("data-type", "task")
      await expect(getOption("epic")).toHaveAttribute("data-type", "epic")
      await expect(getOption("chore")).toHaveAttribute("data-type", "chore")
    })

    test("current type is highlighted with aria-selected", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)
      await trigger.click()

      // Bug option should be selected
      await expect(getOption("bug")).toHaveAttribute("aria-selected", "true")

      // Others should not be selected
      await expect(getOption("feature")).toHaveAttribute("aria-selected", "false")
      await expect(getOption("task")).toHaveAttribute("aria-selected", "false")
    })

    test("current type has data-selected attribute", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)
      await trigger.click()

      // Bug option should have data-selected
      await expect(getOption("bug")).toHaveAttribute("data-selected", "true")
    })
  })

  test.describe("Selection", () => {
    test("changing type calls API with correct parameters", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)

      // Start waiting for the response BEFORE triggering the action
      const responsePromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/type-test-bug") &&
          res.request().method() === "PATCH"
      )

      // Change from bug to feature
      await trigger.click()
      await getOption("feature").click()

      // Wait for PATCH call
      await responsePromise

      // Verify API call was made with correct parameters
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].url).toContain("/api/issues/type-test-bug")
      expect(patchCalls[0].body).toEqual({ issue_type: "feature" })
    })

    test("UI updates immediately on selection (optimistic)", async ({ page }) => {
      await setupMocks(page, { patchDelay: 500 })
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)

      // Change from bug to feature
      await trigger.click()
      await getOption("feature").click()

      // Verify trigger shows new type BEFORE API completes
      await expect(trigger).toContainText("Feature")
      await expect(trigger).toHaveAttribute("data-type", "feature")
    })

    test("same type selection does not call API", async ({ page }) => {
      let patchCallCount = 0

      await setupMocks(page, {
        onPatch: () => {
          patchCallCount++
        },
      })
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)

      // Select bug again (same type)
      await trigger.click()
      await getOption("bug").click()

      // Give time for any potential API call
      await page.waitForTimeout(200)

      // No PATCH call should be made
      expect(patchCallCount).toBe(0)
    })
  })

  test.describe("Saving State", () => {
    test("shows saving indicator during API call", async ({ page }) => {
      await setupMocks(page, { patchDelay: 500 })
      await openTestPanel(page, 0)

      const { trigger, getOption, savingIndicator } = getTypeDropdown(page)

      // Start the type change
      const patchPromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/type-test-bug") &&
          res.request().method() === "PATCH"
      )
      await trigger.click()
      await getOption("feature").click()

      // Saving indicator should appear
      await expect(savingIndicator).toBeVisible()

      // Trigger should have data-saving attribute
      await expect(trigger).toHaveAttribute("data-saving", "true")

      // Wait for save to complete
      await patchPromise

      // Saving indicator should be hidden after completion
      await expect(savingIndicator).not.toBeVisible()
    })

    test("dropdown is disabled during save", async ({ page }) => {
      await setupMocks(page, { patchDelay: 500 })
      await openTestPanel(page, 0)

      const { trigger, getOption, menu } = getTypeDropdown(page)

      // Start the type change
      await trigger.click()
      await getOption("feature").click()

      // Trigger should be disabled during save
      await expect(trigger).toBeDisabled()

      // Try to click trigger during save - menu should not open
      await trigger.click({ force: true })
      await expect(menu).not.toBeVisible()
    })
  })

  test.describe("Error Handling", () => {
    test("reverts to original type on API failure", async ({ page }) => {
      await setupMocks(page, { patchError: true })
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)

      // Start waiting for the response BEFORE triggering the action
      const responsePromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/type-test-bug") &&
          res.request().method() === "PATCH" &&
          res.status() === 500
      )

      // Change from bug to feature (will fail)
      await trigger.click()
      await getOption("feature").click()

      // Wait for the failed PATCH
      await responsePromise

      // Verify trigger reverts to "Bug"
      await expect(trigger).toContainText("Bug")
      await expect(trigger).toHaveAttribute("data-type", "bug")
    })

    test("displays error message on failure", async ({ page }) => {
      await setupMocks(page, { patchError: true })
      await openTestPanel(page, 0)

      const { trigger, getOption, errorMessage } = getTypeDropdown(page)

      // Start waiting for the response BEFORE triggering the action
      const responsePromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/type-test-bug") &&
          res.request().method() === "PATCH" &&
          res.status() === 500
      )

      // Change type (will fail)
      await trigger.click()
      await getOption("feature").click()

      // Wait for the failed PATCH
      await responsePromise

      // Error message should appear
      await expect(errorMessage).toBeVisible()
    })

    test("dropdown is re-enabled after error", async ({ page }) => {
      await setupMocks(page, { patchError: true })
      await openTestPanel(page, 0)

      const { trigger, getOption, menu } = getTypeDropdown(page)

      // Start waiting for the response BEFORE triggering the action
      const responsePromise = page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/type-test-bug") &&
          res.request().method() === "PATCH" &&
          res.status() === 500
      )

      // Change type (fails)
      await trigger.click()
      await getOption("feature").click()

      // Wait for the failed PATCH
      await responsePromise

      // Trigger should be clickable again
      await expect(trigger).toBeEnabled()

      // Can open dropdown for retry
      await trigger.click()
      await expect(menu).toBeVisible()
    })
  })

  test.describe("Accessibility", () => {
    test("trigger has aria-haspopup attribute", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger } = getTypeDropdown(page)
      await expect(trigger).toHaveAttribute("aria-haspopup", "listbox")
    })

    test("trigger has aria-expanded state", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)

      // Closed state
      await expect(trigger).toHaveAttribute("aria-expanded", "false")

      // Open the dropdown
      await trigger.click()
      await expect(menu).toBeVisible()

      // Open state
      await expect(trigger).toHaveAttribute("aria-expanded", "true")
    })

    test("menu has role listbox", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)
      await trigger.click()

      await expect(menu).toHaveAttribute("role", "listbox")
    })

    test("options have role option with aria-selected", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)
      await trigger.click()

      // Verify each option has role="option"
      for (const type of ["bug", "feature", "task", "epic", "chore"]) {
        await expect(getOption(type)).toHaveAttribute("role", "option")
      }

      // Verify selected option has aria-selected="true"
      await expect(getOption("bug")).toHaveAttribute("aria-selected", "true")
    })

    test("trigger has descriptive aria-label", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger } = getTypeDropdown(page)
      const ariaLabel = await trigger.getAttribute("aria-label")

      // Should contain "Type" and current type
      expect(ariaLabel).toContain("Type")
      expect(ariaLabel).toContain("Bug")
    })
  })

  test.describe("Keyboard Navigation", () => {
    test("Enter opens dropdown", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)

      // Focus trigger
      await trigger.focus()

      // Press Enter
      await page.keyboard.press("Enter")

      // Menu should open
      await expect(menu).toBeVisible()
    })

    test("Space opens dropdown", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0)

      const { trigger, menu } = getTypeDropdown(page)

      // Focus trigger
      await trigger.focus()

      // Press Space
      await page.keyboard.press("Space")

      // Menu should open
      await expect(menu).toBeVisible()
    })

    test("Arrow keys navigate options", async ({ page }) => {
      await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger, getOption } = getTypeDropdown(page)

      // Open dropdown
      await trigger.click()

      // Initial focus should be on current type (bug is first in TYPE_OPTIONS)
      await expect(getOption("bug")).toHaveAttribute("data-focused", "true")

      // Press ArrowDown - focus moves to feature
      await page.keyboard.press("ArrowDown")
      await expect(getOption("feature")).toHaveAttribute("data-focused", "true")

      // Press ArrowDown - focus moves to task
      await page.keyboard.press("ArrowDown")
      await expect(getOption("task")).toHaveAttribute("data-focused", "true")

      // Press ArrowUp - focus moves back to feature
      await page.keyboard.press("ArrowUp")
      await expect(getOption("feature")).toHaveAttribute("data-focused", "true")
    })

    test("Enter selects focused option", async ({ page }) => {
      const { patchCalls } = await setupMocks(page)
      await openTestPanel(page, 0) // bug issue

      const { trigger, menu } = getTypeDropdown(page)

      // Open dropdown
      await trigger.click()
      await expect(menu).toBeVisible()

      // Navigate down to feature
      await page.keyboard.press("ArrowDown")

      // Press Enter to select
      await page.keyboard.press("Enter")

      // Wait for PATCH call
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/issues/type-test-bug") &&
          res.request().method() === "PATCH"
      )

      // Verify type changed to feature
      expect(patchCalls).toHaveLength(1)
      expect(patchCalls[0].body).toEqual({ issue_type: "feature" })

      // Dropdown should close
      await expect(menu).not.toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("displays different types correctly when navigating between issues", async ({ page }) => {
      await setupMocks(page)

      // Test bug issue
      await openTestPanel(page, 0)
      let { trigger } = getTypeDropdown(page)
      await expect(trigger).toContainText("Bug")

      // Navigate to feature issue
      await page.goto(buildTestUrl(testIssues[1]))
      await expect(page.getByTestId("issue-detail-panel")).toBeVisible()
      trigger = getTypeDropdown(page).trigger
      await expect(trigger).toContainText("Feature")

      // Navigate to epic issue
      await page.goto(buildTestUrl(testIssues[3]))
      await expect(page.getByTestId("issue-detail-panel")).toBeVisible()
      trigger = getTypeDropdown(page).trigger
      await expect(trigger).toContainText("Epic")
    })
  })
})
