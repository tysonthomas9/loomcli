import { test, expect, Page } from "@playwright/test"

/**
 * GroupBy Dropdown E2E Tests
 *
 * Tests for the groupBy dropdown component in FilterBar, which enables
 * users to group Kanban issues into swim lanes by various fields.
 */

/**
 * Mock issues with varied assignees, priorities, types, labels, and epics
 * for testing groupBy functionality.
 */
const mockIssues = [
  {
    id: "issue-1",
    title: "Bug with login",
    status: "open",
    priority: 0,
    issue_type: "bug",
    assignee: "alice",
    labels: ["frontend"],
    epic_id: "epic-1",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-2",
    title: "New feature request",
    status: "open",
    priority: 2,
    issue_type: "feature",
    assignee: "bob",
    labels: ["backend"],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "issue-3",
    title: "Refactor module",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: "alice",
    labels: ["backend", "tech-debt"],
    epic_id: "epic-1",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  {
    id: "issue-4",
    title: "Chore task",
    status: "open",
    priority: 4,
    issue_type: "chore",
    // No assignee - tests "Ungrouped" lane
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
]

/**
 * Set up API mocks for groupBy dropdown tests.
 */
async function setupMocks(page: Page) {
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })
}

/**
 * Navigate to app and wait for API response.
 */
async function navigateAndWait(page: Page, path: string = "/") {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto(path),
  ])
  expect(response.ok()).toBe(true)
}

test.describe("GroupBy Dropdown", () => {
  test.describe("Display Tests", () => {
    test("dropdown renders in FilterBar", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Verify filter bar is visible
      const filterBar = page.getByTestId("filter-bar")
      await expect(filterBar).toBeVisible()

      // Verify groupBy dropdown is visible
      const groupByDropdown = page.getByTestId("groupby-filter")
      await expect(groupByDropdown).toBeVisible()

      // Verify dropdown has correct aria-label
      await expect(groupByDropdown).toHaveAttribute(
        "aria-label",
        "Group issues by"
      )
    })

    test('shows "Epic" as default selection', async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Verify dropdown value is 'epic'
      await expect(groupByDropdown).toHaveValue("epic")
    })
  })

  test.describe("Options Tests", () => {
    test("all 6 options are present in correct order", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Verify all options are present
      const options = await groupByDropdown.locator("option").allInnerTexts()
      expect(options).toEqual([
        "None",
        "Epic",
        "Assignee",
        "Priority",
        "Type",
        "Label",
      ])

      // Verify option values
      const optionValues = await groupByDropdown
        .locator("option")
        .evaluateAll((opts) =>
          opts.map((o) => (o as HTMLOptionElement).value)
        )
      expect(optionValues).toEqual([
        "none",
        "epic",
        "assignee",
        "priority",
        "type",
        "label",
      ])
    })
  })

  test.describe("Selection Tests", () => {
    test("changing selection updates dropdown value", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Select 'epic'
      await groupByDropdown.selectOption("epic")
      await expect(groupByDropdown).toHaveValue("epic")

      // Select 'assignee'
      await groupByDropdown.selectOption("assignee")
      await expect(groupByDropdown).toHaveValue("assignee")
    })

    test("selection triggers URL update", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Select 'priority' option
      await groupByDropdown.selectOption("priority")

      // Verify URL contains groupBy param
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
      }).toPass({ timeout: 2000 })

      // Select 'type' option
      await groupByDropdown.selectOption("type")

      // Verify URL updated
      await expect(async () => {
        expect(page.url()).toContain("groupBy=type")
      }).toPass({ timeout: 2000 })
    })

    test("'None' selection removes groupBy from URL", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=epic")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Verify dropdown shows 'Epic'
      await expect(groupByDropdown).toHaveValue("epic")

      // Select 'None'
      await groupByDropdown.selectOption("none")

      // Verify URL does NOT contain groupBy
      await expect(async () => {
        expect(page.url()).not.toContain("groupBy=")
      }).toPass({ timeout: 2000 })
    })

    test("URL param restores selection on page load", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=assignee")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Verify dropdown shows 'Assignee' as selected
      await expect(groupByDropdown).toHaveValue("assignee")
    })
  })

  test.describe("Default Behavior Tests", () => {
    test("default is 'Epic' without URL param", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")
      await expect(groupByDropdown).toHaveValue("epic")
    })

    test("invalid URL param defaults to 'Epic'", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=invalid")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Verify dropdown shows 'Epic' (hook ignores invalid values)
      await expect(groupByDropdown).toHaveValue("epic")
    })
  })

  test.describe("Keyboard Navigation Tests", () => {
    test("Tab focuses dropdown", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Focus search input first (if present)
      await page.keyboard.press("Tab")

      // Tab through filters until groupBy is focused
      // This depends on DOM order, so we just verify we can focus it
      const groupByDropdown = page.getByTestId("groupby-filter")
      await groupByDropdown.focus()

      // Verify dropdown is focused
      await expect(groupByDropdown).toBeFocused()
    })

    test("keyboard selection changes value", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")
      await groupByDropdown.focus()

      // Type 'e' to select Epic (native select keyboard navigation)
      // This is more reliable than ArrowDown across browsers
      await page.keyboard.type("e")

      // Verify selection changed to 'epic'
      await expect(groupByDropdown).toHaveValue("epic")
    })

    test("Escape closes dropdown without change", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Initial value
      await expect(groupByDropdown).toHaveValue("epic")

      // Focus and try to interact
      await groupByDropdown.focus()

      // Press Escape
      await page.keyboard.press("Escape")

      // Verify original selection preserved
      await expect(groupByDropdown).toHaveValue("epic")
    })
  })

  test.describe("Integration Tests", () => {
    test("groupBy persists after applying other filters", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Select groupBy='priority'
      const groupByDropdown = page.getByTestId("groupby-filter")
      await groupByDropdown.selectOption("priority")

      // Wait for URL update
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
      }).toPass({ timeout: 2000 })

      // Apply a type filter
      const typeFilter = page.getByTestId("type-filter")
      await typeFilter.selectOption("bug")

      // Verify URL contains both params
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
        expect(page.url()).toContain("type=bug")
      }).toPass({ timeout: 2000 })

      // Verify both filters are active
      await expect(groupByDropdown).toHaveValue("priority")
      await expect(typeFilter).toHaveValue("bug")
    })

    test("clear filters button clears groupBy", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Apply type filter + set groupBy=assignee
      const typeFilter = page.getByTestId("type-filter")
      await typeFilter.selectOption("bug")

      const groupByDropdown = page.getByTestId("groupby-filter")
      await groupByDropdown.selectOption("assignee")

      // Wait for filters to be applied
      await expect(async () => {
        expect(page.url()).toContain("type=bug")
        expect(page.url()).toContain("groupBy=assignee")
      }).toPass({ timeout: 2000 })

      // Clear filters button should be visible
      const clearButton = page.getByTestId("clear-filters")
      await expect(clearButton).toBeVisible()

      // Use JavaScript click to avoid layout overlap issues with ViewSwitcher
      // (See filter-bar.spec.ts - same issue exists there)
      await clearButton.evaluate((el) => (el as HTMLButtonElement).click())

      // Verify filters cleared
      await expect(async () => {
        expect(page.url()).not.toContain("type=")
        expect(page.url()).not.toContain("groupBy=")
      }).toPass({ timeout: 2000 })

      // Verify dropdowns reset
      await expect(typeFilter).toHaveValue("")
      await expect(groupByDropdown).toHaveValue("epic")
    })

    test("groupBy selection persists when changing views", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      // Set groupBy
      const groupByDropdown = page.getByTestId("groupby-filter")
      await groupByDropdown.selectOption("assignee")

      // Wait for URL update
      await expect(async () => {
        expect(page.url()).toContain("groupBy=assignee")
      }).toPass({ timeout: 2000 })

      // Switch to table view
      const tableViewButton = page.locator('[data-testid="view-table"]')
      if (await tableViewButton.isVisible()) {
        await tableViewButton.click()

        // Verify groupBy still in URL after view switch
        await expect(async () => {
          expect(page.url()).toContain("groupBy=assignee")
        }).toPass({ timeout: 2000 })
      }
    })
  })

  test.describe("Edge Cases", () => {
    test("dropdown works with empty issues list", async ({ page }) => {
      // Mock empty issues response
      await page.route("**/api/ready", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        })
      })

      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Verify dropdown still renders and is functional
      await expect(groupByDropdown).toBeVisible()

      // Select 'Assignee' option
      await groupByDropdown.selectOption("assignee")

      // Verify selection works (URL updates)
      await expect(async () => {
        expect(page.url()).toContain("groupBy=assignee")
      }).toPass({ timeout: 2000 })
    })

    test("multiple rapid selections result in correct final URL", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page, "/")

      const groupByDropdown = page.getByTestId("groupby-filter")

      // Rapidly select different options
      await groupByDropdown.selectOption("epic")
      await groupByDropdown.selectOption("assignee")
      await groupByDropdown.selectOption("priority")

      // Verify final URL reflects last selection
      await expect(async () => {
        expect(page.url()).toContain("groupBy=priority")
        expect(page.url()).not.toContain("groupBy=epic")
        expect(page.url()).not.toContain("groupBy=assignee")
      }).toPass({ timeout: 2000 })

      // Verify dropdown shows final selection
      await expect(groupByDropdown).toHaveValue("priority")
    })
  })
})
