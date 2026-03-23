import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the NavRail component.
 * Tests the icon-only vertical navigation rail that provides primary view switching.
 */

const mockIssues = [
  {
    id: "nav-test-1",
    title: "Nav Test Issue 1",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "nav-test-2",
    title: "Nav Test Issue 2",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
]

async function setupMocks(page: Page) {
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({ status: 404 })
  })
  await page.route("**/api/ready", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })
  await page.route("**/api/issues/graph", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: mockIssues }),
    })
  })
  await page.route("**/api/events", async (route) => {
    await route.abort()
  })
  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 2, closed: 0, total: 2, completion: 0 },
      }),
    })
  })
}

async function navigateAndWait(page: Page, path = "/") {
  await page.goto(path)
  const nav = page.locator('nav[aria-label="Primary"]')
  await nav.waitFor({ state: "visible", timeout: 10000 })
}

test.describe("NavRail rendering", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("renders navigation landmark with aria-label", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    await expect(nav).toBeVisible()
  })

  test("renders all navigation buttons", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    const buttons = nav.locator("button")
    await expect(buttons).toHaveCount(6)
  })

  test("all buttons have aria-labels", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    await expect(nav.getByLabel("Kanban")).toBeVisible()
    await expect(nav.getByLabel("List")).toBeVisible()
    await expect(nav.getByLabel("Observability")).toBeVisible()
    await expect(nav.getByLabel("Files")).toBeVisible()
    await expect(nav.getByLabel("Terminal")).toBeVisible()
    await expect(nav.getByLabel("Settings")).toBeVisible()
  })

  test("Kanban button is active by default on root path", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    const kanbanBtn = nav.getByLabel("Kanban")
    await expect(kanbanBtn).toHaveAttribute("data-active", "true")

    const listBtn = nav.getByLabel("List")
    await expect(listBtn).not.toHaveAttribute("data-active", "true")
  })

  test("renders buttons in correct order - top items before settings", async ({
    page,
  }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    const buttons = nav.locator("button")

    await expect(buttons.nth(0)).toHaveAttribute("aria-label", "Kanban")
    await expect(buttons.nth(1)).toHaveAttribute("aria-label", "List")
    await expect(buttons.nth(2)).toHaveAttribute("aria-label", "Observability")
    await expect(buttons.nth(3)).toHaveAttribute("aria-label", "Files")
    await expect(buttons.nth(4)).toHaveAttribute("aria-label", "Terminal")
    await expect(buttons.nth(5)).toHaveAttribute("aria-label", "Settings")
  })

  test("SVG icons are hidden from screen readers", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    const svgs = nav.locator("svg")
    const count = await svgs.count()
    for (let i = 0; i < count; i++) {
      await expect(svgs.nth(i)).toHaveAttribute("aria-hidden", "true")
    }
  })
})

test.describe("NavRail view switching", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("clicking List switches to table view", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')

    await nav.getByLabel("List").click()
    await expect(nav.getByLabel("List")).toHaveAttribute("data-active", "true")
    await expect(nav.getByLabel("Kanban")).not.toHaveAttribute(
      "data-active",
      "true"
    )
  })

  test("clicking Terminal switches to terminal view", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')

    await nav.getByLabel("Terminal").click()
    await expect(nav.getByLabel("Terminal")).toHaveAttribute(
      "data-active",
      "true"
    )
    await expect(nav.getByLabel("Kanban")).not.toHaveAttribute(
      "data-active",
      "true"
    )
  })

  test("clicking Settings switches to settings view", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')

    await nav.getByLabel("Settings").click()
    await expect(nav.getByLabel("Settings")).toHaveAttribute(
      "data-active",
      "true"
    )
    await expect(nav.getByLabel("Kanban")).not.toHaveAttribute(
      "data-active",
      "true"
    )
  })

  test("clicking Kanban returns to kanban view from List", async ({ page }) => {
    await navigateAndWait(page, "/?view=table")
    const nav = page.locator('nav[aria-label="Primary"]')

    await expect(nav.getByLabel("List")).toHaveAttribute("data-active", "true")

    await nav.getByLabel("Kanban").click()
    await expect(nav.getByLabel("Kanban")).toHaveAttribute(
      "data-active",
      "true"
    )
    await expect(nav.getByLabel("List")).not.toHaveAttribute(
      "data-active",
      "true"
    )
  })

  test("clicking already-active button keeps view", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')

    await nav.getByLabel("Kanban").click()
    await expect(nav.getByLabel("Kanban")).toHaveAttribute(
      "data-active",
      "true"
    )
  })

  test("rapid view switching resolves to final view", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')

    await nav.getByLabel("List").click()
    await nav.getByLabel("Terminal").click()
    await nav.getByLabel("Settings").click()

    await expect(nav.getByLabel("Settings")).toHaveAttribute(
      "data-active",
      "true"
    )
    await expect(nav.getByLabel("List")).not.toHaveAttribute(
      "data-active",
      "true"
    )
    await expect(nav.getByLabel("Terminal")).not.toHaveAttribute(
      "data-active",
      "true"
    )
  })
})

test.describe("NavRail tooltips", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("tooltip elements exist with correct labels", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    const tooltips = nav.locator('[role="tooltip"]')
    const count = await tooltips.count()
    expect(count).toBe(6)

    await expect(tooltips.nth(0)).toHaveText("Kanban")
    await expect(tooltips.nth(1)).toHaveText("List")
    await expect(tooltips.nth(2)).toHaveText("Observability")
    await expect(tooltips.nth(3)).toHaveText("Files")
    await expect(tooltips.nth(4)).toHaveText("Terminal")
    await expect(tooltips.nth(5)).toHaveText("Settings")
  })
})

test.describe("NavRail accessibility", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page)
  })

  test("navigation landmark is present", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    await expect(nav).toBeVisible()
  })

  test("buttons are keyboard focusable", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')
    const kanbanBtn = nav.getByLabel("Kanban")

    await kanbanBtn.focus()
    await expect(kanbanBtn).toBeFocused()
  })

  test("active state communicated via data-active", async ({ page }) => {
    await navigateAndWait(page)
    const nav = page.locator('nav[aria-label="Primary"]')

    await expect(nav.getByLabel("Kanban")).toHaveAttribute(
      "data-active",
      "true"
    )

    await nav.getByLabel("List").click()
    await expect(nav.getByLabel("List")).toHaveAttribute("data-active", "true")
    await expect(nav.getByLabel("Kanban")).not.toHaveAttribute(
      "data-active",
      "true"
    )
  })
})
