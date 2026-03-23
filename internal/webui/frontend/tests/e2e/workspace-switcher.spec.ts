import { test, expect, Page } from "@playwright/test"

/**
 * E2E tests for the WorkspaceSwitcher dropdown component.
 * Tests the portal-based modal overlay for searching and switching workspaces.
 */

const mockWorkspaceData = {
  success: true,
  data: {
    name: "alpha",
    path: "/workspaces/alpha",
    repos: [
      {
        name: "repo-one",
        path: "/workspaces/alpha/repo-one",
        default_branch: "main",
        remote: "origin",
        groups: [],
      },
    ],
    groups: [],
    agents: [],
    workspaces: [
      {
        name: "alpha",
        path: "/workspaces/alpha",
        active: true,
        repo_count: 3,
        is_default: true,
      },
      {
        name: "beta",
        path: "/workspaces/beta",
        active: false,
        repo_count: 1,
        is_default: false,
      },
      {
        name: "gamma",
        path: "/workspaces/gamma",
        active: false,
        repo_count: 2,
        is_default: false,
      },
    ],
    default_workspace: "alpha",
  },
}

const mockIssues = [
  {
    id: "ws-test-1",
    title: "WS Test Issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
]

async function setupMocks(page: Page) {
  await page.route("**/api/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
      body: "event: connected\ndata: {\"message\":\"connected\"}\n\n",
    })
  })

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    })
  })

  await page.route("**/api/workspace", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockWorkspaceData),
    })
  })

  await page.route(
    (url) =>
      url.pathname.endsWith("/api/issues") ||
      url.pathname.endsWith("/api/ready"),
    async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockIssues }),
        })
      } else {
        await route.continue()
      }
    }
  )

  await page.route("**/api/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { open: 1, closed: 0, total: 1, completion: 0 },
      }),
    })
  })
}

async function navigateAndWait(page: Page) {
  await page.goto("/")
  await page.waitForTimeout(1000)
}

async function openSwitcher(page: Page) {
  await page.keyboard.press("Control+k")
  const dialog = page.getByRole("dialog", { name: "Switch workspace" })
  await expect(dialog).toBeVisible()
  return dialog
}

test.describe("Workspace Switcher Dropdown", () => {
  test.describe("Display Tests", () => {
    test("opens via Ctrl+K shortcut", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await page.keyboard.press("Control+k")
      const dialog = page.getByRole("dialog", { name: "Switch workspace" })
      await expect(dialog).toBeVisible()
    })

    test("renders dialog with correct aria attributes", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      const dialog = await openSwitcher(page)
      await expect(dialog).toHaveAttribute("role", "dialog")
      await expect(dialog).toHaveAttribute(
        "aria-label",
        "Switch workspace"
      )
    })

    test("displays all workspace names and repo counts", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      await expect(page.getByText("alpha")).toBeVisible()
      await expect(page.getByText("beta")).toBeVisible()
      await expect(page.getByText("gamma")).toBeVisible()

      await expect(page.getByText("3 repos")).toBeVisible()
      await expect(page.getByText("1 repo")).toBeVisible()
      await expect(page.getByText("2 repos")).toBeVisible()
    })

    test("auto-focuses search input on open", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await expect(searchInput).toBeFocused()
    })
  })

  test.describe("Search Filtering", () => {
    test("filters workspaces by name substring", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await searchInput.fill("alph")

      await expect(page.getByText("alpha")).toBeVisible()
      const items = page.locator("[data-workspace-item]")
      await expect(items).toHaveCount(1)
    })

    test("search is case-insensitive", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await searchInput.fill("BETA")

      await expect(page.getByText("beta")).toBeVisible()
    })

    test('shows No workspaces found when nothing matches', async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await searchInput.fill("nonexistent")

      await expect(page.getByText("No workspaces found")).toBeVisible()
    })

    test("clearing search shows all workspaces again", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await searchInput.fill("alph")
      await expect(page.locator("[data-workspace-item]")).toHaveCount(1)

      await searchInput.clear()
      await expect(page.locator("[data-workspace-item]")).toHaveCount(3)
    })
  })

  test.describe("Keyboard Navigation", () => {
    test("ArrowDown moves highlight to next item", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const items = page.locator("[data-workspace-item]")
      await expect(items.nth(0)).toHaveClass(/highlighted/)

      await page.keyboard.press("ArrowDown")
      await expect(items.nth(1)).toHaveClass(/highlighted/)
    })

    test("ArrowUp moves highlight to previous item", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      await page.keyboard.press("ArrowDown")
      await page.keyboard.press("ArrowUp")

      const items = page.locator("[data-workspace-item]")
      await expect(items.nth(0)).toHaveClass(/highlighted/)
    })

    test("ArrowDown wraps from last to first", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      await page.keyboard.press("ArrowDown")
      await page.keyboard.press("ArrowDown")
      await page.keyboard.press("ArrowDown")

      const items = page.locator("[data-workspace-item]")
      await expect(items.nth(0)).toHaveClass(/highlighted/)
    })

    test("ArrowUp wraps from first to last", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      await page.keyboard.press("ArrowUp")

      const items = page.locator("[data-workspace-item]")
      await expect(items.nth(2)).toHaveClass(/highlighted/)
    })

    test("Enter selects the highlighted workspace", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      await page.keyboard.press("ArrowDown")
      await page.keyboard.press("Enter")

      const dialog = page.getByRole("dialog", { name: "Switch workspace" })
      await expect(dialog).not.toBeVisible()
    })
  })

  test.describe("Mouse Interactions", () => {
    test("clicking a workspace item closes dialog", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const items = page.locator("[data-workspace-item]")
      await items.nth(1).click()

      const dialog = page.getByRole("dialog", { name: "Switch workspace" })
      await expect(dialog).not.toBeVisible()
    })

    test("clicking the overlay backdrop closes without selecting", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const overlay = page.locator("[class*=overlay]")
      await overlay.click({ position: { x: 5, y: 5 } })

      const dialog = page.getByRole("dialog", { name: "Switch workspace" })
      await expect(dialog).not.toBeVisible()
    })
  })

  test.describe("Dismiss Behavior", () => {
    test("Escape closes the switcher", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)
      await page.keyboard.press("Escape")

      const dialog = page.getByRole("dialog", { name: "Switch workspace" })
      await expect(dialog).not.toBeVisible()
    })

    test("reopening resets search term", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await searchInput.fill("alpha")

      await page.keyboard.press("Escape")

      await openSwitcher(page)

      const searchInputNew = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await expect(searchInputNew).toHaveValue("")
    })

    test("reopening resets highlight to first item", async ({ page }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      await page.keyboard.press("ArrowDown")
      await page.keyboard.press("ArrowDown")

      await page.keyboard.press("Escape")

      await openSwitcher(page)

      const items = page.locator("[data-workspace-item]")
      await expect(items.nth(0)).toHaveClass(/highlighted/)
    })
  })

  test.describe("Edge Cases", () => {
    test("keyboard nav after filtering adjusts to filtered list size", async ({
      page,
    }) => {
      await setupMocks(page)
      await navigateAndWait(page)

      await openSwitcher(page)

      const searchInput = page.locator(
        'input[placeholder="Switch workspace..."]'
      )
      await searchInput.fill("a")

      const items = page.locator("[data-workspace-item]")
      const count = await items.count()

      for (let i = 0; i < count + 1; i++) {
        await page.keyboard.press("ArrowDown")
      }
      await expect(items.nth(0)).toHaveClass(/highlighted/)
    })
  })
})
