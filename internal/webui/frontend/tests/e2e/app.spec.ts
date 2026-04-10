import { test, expect, type Page } from "@playwright/test"

/**
 * Set up minimum API mocks so the app can boot in mode=open.
 * Without these, the Vite dev server returns HTML for API routes
 * causing parse errors and the BootError screen.
 */
async function setupAppMocks(page: Page) {
  // Auth mode discovery — must succeed for app to render
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname !== "/api/config") {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    })
  })

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
    })
  })

  // Health check
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    })
  })

  // Workspace-scoped routes
  await page.route(
    (url) => url.toString().includes("/api/workspaces/"),
    async (route) => {
      const url = route.request().url()

      if (url.includes("/events")) {
        await route.abort()
        return
      }

      const wsData = {
        id: "default",
        name: "default",
        path: "/tmp/ws",
        repos: [],
        groups: [],
        agents: [],
        workspaces: [
          {
            id: "default",
            name: "default",
            path: "/tmp/ws",
            active: true,
            repo_count: 0,
            is_default: true,
          },
        ],
        workspace_order: ["default"],
        default_workspace: "default",
      }

      if (url.includes("/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        })
      } else if (url.includes("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: {
              total_issues: 0,
              open_issues: 0,
              in_progress_issues: 0,
              closed_issues: 0,
              blocked_issues: 0,
              deferred_issues: 0,
              ready_issues: 0,
              tombstone_issues: 0,
              pinned_issues: 0,
              epics_eligible_for_closure: 0,
              average_lead_time_hours: 0,
            },
          }),
        })
      } else if (url.includes("/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        })
      } else if (url.includes("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        })
      } else if (url.includes("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        })
      } else if (url.includes("/terminal/sessions")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        })
      } else {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: wsData }),
        })
      }
    },
  )

  // Monitor endpoints
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    })
  })
}

test.describe("App", () => {
  test.beforeEach(async ({ page }) => {
    await setupAppMocks(page)
  })

  test("homepage loads successfully", async ({ page }) => {
    const response = await page.goto("/")
    expect(response?.status()).toBe(200)
  })

  test("has correct page title", async ({ page }) => {
    await page.goto("/")
    await expect(page).toHaveTitle("Loom")
  })

  test("displays main heading", async ({ page }) => {
    await page.goto("/")
    await expect(page.locator("h1")).toBeVisible({ timeout: 10000 })
  })

  test("displays connection status", async ({ page }) => {
    await page.goto("/")
    // The app now shows a connection status indicator in the header
    // Use data-state attribute to find ConnectionStatus specifically (dnd-kit adds other status elements)
    await expect(page.locator('[data-state]').first()).toBeVisible({
      timeout: 10000,
    })
  })

  test("has no console errors on load", async ({ page }) => {
    const consoleErrors: string[] = []
    page.on("console", (msg) => {
      if (msg.type() === "error") {
        const text = msg.text()
        // Ignore network errors from unmocked endpoints (e.g. monitor polling)
        if (text.includes("net::ERR_FAILED") || text.includes("Failed to load resource")) {
          return
        }
        consoleErrors.push(text)
      }
    })

    await page.goto("/")
    await expect(page.locator("h1")).toBeVisible({ timeout: 10000 })

    expect(consoleErrors).toHaveLength(0)
  })

  test("page renders within acceptable time", async ({ page }) => {
    const startTime = Date.now()
    await page.goto("/")
    await page.waitForSelector("h1", { timeout: 10000 })
    const loadTime = Date.now() - startTime

    expect(loadTime).toBeLessThan(5000)
  })
})
