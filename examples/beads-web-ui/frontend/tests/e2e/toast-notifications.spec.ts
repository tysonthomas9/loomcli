import { test, expect } from "@playwright/test"

/**
 * E2E tests for Toast notification system.
 *
 * Tests use the /test/toast fixture route that provides buttons
 * to trigger each toast type for testing.
 *
 * URL: /test/toast
 */

// Test fixture URL
const TOAST_FIXTURE = "/test/toast"

test.describe("Toast Notifications", () => {
  test.beforeEach(async ({ page }) => {
  })

  test.describe("Display", () => {
    test("toast appears when triggered", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Click button to trigger a success toast
      await page.getByTestId("trigger-success-toast").click()

      // Toast should be visible (uses data-testid="toast-success")
      const toast = page.getByTestId("toast-success")
      await expect(toast).toBeVisible()
    })

    test("toast shows correct message", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      const toast = page.getByTestId("toast-success")
      await expect(toast).toContainText("Success message")
    })

    test("toast count updates when toasts are added", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Initial count should be 0
      await expect(page.getByTestId("toast-count")).toHaveText("0")

      // Trigger a toast
      await page.getByTestId("trigger-success-toast").click()

      // Count should be 1
      await expect(page.getByTestId("toast-count")).toHaveText("1")

      // Trigger another toast
      await page.getByTestId("trigger-error-toast").click()

      // Count should be 2
      await expect(page.getByTestId("toast-count")).toHaveText("2")
    })
  })

  test.describe("Position", () => {
    test("toast container appears in bottom-right corner", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      const container = page.getByTestId("toast-container")
      await expect(container).toBeVisible()

      // Verify position via computed styles
      const position = await container.evaluate((el) => {
        const style = window.getComputedStyle(el)
        return {
          position: style.position,
          bottom: style.bottom,
          right: style.right,
        }
      })

      expect(position.position).toBe("fixed")
      // Bottom and right should be small values (spacing from edge)
      expect(parseInt(position.bottom)).toBeLessThan(50)
      expect(parseInt(position.right)).toBeLessThan(50)
    })
  })

  test.describe("Auto-dismiss", () => {
    test("toast auto-dismisses after 5 seconds", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      const toast = page.getByTestId("toast-success")
      await expect(toast).toBeVisible()

      // Wait slightly more than 5 seconds
      await page.waitForTimeout(5500)

      // Toast should be gone
      await expect(toast).not.toBeVisible()
    })

    test("toast with custom duration dismisses at correct time", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Trigger toast with 2 second duration
      await page.getByTestId("trigger-short-toast").click()

      const toast = page.getByTestId("toast-info")
      await expect(toast).toBeVisible()

      // Should still be visible at 1.5 seconds
      await page.waitForTimeout(1500)
      await expect(toast).toBeVisible()

      // Should be gone after 2.5 seconds total
      await page.waitForTimeout(1000)
      await expect(toast).not.toBeVisible()
    })

    test("toast with duration 0 does not auto-dismiss", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-persistent-toast").click()

      const toast = page.getByTestId("toast-info")
      await expect(toast).toBeVisible()

      // Wait longer than default duration
      await page.waitForTimeout(6000)

      // Toast should still be visible
      await expect(toast).toBeVisible()
    })
  })

  test.describe("Manual Dismiss", () => {
    test("clicking dismiss button closes toast", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      const toast = page.getByTestId("toast-success")
      await expect(toast).toBeVisible()

      // Click dismiss button (has aria-label="Dismiss notification")
      await page.getByRole("button", { name: "Dismiss notification" }).click()

      // Toast should be gone
      await expect(toast).not.toBeVisible()
    })

    test("dismiss button has correct accessibility label", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      // Find dismiss button within toast
      const dismissBtn = page.getByTestId("toast-success").getByRole("button")
      await expect(dismissBtn).toHaveAttribute("aria-label", "Dismiss notification")
    })

    test("dismiss all button clears all toasts", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Trigger multiple toasts
      await page.getByTestId("trigger-success-toast").click()
      await page.getByTestId("trigger-error-toast").click()
      await page.getByTestId("trigger-warning-toast").click()

      // Verify multiple toasts exist
      await expect(page.getByTestId("toast-count")).toHaveText("3")

      // Click dismiss all
      await page.getByTestId("dismiss-all").click()

      // All toasts should be gone
      await expect(page.getByTestId("toast-count")).toHaveText("0")
      await expect(page.getByTestId("toast-container").locator("[role=alert]")).toHaveCount(0)
    })
  })

  test.describe("Stacking", () => {
    test("multiple toasts stack vertically", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Trigger multiple toasts
      await page.getByTestId("trigger-success-toast").click()
      await page.getByTestId("trigger-error-toast").click()
      await page.getByTestId("trigger-warning-toast").click()

      // Should see 3 toasts
      await expect(page.getByTestId("toast-count")).toHaveText("3")
      await expect(page.getByTestId("toast-success")).toBeVisible()
      await expect(page.getByTestId("toast-error")).toBeVisible()
      await expect(page.getByTestId("toast-warning")).toBeVisible()
    })

    test("maximum 5 toasts visible (oldest removed when exceeded)", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Trigger 6 toasts
      for (let i = 0; i < 6; i++) {
        await page.getByTestId("trigger-info-toast").click()
        await page.waitForTimeout(100) // Small delay between triggers
      }

      // Should only see 5 toasts (maxToasts default is 5)
      await expect(page.getByTestId("toast-count")).toHaveText("5")
    })

    test("toasts stack newest at bottom", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()
      await page.waitForTimeout(100)
      await page.getByTestId("trigger-error-toast").click()

      const successToast = page.getByTestId("toast-success")
      const errorToast = page.getByTestId("toast-error")

      // Get positions
      const successBox = await successToast.boundingBox()
      const errorBox = await errorToast.boundingBox()

      // Both should exist
      expect(successBox).not.toBeNull()
      expect(errorBox).not.toBeNull()

      // Second toast (error) should be below first (success) since stacking from bottom
      expect(errorBox!.y).toBeGreaterThan(successBox!.y)
    })
  })

  test.describe("Type Styling", () => {
    test("each toast type renders with correct test ID", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      // Trigger each type
      await page.getByTestId("trigger-success-toast").click()
      await expect(page.getByTestId("toast-success")).toBeVisible()

      await page.getByTestId("trigger-error-toast").click()
      await expect(page.getByTestId("toast-error")).toBeVisible()

      await page.getByTestId("trigger-warning-toast").click()
      await expect(page.getByTestId("toast-warning")).toBeVisible()

      await page.getByTestId("trigger-info-toast").click()
      await expect(page.getByTestId("toast-info")).toBeVisible()
    })

    test("error toast has assertive aria-live for screen readers", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-error-toast").click()

      const toast = page.getByTestId("toast-error")
      await expect(toast).toHaveAttribute("aria-live", "assertive")
    })

    test("non-error toasts have polite aria-live", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()
      await expect(page.getByTestId("toast-success")).toHaveAttribute("aria-live", "polite")

      await page.getByTestId("trigger-warning-toast").click()
      await expect(page.getByTestId("toast-warning")).toHaveAttribute("aria-live", "polite")

      await page.getByTestId("trigger-info-toast").click()
      await expect(page.getByTestId("toast-info")).toHaveAttribute("aria-live", "polite")
    })
  })

  test.describe("Accessibility", () => {
    test("toast has role=alert", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      const toast = page.getByTestId("toast-success")
      await expect(toast).toHaveAttribute("role", "alert")
    })

    test("toast container has accessible label", async ({ page }) => {
      await page.goto(TOAST_FIXTURE)

      await page.getByTestId("trigger-success-toast").click()

      const container = page.getByTestId("toast-container")
      await expect(container).toHaveAttribute("aria-label", "Notifications")
    })
  })

  // Note: Integration test for "error toast appears on drag-drop failure"
  // is covered by kanban.spec.ts (lines 343-458) to avoid duplication
})
