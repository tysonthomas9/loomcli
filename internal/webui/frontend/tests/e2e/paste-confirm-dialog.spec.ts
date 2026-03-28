import { test, expect } from "@playwright/test"
import { pasteConfirmUrl } from "../helpers/fixture-routes"

/**
 * E2E tests for PasteConfirmDialog component.
 *
 * Tests use the /test/paste-confirm fixture route that provides
 * buttons to open the dialog with different text payloads.
 *
 * URL: /test/paste-confirm
 */

const FIXTURE_URL = pasteConfirmUrl()

test.describe("PasteConfirmDialog", () => {
  test.describe("Display", () => {
    test("dialog appears when opened with multi-line text", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()
    })

    test("title shows correct line count", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      await expect(page.getByText("Paste 2 lines?")).toBeVisible()
    })

    test("subtitle describes the paste action", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      await expect(
        page.getByText("You are about to paste multi-line text into the terminal.")
      ).toBeVisible()
    })

    test("preview area shows text content in monospace pre element", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const pre = page.getByRole("alertdialog").locator("pre")
      await expect(pre).toBeVisible()
      await expect(pre).toContainText("line1")
      await expect(pre).toContainText("line2")
    })
  })

  test.describe("Truncation", () => {
    test("10-line text: all lines visible, no truncation message", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-10-lines").click()

      await expect(page.getByText("Paste 10 lines?")).toBeVisible()

      const pre = page.getByRole("alertdialog").locator("pre")
      await expect(pre).toContainText("line1")
      await expect(pre).toContainText("line10")

      await expect(page.getByText("... and")).not.toBeVisible()
    })

    test("15-line text: only first 10 lines visible, shows truncation message", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-15-lines").click()

      await expect(page.getByText("Paste 15 lines?")).toBeVisible()

      const pre = page.getByRole("alertdialog").locator("pre")
      await expect(pre).toContainText("line1")
      await expect(pre).toContainText("line10")
      await expect(pre).not.toContainText("line11")

      await expect(page.getByText("... and 5 more lines")).toBeVisible()
    })

    test("25-line text: title and truncation correct", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-25-lines").click()

      await expect(page.getByText("Paste 25 lines?")).toBeVisible()
      await expect(page.getByText("... and 15 more lines")).toBeVisible()
    })

    test("11-line text: singular 'line' in truncation message", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-11-lines").click()

      await expect(page.getByText("Paste 11 lines?")).toBeVisible()
      await expect(page.getByText("... and 1 more line")).toBeVisible()
      // Should not say "lines" (plural) for exactly 1 remaining
      await expect(page.getByText("... and 1 more lines")).not.toBeVisible()
    })
  })

  test.describe("Button Interactions", () => {
    test("clicking Paste button confirms and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      await expect(page.getByRole("alertdialog")).toBeVisible()

      await page.getByRole("button", { name: "Paste" }).click()

      await expect(page.getByRole("alertdialog")).not.toBeVisible()
      await expect(page.getByTestId("confirm-count")).toHaveText("1")
    })

    test("clicking Cancel button cancels and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      await expect(page.getByRole("alertdialog")).toBeVisible()

      await page.getByRole("button", { name: "Cancel" }).click()

      await expect(page.getByRole("alertdialog")).not.toBeVisible()
      await expect(page.getByTestId("cancel-count")).toHaveText("1")
    })

    test("clicking overlay cancels and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()

      // Click the overlay (outside the dialog) — the overlay is the parent of alertdialog
      // Use position to click outside the dialog bounds
      const dialogBox = await dialog.boundingBox()
      expect(dialogBox).not.toBeNull()

      // Click far to the left of the dialog (on the overlay)
      await page.mouse.click(5, 5)

      await expect(page.getByRole("alertdialog")).not.toBeVisible()
      await expect(page.getByTestId("cancel-count")).toHaveText("1")
    })
  })

  test.describe("Keyboard Interactions", () => {
    test("Enter key confirms paste and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()

      await dialog.press("Enter")

      await expect(dialog).not.toBeVisible()
      await expect(page.getByTestId("confirm-count")).toHaveText("1")
    })

    test("Escape key cancels paste and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()

      await dialog.press("Escape")

      await expect(dialog).not.toBeVisible()
      await expect(page.getByTestId("cancel-count")).toHaveText("1")
    })
  })

  test.describe("Accessibility", () => {
    test("dialog has role=alertdialog and aria-modal=true", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toBeVisible()
      await expect(dialog).toHaveAttribute("aria-modal", "true")
    })

    test("aria-labelledby points to title, aria-describedby points to description", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const dialog = page.getByRole("alertdialog")
      await expect(dialog).toHaveAttribute("aria-labelledby", "paste-dialog-title")
      await expect(dialog).toHaveAttribute("aria-describedby", "paste-dialog-desc")

      // Verify the referenced elements exist and have correct content
      await expect(page.locator("#paste-dialog-title")).toContainText("Paste")
      await expect(page.locator("#paste-dialog-desc")).toContainText(
        "You are about to paste multi-line text into the terminal."
      )
    })

    test("both buttons have type=button", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const pasteBtn = page.getByRole("button", { name: "Paste" })
      const cancelBtn = page.getByRole("button", { name: "Cancel" })

      await expect(pasteBtn).toHaveAttribute("type", "button")
      await expect(cancelBtn).toHaveAttribute("type", "button")
    })

    test("Paste button receives auto-focus when dialog opens", async ({ page }) => {
      await page.goto(FIXTURE_URL)
      await page.getByTestId("open-2-lines").click()

      const pasteBtn = page.getByRole("button", { name: "Paste" })
      await expect(pasteBtn).toBeFocused()
    })
  })
})
