/**
 * Visual regression screenshots for PasteConfirmDialog.
 * Tests short, medium, and long paste content in the confirmation dialog.
 */

import { test, expect, waitForStableContent } from "../fixtures/screenshot";
import { pasteConfirmUrl } from "../helpers/fixture-routes";

test.describe("Visual Regression - PasteConfirmDialog", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("short paste 2 lines", async ({ screenshotPage: page }) => {
    await page.goto(pasteConfirmUrl());
    await page.getByTestId("open-2-lines").click();
    await page.locator('[role="alertdialog"]').waitFor();
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("paste-confirm-short.png");
  });

  test("medium paste 10 lines", async ({ screenshotPage: page }) => {
    await page.goto(pasteConfirmUrl());
    await page.getByTestId("open-10-lines").click();
    await page.locator('[role="alertdialog"]').waitFor();
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("paste-confirm-medium.png");
  });

  test("long paste 25 lines", async ({ screenshotPage: page }) => {
    await page.goto(pasteConfirmUrl());
    await page.getByTestId("open-25-lines").click();
    await page.locator('[role="alertdialog"]').waitFor();
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("paste-confirm-long.png");
  });
});
