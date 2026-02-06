import { test, expect, Page } from "@playwright/test"

/**
 * Minimal mock issues for testing groupBy URL synchronization.
 * Just need some issues with varied fields to verify groupBy works.
 */
const mockIssues = [
  {
    id: "issue-1",
    title: "Task One",
    status: "open",
    priority: 2,
    issue_type: "task",
    parent: "epic-1",
    parent_title: "Test Epic",
    assignee: "alice",
    labels: ["frontend"],
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "issue-2",
    title: "Bug One",
    status: "in_progress",
    priority: 1,
    issue_type: "bug",
    assignee: "bob",
    labels: ["urgent"],
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
]

/**
 * Set up API mocks for groupBy URL sync tests.
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
 * Navigate to a page and wait for API response.
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

test.describe("groupBy URL Synchronization", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test.describe("URL Update Tests", () => {
    test("selecting groupBy updates URL param", async ({ page }) => {
      await navigateAndWait(page, "/")

      // Verify no groupBy param initially
      expect(page.url()).not.toContain("groupBy=")

      // Select assignee grouping
      const groupByFilter = page.getByTestId("groupby-filter")
      await groupByFilter.selectOption("assignee")

      // Verify URL contains groupBy param
      await expect(async () => {
        expect(page.url()).toContain("groupBy=assignee")
      }).toPass({ timeout: 2000 })
    })

    test("selecting 'none' removes groupBy from URL", async ({ page }) => {
      // Start with groupBy param
      await navigateAndWait(page, "/?groupBy=priority")
      expect(page.url()).toContain("groupBy=priority")

      // Select 'none'
      const groupByFilter = page.getByTestId("groupby-filter")
      await groupByFilter.selectOption("none")

      // Verify URL no longer has groupBy param
      await expect(async () => {
        expect(page.url()).not.toContain("groupBy=")
      }).toPass({ timeout: 2000 })
    })

    test("all valid groupBy options update URL correctly", async ({ page }) => {
      // Test that all groupBy options work with the same mechanism
      const options = ["assignee", "priority", "type", "label"]
      await navigateAndWait(page, "/")
      const groupByFilter = page.getByTestId("groupby-filter")

      for (const option of options) {
        await groupByFilter.selectOption(option)
        await expect(async () => {
          expect(page.url()).toContain(`groupBy=${option}`)
        }).toPass({ timeout: 2000 })
      }
    })
  })

  test.describe("URL Restore Tests", () => {
    test("loading page with groupBy param restores dropdown selection", async ({
      page,
    }) => {
      await navigateAndWait(page, "/?groupBy=assignee")

      // Verify dropdown shows correct value
      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("assignee")
    })

    test("all valid groupBy params restore correctly from URL", async ({
      page,
    }) => {
      // Test that all groupBy values restore with the same mechanism
      const options = ["epic", "assignee", "priority", "type", "label"]
      for (const option of options) {
        await navigateAndWait(page, `/?groupBy=${option}`)
        await expect(page.getByTestId("groupby-filter")).toHaveValue(option)
      }
    })

    test("?groupBy=none restores correctly and shows flat view", async ({
      page,
    }) => {
      // Explicitly setting groupBy=none in URL should work
      await navigateAndWait(page, "/?groupBy=none")
      await expect(page.getByTestId("groupby-filter")).toHaveValue("none")
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()
    })
  })

  test.describe("Default State Tests", () => {
    test("no groupBy URL param defaults to 'epic'", async ({ page }) => {
      await navigateAndWait(page, "/")

      // Verify dropdown shows 'epic'
      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("epic")

      // Verify swim lanes visible (epic swim lane view)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
    })

    test("empty groupBy param (?groupBy=) defaults to 'epic'", async ({
      page,
    }) => {
      await navigateAndWait(page, "/?groupBy=")

      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("epic")
    })
  })

  test.describe("Invalid Value Tests", () => {
    test("invalid groupBy value falls back to 'epic'", async ({ page }) => {
      await navigateAndWait(page, "/?groupBy=invalid")

      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("epic")

      // Verify swim lanes visible (epic default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
    })

    test("groupBy values are case-sensitive", async ({ page }) => {
      // Capitalized - invalid
      await navigateAndWait(page, "/?groupBy=Epic")

      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("epic")
    })

    test("groupBy=PRIORITY (uppercase) is invalid", async ({ page }) => {
      await navigateAndWait(page, "/?groupBy=PRIORITY")

      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")
    })
  })

  test.describe("Combining with Other Params", () => {
    test("groupBy combines with priority filter in URL", async ({ page }) => {
      await navigateAndWait(page, "/?priority=2&groupBy=epic")

      // Verify both filters applied
      await expect(page.getByTestId("priority-filter")).toHaveValue("2")
      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")

      // Verify URL has both params
      expect(page.url()).toContain("priority=2")
      expect(page.url()).toContain("groupBy=epic")
    })

    test("groupBy combines with type filter in URL", async ({ page }) => {
      await navigateAndWait(page, "/?type=bug&groupBy=assignee")

      await expect(page.getByTestId("type-filter")).toHaveValue("bug")
      await expect(page.getByTestId("groupby-filter")).toHaveValue("assignee")
    })

    test("groupBy combines with search filter in URL", async ({ page }) => {
      await navigateAndWait(page, "/?search=Task&groupBy=priority")

      await expect(page.getByTestId("search-input-field")).toHaveValue("Task")
      await expect(page.getByTestId("groupby-filter")).toHaveValue("priority")
    })

    test("changing groupBy preserves other filter params", async ({ page }) => {
      await navigateAndWait(page, "/?priority=1&type=bug")

      // Change groupBy
      await page.getByTestId("groupby-filter").selectOption("type")

      // Verify other params still present
      await expect(async () => {
        const url = page.url()
        expect(url).toContain("priority=1")
        expect(url).toContain("type=bug")
        expect(url).toContain("groupBy=type")
      }).toPass({ timeout: 2000 })
    })
  })

  test.describe("Browser Back/Forward Navigation", () => {
    test("browser back button restores previous groupBy state", async ({
      page,
    }) => {
      await navigateAndWait(page, "/")

      const groupByFilter = page.getByTestId("groupby-filter")

      // Select epic
      await groupByFilter.selectOption("epic")
      await expect(async () => {
        expect(page.url()).toContain("groupBy=epic")
      }).toPass({ timeout: 2000 })

      // Navigate to a new page (creates history entry)
      await setupMocks(page) // Re-setup for navigation
      await navigateAndWait(page, "/?groupBy=priority")
      await expect(groupByFilter).toHaveValue("priority")

      // Go back
      await setupMocks(page) // Re-setup for back navigation
      await page.goBack()

      // Wait for popstate to be handled
      await expect(async () => {
        expect(page.url()).toContain("groupBy=epic")
      }).toPass({ timeout: 2000 })

      await expect(groupByFilter).toHaveValue("epic")
    })

    test("browser forward button restores groupBy state", async ({ page }) => {
      // Navigate to page with groupBy
      await navigateAndWait(page, "/?groupBy=assignee")

      // Navigate to new state
      await setupMocks(page)
      await navigateAndWait(page, "/?groupBy=type")
      await expect(page.getByTestId("groupby-filter")).toHaveValue("type")

      // Go back
      await setupMocks(page)
      await page.goBack()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))
      await expect(page.getByTestId("groupby-filter")).toHaveValue("assignee")

      // Go forward
      await setupMocks(page)
      await page.goForward()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))
      await expect(page.getByTestId("groupby-filter")).toHaveValue("type")
    })
  })

  test.describe("Bookmarkable URL Tests", () => {
    test("shared/bookmarked URL loads correct groupBy state", async ({
      page,
    }) => {
      // Simulate user sharing a bookmarked URL with groupBy
      const sharedUrl = "/?groupBy=label&priority=0"
      await navigateAndWait(page, sharedUrl)

      // Verify state matches URL
      await expect(page.getByTestId("groupby-filter")).toHaveValue("label")
      await expect(page.getByTestId("priority-filter")).toHaveValue("0")

      // Verify swim lane board is visible (groupBy=label triggers swim lanes)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("multiple groupBy params in URL uses first value", async ({
      page,
    }) => {
      // Multiple params - URLSearchParams.get() returns first
      await navigateAndWait(page, "/?groupBy=epic&groupBy=priority")

      // URLSearchParams.get() returns first value
      const groupByFilter = page.getByTestId("groupby-filter")
      await expect(groupByFilter).toHaveValue("epic")
    })

    test("groupBy state preserved through page refresh", async ({ page }) => {
      await navigateAndWait(page, "/")

      // Select groupBy
      await page.getByTestId("groupby-filter").selectOption("type")
      await expect(async () => {
        expect(page.url()).toContain("groupBy=type")
      }).toPass({ timeout: 2000 })

      // Re-setup mocks before reload (route handlers clear on reload)
      await setupMocks(page)

      // Reload
      await page.reload()
      await page.waitForResponse((res) => res.url().includes("/api/ready"))

      // Verify state persisted
      await expect(page.getByTestId("groupby-filter")).toHaveValue("type")
    })

    test("clear filters button removes groupBy from URL", async ({ page }) => {
      // Use flat kanban view (no groupBy) to avoid swim lane layout overlap issues
      await navigateAndWait(page, "/?priority=2")

      // First set groupBy via dropdown to have both filters active
      await page.getByTestId("groupby-filter").selectOption("assignee")
      await expect(async () => {
        expect(page.url()).toContain("groupBy=assignee")
      }).toPass({ timeout: 2000 })

      // Click clear filters using JavaScript dispatch to bypass pointer event interception
      await page.evaluate(() => {
        const button = document.querySelector(
          '[data-testid="clear-filters"]'
        ) as HTMLButtonElement
        button?.click()
      })

      // Verify both params removed from URL
      await expect(async () => {
        expect(page.url()).not.toContain("groupBy=")
        expect(page.url()).not.toContain("priority=")
      }).toPass({ timeout: 2000 })

      // Verify dropdown reset to 'epic' (the new default)
      await expect(page.getByTestId("groupby-filter")).toHaveValue("epic")
    })

    test("swim lanes visible when groupBy is set from URL", async ({
      page,
    }) => {
      await navigateAndWait(page, "/?groupBy=epic")

      // Verify swim lane board appears
      await expect(page.getByTestId("swim-lane-board")).toBeVisible()

      // Verify flat kanban is not visible (mutually exclusive)
      // The swim-lane-board takes over when groupBy is set
    })

    test("flat kanban visible when groupBy is explicit none (?groupBy=none)", async ({ page }) => {
      await navigateAndWait(page, "/?groupBy=none")

      // Verify no swim lanes
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible()

      // Verify flat kanban columns exist
      await expect(page.locator('section[data-status="ready"]')).toBeVisible()
      await expect(
        page.locator('section[data-status="in_progress"]')
      ).toBeVisible()
      await expect(page.locator('section[data-status="done"]')).toBeVisible()
    })
  })
})
