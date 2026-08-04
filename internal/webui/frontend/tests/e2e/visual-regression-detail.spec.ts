/**
 * Visual regression screenshots for IssueDetailPanel.
 * Tests feature, P0 critical, bug, and closed issue states.
 */

import { test, expect, waitForStableContent } from "../fixtures/screenshot";
import { issueDetailPanelUrl } from "../helpers/fixture-routes";

test.describe("Visual Regression - IssueDetailPanel", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("feature issue with all fields", async ({ screenshotPage: page }) => {
    await page.goto(
      issueDetailPanelUrl({
        id: "vis-det-1",
        title: "Implement auth flow",
        status: "open",
        priority: 2,
        issueType: "feature",
        description: "Build a new auth system",
      })
    );
    await waitForStableContent(page);
    await expect(page.getByText("Implement auth flow")).toBeVisible();

    await expect(page).toHaveScreenshot("issue-detail-feature.png");
  });

  test("P0 critical in progress", async ({ screenshotPage: page }) => {
    await page.goto(
      issueDetailPanelUrl({
        id: "vis-det-2",
        title: "Critical production outage",
        status: "in_progress",
        priority: 0,
        issueType: "bug",
        description: "Database connections exhausted",
      })
    );
    await waitForStableContent(page);
    await expect(page.getByText("Critical production outage")).toBeVisible();

    await expect(page).toHaveScreenshot("issue-detail-p0-critical.png");
  });

  test("bug type", async ({ screenshotPage: page }) => {
    await page.goto(
      issueDetailPanelUrl({
        id: "vis-det-3",
        title: "Login page crashes on Safari",
        status: "open",
        priority: 1,
        issueType: "bug",
        description: "TypeError thrown during form validation",
      })
    );
    await waitForStableContent(page);
    await expect(page.getByText("Login page crashes on Safari")).toBeVisible();

    await expect(page).toHaveScreenshot("issue-detail-bug.png");
  });

  test("closed status", async ({ screenshotPage: page }) => {
    await page.goto(
      issueDetailPanelUrl({
        id: "vis-det-4",
        title: "Add unit tests for auth module",
        status: "closed",
        priority: 3,
        issueType: "task",
        description: "Coverage increased to 80%",
      })
    );
    await waitForStableContent(page);
    await expect(page.getByText("Add unit tests for auth module")).toBeVisible();

    await expect(page).toHaveScreenshot("issue-detail-closed.png");
  });
});
