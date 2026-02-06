import { test, expect } from "@playwright/test"

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

test.describe("ErrorDisplay and Retry", () => {
  test.beforeEach(async ({ page }) => {
  })

  test("displays error when API fails", async ({ page }) => {
    // Mock API to fail
    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Server error" }),
      })
    })

    await page.goto("/")

    // Wait for and verify error display
    const errorDisplay = page.getByTestId("error-display")
    await expect(errorDisplay).toBeVisible()
    await expect(errorDisplay).toHaveAttribute("data-variant", "fetch-error")

    // Verify retry button is present
    await expect(page.getByTestId("retry-button")).toBeVisible()
  })

  test("retry button triggers refetch and clears error", async ({ page }) => {
    let requestCount = 0

    await page.route("**/api/ready", async (route) => {
      requestCount++
      if (requestCount === 1) {
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
    })

    await page.goto("/")

    // Wait for error display
    const errorDisplay = page.getByTestId("error-display")
    await expect(errorDisplay).toBeVisible()

    // Click retry
    await page.getByTestId("retry-button").click()

    // Error should clear, Kanban should appear
    await expect(errorDisplay).not.toBeVisible()
    await expect(page.locator('section[data-status="ready"]')).toBeVisible()
  })

  test("error display has correct accessibility attributes", async ({ page }) => {
    await page.route("**/api/ready", async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ success: false, error: "Server error" }),
      })
    })

    await page.goto("/")

    const errorDisplay = page.getByTestId("error-display")
    await expect(errorDisplay).toBeVisible()
    await expect(errorDisplay).toHaveAttribute("role", "alert")
    await expect(errorDisplay).toHaveAttribute("aria-live", "assertive")
  })
})
