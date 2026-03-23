import { test, expect } from "@playwright/test";

/**
 * E2E tests for PasteConfirmDialog component.
 * Uses the /test/paste-confirm fixture route which renders a paste confirmation
 * dialog with controls to open it with different text payloads.
 *
 * Tests validate dialog rendering, text preview/truncation, button interactions,
 * keyboard shortcuts, overlay dismissal, and accessibility attributes.
 */

const FIXTURE_URL = "/test/paste-confirm";

test.describe("PasteConfirmDialog", () => {
  test.describe("Display", () => {
    test("dialog appears when opened with multi-line text", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      await expect(page.getByRole("alertdialog")).toBeVisible();
    });

    test("title shows correct line count for 2 lines", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      await expect(page.getByText("Paste 2 lines?")).toBeVisible();
    });

    test("title shows correct line count for 25 lines", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-25-lines").click();

      await expect(page.getByText("Paste 25 lines?")).toBeVisible();
    });

    test("preview area shows the text content", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      const dialog = page.getByRole("alertdialog");
      await expect(dialog.locator("pre")).toBeVisible();
      await expect(dialog.locator("pre")).toContainText("line1");
      await expect(dialog.locator("pre")).toContainText("line2");
    });
  });

  test.describe("Truncation", () => {
    test("10-line text shows all lines, no truncation message", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-10-lines").click();

      const dialog = page.getByRole("alertdialog");
      await expect(dialog.locator("pre")).toContainText("line10");
      await expect(dialog.getByText("... and")).toHaveCount(0);
    });

    test("15-line text shows truncation message", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-15-lines").click();

      const dialog = page.getByRole("alertdialog");
      await expect(dialog.getByText("... and 5 more lines")).toBeVisible();
    });

    test("25-line text shows truncation with 15 more lines", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-25-lines").click();

      await expect(page.getByText("Paste 25 lines?")).toBeVisible();
      await expect(page.getByText("... and 15 more lines")).toBeVisible();
    });
  });

  test.describe("Button Interactions", () => {
    test("clicking Paste button confirms and closes dialog", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      await page.getByRole("button", { name: "Paste" }).click();

      await expect(page.getByTestId("confirm-count")).toHaveText("1");
      await expect(page.getByRole("alertdialog")).toHaveCount(0);
    });

    test("clicking Cancel button cancels and closes dialog", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      await page.getByRole("button", { name: "Cancel" }).click();

      await expect(page.getByTestId("cancel-count")).toHaveText("1");
      await expect(page.getByRole("alertdialog")).toHaveCount(0);
    });

    test("clicking overlay cancels and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      // Click the overlay background (outside the dialog content)
      const dialog = page.getByRole("alertdialog");
      await expect(dialog).toBeVisible();

      // Click at the very edge of the overlay
      await page.mouse.click(10, 10);

      await expect(page.getByTestId("cancel-count")).toHaveText("1");
    });
  });

  test.describe("Keyboard Interactions", () => {
    test("Enter key confirms paste and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      await page.getByRole("alertdialog").press("Enter");

      await expect(page.getByTestId("confirm-count")).toHaveText("1");
      await expect(page.getByRole("alertdialog")).toHaveCount(0);
    });

    test("Escape key cancels paste and closes dialog", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      await page.getByRole("alertdialog").press("Escape");

      await expect(page.getByTestId("cancel-count")).toHaveText("1");
      await expect(page.getByRole("alertdialog")).toHaveCount(0);
    });
  });

  test.describe("Accessibility", () => {
    test("dialog has role=alertdialog and aria-modal=true", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      const dialog = page.getByRole("alertdialog");
      await expect(dialog).toHaveAttribute("aria-modal", "true");
    });

    test("aria-labelledby points to title element", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      const dialog = page.getByRole("alertdialog");
      await expect(dialog).toHaveAttribute(
        "aria-labelledby",
        "paste-confirm-title",
      );
    });

    test("aria-describedby points to description element", async ({
      page,
    }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      const dialog = page.getByRole("alertdialog");
      await expect(dialog).toHaveAttribute(
        "aria-describedby",
        "paste-confirm-desc",
      );
    });

    test("both buttons have type=button", async ({ page }) => {
      await page.goto(FIXTURE_URL);
      await page.getByTestId("open-2-lines").click();

      const pasteBtn = page.getByRole("button", { name: "Paste" });
      const cancelBtn = page.getByRole("button", { name: "Cancel" });
      await expect(pasteBtn).toHaveAttribute("type", "button");
      await expect(cancelBtn).toHaveAttribute("type", "button");
    });
  });

  test.describe("Counter tracking", () => {
    test("confirm and cancel counters start at zero", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      await expect(page.getByTestId("confirm-count")).toHaveText("0");
      await expect(page.getByTestId("cancel-count")).toHaveText("0");
    });

    test("multiple opens and confirms increment counter", async ({ page }) => {
      await page.goto(FIXTURE_URL);

      // First confirm
      await page.getByTestId("open-2-lines").click();
      await page.getByRole("button", { name: "Paste" }).click();
      await expect(page.getByTestId("confirm-count")).toHaveText("1");

      // Second confirm
      await page.getByTestId("open-10-lines").click();
      await page.getByRole("button", { name: "Paste" }).click();
      await expect(page.getByTestId("confirm-count")).toHaveText("2");
    });
  });
});
