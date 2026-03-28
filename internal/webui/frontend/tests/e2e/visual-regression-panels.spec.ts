/**
 * Visual regression screenshots for panel components:
 * SplitDetailSummary.
 *
 * Tests the two-column (with design) vs single-column (without) layout
 * and priority badge styling.
 */

import { test, expect, waitForStableContent } from "../fixtures/screenshot";
import { splitDetailSummaryUrl } from "../helpers/fixture-routes";

test.describe("Visual Regression - SplitDetailSummary", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("with design two-column", async ({ screenshotPage: page }) => {
    await page.goto(
      splitDetailSummaryUrl({
        id: "vis-panel-1",
        title: "Implement new API endpoint",
        priority: 2,
        hasDesign: true,
        description: "Build a new REST endpoint for user management.",
        issueType: "feature",
        assignee: "ember",
      }),
    );
    await waitForStableContent(page);

    const designSection = page.getByTestId("design-section");
    await expect(designSection).toBeVisible();

    await expect(page).toHaveScreenshot("split-detail-with-design.png", {
      maxDiffPixels: 200,
    });
  });

  test("without design single-column", async ({ screenshotPage: page }) => {
    await page.goto(
      splitDetailSummaryUrl({
        id: "vis-panel-2",
        title: "Fix login regression",
        priority: 1,
        hasDesign: false,
        description: "Users are unable to login after the last deployment.",
        issueType: "bug",
      }),
    );
    await waitForStableContent(page);

    // Design section should not be present
    const designSection = page.getByTestId("design-section");
    await expect(designSection).not.toBeVisible();

    await expect(page).toHaveScreenshot("split-detail-no-design.png", {
      maxDiffPixels: 200,
    });
  });

  test("p0 priority badge", async ({ screenshotPage: page }) => {
    await page.goto(
      splitDetailSummaryUrl({
        id: "vis-panel-3",
        title: "Critical production outage",
        priority: 0,
        hasDesign: true,
        description: "Production database is down. Immediate response required.",
        issueType: "bug",
      }),
    );
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("split-detail-p0-badge.png", {
      maxDiffPixels: 200,
    });
  });
});
