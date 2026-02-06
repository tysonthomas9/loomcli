import { test, expect } from "@playwright/test"

/**
 * E2E tests for ErrorBoundary component.
 *
 * Tests use the /test/error-boundary fixture route that can trigger
 * render errors on demand via URL parameters.
 *
 * URL Parameters:
 * - throw=true: Causes the fixture to throw during render
 * - errorMessage=...: Custom error message to throw
 */

// Test fixture URLs
const FIXTURE_BASE = "/test/error-boundary"
const THROWING_URL = `${FIXTURE_BASE}?throw=true`
const NORMAL_URL = FIXTURE_BASE
const CUSTOM_ERROR_URL = `${FIXTURE_BASE}?throw=true&errorMessage=Custom%20test%20error`

test.describe("ErrorBoundary", () => {
  test.beforeEach(async ({ page }) => {
  })

  test.describe("Catch", () => {
    test("catches component render errors without crashing app", async ({ page }) => {
      await page.goto(THROWING_URL)

      // ErrorDisplay should be visible instead of a crash
      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()

      // Should NOT see default React error in dev mode (the boundary catches it)
      // The page should not show the React error overlay in production
      const body = page.locator("body")
      await expect(body).toBeVisible()
    })

    test("catches errors thrown during initial render", async ({ page }) => {
      await page.goto(THROWING_URL)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()
      await expect(errorDisplay).toHaveAttribute("data-variant", "unknown-error")
    })
  })

  test.describe("Fallback", () => {
    test("shows ErrorDisplay as fallback UI", async ({ page }) => {
      await page.goto(THROWING_URL)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()

      // Verify it's the "unknown-error" variant (used for render errors)
      await expect(errorDisplay).toHaveAttribute("data-variant", "unknown-error")
    })

    test("fallback has correct accessibility attributes", async ({ page }) => {
      await page.goto(THROWING_URL)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()
      await expect(errorDisplay).toHaveAttribute("role", "alert")
      await expect(errorDisplay).toHaveAttribute("aria-live", "assertive")
    })

    test("shows 'Something went wrong' title", async ({ page }) => {
      await page.goto(THROWING_URL)

      // The unknown-error variant defaults to "Something went wrong"
      await expect(page.getByRole("heading", { name: "Something went wrong" })).toBeVisible()
    })
  })

  test.describe("Message", () => {
    test("displays error message in technical details", async ({ page }) => {
      await page.goto(CUSTOM_ERROR_URL)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()

      // ErrorDisplay with showDetails shows error message in expandable section
      const details = page.locator("details")
      await expect(details).toBeVisible()
      await details.click() // Expand details
      await expect(page.locator("pre")).toContainText("Custom test error")
    })

    test("shows default description for unknown errors", async ({ page }) => {
      await page.goto(THROWING_URL)

      // The unknown-error variant description
      await expect(page.getByText("An error occurred while rendering this component")).toBeVisible()
    })
  })

  test.describe("Retry", () => {
    test("shows retry button in fallback", async ({ page }) => {
      await page.goto(THROWING_URL)

      const retryButton = page.getByTestId("retry-button")
      await expect(retryButton).toBeVisible()
      await expect(retryButton).toHaveText("Try again")
    })

    test("retry re-renders component and recovers when error is fixed", async ({ page }) => {
      // First load: throw error
      await page.goto(THROWING_URL)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()

      // Update URL to not throw (simulating "error fixed")
      await page.evaluate(() => {
        window.history.replaceState({}, "", "/test/error-boundary")
      })

      // Click retry
      await page.getByTestId("retry-button").click()

      // Error should be cleared, normal content visible
      await expect(errorDisplay).not.toBeVisible()
      await expect(page.getByTestId("error-boundary-content")).toBeVisible()
    })

    test("retry shows error again if error persists", async ({ page }) => {
      await page.goto(THROWING_URL)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()

      // Click retry (error will persist since URL still has throw=true)
      await page.getByTestId("retry-button").click()

      // Error should still be visible (the component re-throws)
      await expect(errorDisplay).toBeVisible()
    })
  })

  test.describe("Isolation", () => {
    test("root ErrorBoundary catches all unhandled render errors", async ({ page }) => {
      await page.goto(THROWING_URL)

      // The root ErrorBoundary should catch the error
      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()

      // App should NOT have crashed completely (document still accessible)
      await expect(page.locator("body")).toBeVisible()
    })

    test("normal app renders correctly when no error occurs", async ({ page }) => {
      // Navigate to fixture without throwing
      await page.goto(NORMAL_URL)

      // Normal content should render
      await expect(page.getByTestId("error-boundary-content")).toBeVisible()
      await expect(page.getByText("Error Boundary Test Fixture")).toBeVisible()
    })
  })

  test.describe("Edge Cases", () => {
    test("handles error with empty message gracefully", async ({ page }) => {
      // Fixture throws error with empty message
      await page.goto(`${FIXTURE_BASE}?throw=true&errorMessage=`)

      const errorDisplay = page.getByTestId("error-display")
      await expect(errorDisplay).toBeVisible()
      // Should not crash, should show default fallback
      await expect(page.getByRole("heading", { name: "Something went wrong" })).toBeVisible()
    })

    test("preserves error boundary functionality across multiple errors", async ({ page }) => {
      // First error
      await page.goto(THROWING_URL)
      await expect(page.getByTestId("error-display")).toBeVisible()

      // Fix error and retry
      await page.evaluate(() => {
        window.history.replaceState({}, "", "/test/error-boundary")
      })
      await page.getByTestId("retry-button").click()
      await expect(page.getByTestId("error-boundary-content")).toBeVisible()

      // Trigger new error
      await page.evaluate(() => {
        window.history.replaceState({}, "", "/test/error-boundary?throw=true")
      })
      await page.goto(THROWING_URL)
      // Error boundary should catch the new error
      await expect(page.getByTestId("error-display")).toBeVisible()
    })
  })
})
