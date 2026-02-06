import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for Table view column sorting.
 *
 * Tests verify click interactions, sort indicators, data ordering,
 * and keyboard accessibility for the IssueTable component.
 */

// Test issues with varied data for meaningful sort verification
const mockIssues = [
  {
    id: "aaa-001",
    title: "Zebra feature",
    status: "closed",
    priority: 4,
    issue_type: "chore",
    assignee: "alice",
    created_at: "2026-01-20T10:00:00Z",
    updated_at: "2026-01-22T10:00:00Z",
  },
  {
    id: "zzz-002",
    title: "Alpha bug",
    status: "open",
    priority: 0,
    issue_type: "bug",
    assignee: "zach",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-26T10:00:00Z",
  },
  {
    id: "mmm-003",
    title: "Middle task",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: "bob",
    created_at: "2026-01-22T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
]

/**
 * Set up API mocks for table sort tests.
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
 * Navigate to Table view and wait for data to load.
 */
async function navigateToTable(page: Page) {
  await Promise.all([
    page.waitForResponse(
      (res) => res.url().includes("/api/ready") && res.status() === 200
    ),
    page.goto("/?view=table"),
  ])
  await expect(page.getByTestId("issue-table")).toBeVisible()
}

/**
 * Get a column header element by column ID.
 */
function getColumnHeader(page: Page, columnId: string) {
  return page.locator(`th[data-column="${columnId}"]`)
}

/**
 * Get all row IDs in their current display order.
 */
async function getRowIds(page: Page): Promise<string[]> {
  const idCells = page.locator(
    '[data-testid="issue-table"] tbody tr td[data-column="id"]'
  )
  return await idCells.allTextContents()
}

/**
 * Get all row titles in their current display order.
 */
async function getRowTitles(page: Page): Promise<string[]> {
  const titleCells = page.locator(
    '[data-testid="issue-table"] tbody tr td[data-column="title"]'
  )
  return await titleCells.allTextContents()
}

test.describe("Table Column Sorting", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test.describe("Initial State", () => {
    test("all sortable columns show unsorted state", async ({ page }) => {
      await navigateToTable(page)

      // Verify sortable columns have aria-sort="none" initially
      const sortableColumns = ["id", "priority", "title", "status", "issue_type"]
      for (const col of sortableColumns) {
        const header = getColumnHeader(page, col)
        await expect(header).toHaveAttribute("aria-sort", "none")
      }
    })

    test("sort indicator shows bidirectional arrow when unsorted", async ({
      page,
    }) => {
      await navigateToTable(page)

      // Verify ID column sort indicator shows ↕
      const idHeader = getColumnHeader(page, "id")
      const indicator = idHeader.locator(".issue-table__sort-indicator")
      await expect(indicator).toHaveText("↕")
    })
  })

  test.describe("Sort by ID", () => {
    test("click ID header sorts ascending", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.click()

      // Verify aria-sort is ascending
      await expect(idHeader).toHaveAttribute("aria-sort", "ascending")

      // Verify sort indicator shows ▲
      const indicator = idHeader.locator(".issue-table__sort-indicator")
      await expect(indicator).toHaveText("▲")

      // Verify rows are sorted: aaa-001, mmm-003, zzz-002
      const ids = await getRowIds(page)
      expect(ids).toEqual(["aaa-001", "mmm-003", "zzz-002"])
    })

    test("second click sorts descending", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.click() // First click: ascending
      await idHeader.click() // Second click: descending

      // Verify aria-sort is descending
      await expect(idHeader).toHaveAttribute("aria-sort", "descending")

      // Verify sort indicator shows ▼
      const indicator = idHeader.locator(".issue-table__sort-indicator")
      await expect(indicator).toHaveText("▼")

      // Verify rows are sorted: zzz-002, mmm-003, aaa-001
      const ids = await getRowIds(page)
      expect(ids).toEqual(["zzz-002", "mmm-003", "aaa-001"])
    })

    test("third click clears sort", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.click() // First: ascending
      await idHeader.click() // Second: descending
      await idHeader.click() // Third: clear

      // Verify aria-sort is none
      await expect(idHeader).toHaveAttribute("aria-sort", "none")

      // Verify sort indicator shows ↕
      const indicator = idHeader.locator(".issue-table__sort-indicator")
      await expect(indicator).toHaveText("↕")
    })
  })

  test.describe("Sort by Title", () => {
    test("click Title header sorts alphabetically ascending", async ({
      page,
    }) => {
      await navigateToTable(page)

      const titleHeader = getColumnHeader(page, "title")
      await titleHeader.click()

      // Verify rows ordered: Alpha bug, Middle task, Zebra feature
      const titles = await getRowTitles(page)
      expect(titles).toEqual(["Alpha bug", "Middle task", "Zebra feature"])
    })

    test("click again sorts alphabetically descending", async ({ page }) => {
      await navigateToTable(page)

      const titleHeader = getColumnHeader(page, "title")
      await titleHeader.click() // ascending
      await titleHeader.click() // descending

      // Verify rows ordered: Zebra feature, Middle task, Alpha bug
      const titles = await getRowTitles(page)
      expect(titles).toEqual(["Zebra feature", "Middle task", "Alpha bug"])
    })
  })

  test.describe("Sort by Priority", () => {
    test("click Priority header sorts ascending (P0 first)", async ({
      page,
    }) => {
      await navigateToTable(page)

      const priorityHeader = getColumnHeader(page, "priority")
      await priorityHeader.click()

      // Verify rows ordered by priority: 0, 2, 4 (P0, P2, P4)
      // zzz-002 (P0), mmm-003 (P2), aaa-001 (P4)
      const ids = await getRowIds(page)
      expect(ids).toEqual(["zzz-002", "mmm-003", "aaa-001"])
    })

    test("click again sorts descending (P4 first)", async ({ page }) => {
      await navigateToTable(page)

      const priorityHeader = getColumnHeader(page, "priority")
      await priorityHeader.click() // ascending
      await priorityHeader.click() // descending

      // Verify rows ordered: 4, 2, 0 (P4, P2, P0)
      const ids = await getRowIds(page)
      expect(ids).toEqual(["aaa-001", "mmm-003", "zzz-002"])
    })
  })

  test.describe("Sort by Status", () => {
    test("click Status header sorts by status", async ({ page }) => {
      await navigateToTable(page)

      const statusHeader = getColumnHeader(page, "status")
      await statusHeader.click()

      // Verify rows sort by status (string comparison):
      // closed < in_progress < open
      const ids = await getRowIds(page)
      expect(ids).toEqual(["aaa-001", "mmm-003", "zzz-002"])
    })
  })

  test.describe("Sort by Type", () => {
    test("click Type header sorts by issue type", async ({ page }) => {
      await navigateToTable(page)

      const typeHeader = getColumnHeader(page, "issue_type")
      await typeHeader.click()

      // Verify rows ordered alphabetically by type: bug, chore, task
      const ids = await getRowIds(page)
      expect(ids).toEqual(["zzz-002", "aaa-001", "mmm-003"])
    })
  })

  test.describe("Sort Indicators", () => {
    test("sort indicator appears on active column", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.click()

      // Verify ID column header has sorted class
      await expect(idHeader).toHaveClass(/issue-table__header-cell--sorted/)
    })

    test("only one column shows sorted at a time", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      const titleHeader = getColumnHeader(page, "title")

      // Sort by ID first
      await idHeader.click()
      await expect(idHeader).toHaveClass(/issue-table__header-cell--sorted/)

      // Sort by Title
      await titleHeader.click()

      // Verify only Title shows sorted, ID no longer sorted
      await expect(titleHeader).toHaveClass(/issue-table__header-cell--sorted/)
      await expect(idHeader).not.toHaveClass(/issue-table__header-cell--sorted/)
    })

    test("ascending shows up arrow", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.click()

      const indicator = idHeader.locator(".issue-table__sort-indicator")
      await expect(indicator).toHaveText("▲")
      await expect(indicator).toHaveClass(/issue-table__sort-indicator--asc/)
    })

    test("descending shows down arrow", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.click() // ascending
      await idHeader.click() // descending

      const indicator = idHeader.locator(".issue-table__sort-indicator")
      await expect(indicator).toHaveText("▼")
      await expect(indicator).toHaveClass(/issue-table__sort-indicator--desc/)
    })
  })

  test.describe("Sort Persistence", () => {
    test("sort persists when switching columns", async ({ page }) => {
      await navigateToTable(page)

      // Sort by ID ascending
      const idHeader = getColumnHeader(page, "id")
      await idHeader.click()
      await expect(idHeader).toHaveAttribute("aria-sort", "ascending")

      // Click Title to sort by Title
      const titleHeader = getColumnHeader(page, "title")
      await titleHeader.click()

      // Verify Title is sorted, ID is not
      await expect(titleHeader).toHaveAttribute("aria-sort", "ascending")
      await expect(idHeader).toHaveAttribute("aria-sort", "none")

      // Data should be in Title sort order
      const titles = await getRowTitles(page)
      expect(titles).toEqual(["Alpha bug", "Middle task", "Zebra feature"])
    })
  })

  test.describe("Keyboard Navigation", () => {
    test("Enter key on focused column header triggers sort", async ({
      page,
    }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.focus()
      await page.keyboard.press("Enter")

      // Verify sort was applied
      await expect(idHeader).toHaveAttribute("aria-sort", "ascending")
      const ids = await getRowIds(page)
      expect(ids).toEqual(["aaa-001", "mmm-003", "zzz-002"])
    })

    test("Space key on focused column header triggers sort", async ({
      page,
    }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")
      await idHeader.focus()
      await page.keyboard.press("Space")

      // Verify sort was applied
      await expect(idHeader).toHaveAttribute("aria-sort", "ascending")
    })

    test("Tab navigates between sortable headers", async ({ page }) => {
      await navigateToTable(page)

      // Focus on first sortable header (ID)
      const idHeader = getColumnHeader(page, "id")
      await idHeader.focus()
      await expect(idHeader).toBeFocused()

      // Tab to next sortable header (Priority)
      await page.keyboard.press("Tab")
      const priorityHeader = getColumnHeader(page, "priority")
      await expect(priorityHeader).toBeFocused()
    })
  })

  test.describe("Accessibility", () => {
    test("sortable headers have role button", async ({ page }) => {
      await navigateToTable(page)

      const sortableColumns = ["id", "priority", "title", "status", "issue_type"]
      for (const col of sortableColumns) {
        const header = getColumnHeader(page, col)
        await expect(header).toHaveAttribute("role", "button")
      }
    })

    test("aria-label describes sort state", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")

      // Click to sort ascending
      await idHeader.click()
      await expect(idHeader).toHaveAttribute(
        "aria-label",
        /currently sorted ascending/
      )

      // Click again for descending
      await idHeader.click()
      await expect(idHeader).toHaveAttribute(
        "aria-label",
        /currently sorted descending/
      )
    })

    test("aria-sort attribute updates correctly", async ({ page }) => {
      await navigateToTable(page)

      const idHeader = getColumnHeader(page, "id")

      // Initial state: none
      await expect(idHeader).toHaveAttribute("aria-sort", "none")

      // Click: ascending
      await idHeader.click()
      await expect(idHeader).toHaveAttribute("aria-sort", "ascending")

      // Click: descending
      await idHeader.click()
      await expect(idHeader).toHaveAttribute("aria-sort", "descending")

      // Click: back to none
      await idHeader.click()
      await expect(idHeader).toHaveAttribute("aria-sort", "none")
    })
  })

  test.describe("Edge Cases", () => {
    test("sorting with empty table displays empty message", async ({
      page,
    }) => {
      // Override mock to return empty data
      await page.route("**/api/ready", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        })
      })

      await navigateToTable(page)

      // Verify empty message is shown
      await expect(page.getByTestId("issue-table-empty")).toBeVisible()

      // Sorting should still work (no error)
      const idHeader = getColumnHeader(page, "id")
      await idHeader.click()
      await expect(idHeader).toHaveAttribute("aria-sort", "ascending")
    })
  })
})
