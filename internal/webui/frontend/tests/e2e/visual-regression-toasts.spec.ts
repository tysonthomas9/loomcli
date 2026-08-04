/**
 * Visual regression screenshots for ToastContainer.
 * Tests success, error, warning-with-undo, and stacked toast states.
 */

import { test, expect, waitForStableContent } from "../fixtures/screenshot";

test.describe("Visual Regression - ToastContainer", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("success toast", async ({ screenshotPage: page }) => {
    await page.goto("/test/toast");
    await page.getByTestId("trigger-success-toast").click();
    await page.waitForTimeout(300);
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("toast-success.png");
  });

  test("error toast", async ({ screenshotPage: page }) => {
    await page.goto("/test/toast");
    await page.getByTestId("trigger-error-toast").click();
    await page.waitForTimeout(300);
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("toast-error.png");
  });

  test("warning toast with undo", async ({ screenshotPage: page }) => {
    await page.goto("/test/toast");
    await page.getByTestId("trigger-undo-toast").click();
    await page.waitForTimeout(300);
    await waitForStableContent(page);

    await expect(
      page.getByRole("button", { name: "Undo action" })
    ).toBeVisible();

    await expect(page).toHaveScreenshot("toast-warning-undo.png");
  });

  test("multiple stacked toasts", async ({ screenshotPage: page }) => {
    await page.goto("/test/toast");

    // Trigger 3 different toast types to avoid deduplication
    await page.getByTestId("trigger-success-toast").click();
    await page.getByTestId("trigger-error-toast").click();
    await page.getByTestId("trigger-warning-toast").click();
    await page.waitForTimeout(300);

    await expect(page.getByTestId("toast-count")).toHaveText("3");
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("toast-stacked.png");
  });
});
