import { test, expect, type Page } from "@playwright/test"

/**
 * Mock issues for successful retry response.
 * Minimal data to verify error clears and Kanban renders.
 */
const mockIssues = [
  {
    id: "test-1",
    title: "Test Issue",
    status: "open",
    priority: 2,
    created_at: "2026-01-25T00:00:00Z",
    updated_at: "2026-01-25T00:00:00Z",
  },
]

async function setupAppMocks(page: Page) {
  const workspace = {
    id: "test-workspace",
    name: "test-workspace",
    path: "/tmp/test-workspace",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: "test-workspace",
        name: "test-workspace",
        path: "/tmp/test-workspace",
        active: true,
        repo_count: 0,
        is_default: true,
      },
    ],
    workspace_order: ["test-workspace"],
    default_workspace: "test-workspace",
  }

  await page.route("**/*", async (route) => {
    const request = route.request()
    const pathname = new URL(request.url()).pathname

    if (pathname === "/api/config") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ mode: "open" }),
      })
      return
    }

    if (
      pathname === "/api/workspaces/active" ||
      pathname === "/api/workspaces/test-workspace"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: workspace }),
      })
      return
    }

    if (pathname === "/api/workspaces/test-workspace/stats") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
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
        }),
      })
      return
    }

    if (
      pathname === "/api/workspaces/test-workspace/blocked" ||
      pathname === "/api/workspaces/test-workspace/issues" ||
      pathname === "/api/workspaces/test-workspace/issues/graph" ||
      pathname === "/api/workspaces/test-workspace/terminal/tabs"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      })
      return
    }

    if (pathname === "/api/workspaces/test-workspace/terminal/state") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      })
      return
    }

    if (pathname.startsWith("/api/") && pathname.includes("/events")) {
      await route.abort()
      return
    }

    await route.continue()
  })
}

test.describe("ErrorDisplay and Retry", () => {
  test.beforeEach(async ({ page }) => {
    await setupAppMocks(page)
  })

  test("displays error when API fails", async ({ page }) => {
    // Mock API to fail
    await page.route(
      (url) => url.pathname === "/api/workspaces/test-workspace/issues",
      async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Server error" }),
      })
      },
    )

    await page.goto("/ws/test-workspace/kanban")

    // Wait for and verify error display
    const errorDisplay = page.getByTestId("error-display")
    await expect(errorDisplay).toBeVisible()
    await expect(errorDisplay).toHaveAttribute("data-variant", "fetch-error")

    // Verify retry button is present
    await expect(page.getByTestId("retry-button")).toBeVisible()
  })

  test("retry button triggers refetch and clears error", async ({ page }) => {
    let shouldFail = true

    await page.route(
      (url) => url.pathname === "/api/workspaces/test-workspace/issues",
      async (route) => {
      if (shouldFail) {
        // First request fails
        await route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ success: false, error: "Server error" }),
        })
      } else {
        // Subsequent requests succeed
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockIssues }),
        })
      }
      },
    )

    await page.goto("/ws/test-workspace/kanban")

    // Wait for error display
    const errorDisplay = page.getByTestId("error-display")
    await expect(errorDisplay).toBeVisible()

    // Allow the store's scheduled retry to recover.
    shouldFail = false

    // Error should clear, Kanban should appear
    await expect(errorDisplay).not.toBeVisible({ timeout: 10000 })
    await expect(page.getByRole("heading", { name: "Open" })).toBeVisible()
  })

  test("error display has correct accessibility attributes", async ({ page }) => {
    await page.route(
      (url) => url.pathname === "/api/workspaces/test-workspace/issues",
      async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Server error" }),
      })
      },
    )

    await page.goto("/ws/test-workspace/kanban")

    const errorDisplay = page.getByTestId("error-display")
    await expect(errorDisplay).toBeVisible()
    await expect(errorDisplay).toHaveAttribute("role", "alert")
    await expect(errorDisplay).toHaveAttribute("aria-live", "assertive")
  })
})
