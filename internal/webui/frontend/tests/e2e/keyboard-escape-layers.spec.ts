/**
 * E2E tests for the escape layer priority system.
 *
 * Validates that Escape dispatches to the highest-priority active layer,
 * layers close in correct order, and cleanup prevents ghost handlers.
 */

import { test, expect } from "../fixtures";
import { bootApp, createMockWorkspaces } from "../helpers";

test.describe("Keyboard: Escape layer priority", () => {
  test("? opens cheatsheet, Escape closes it", async ({ page, mockApi }) => {
    await bootApp(page, mockApi);

    await page.keyboard.press("?");
    const cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    await expect(cheatsheet).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(cheatsheet).not.toBeVisible();
  });

  test("Escape closes highest priority layer first: cheatsheet (45) before switcher (42)", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi, {
      multiWorkspace: true,
      workspaces: createMockWorkspaces(3),
    });

    const cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    const switcher = page.getByRole("dialog", { name: "Switch workspace" });

    // Open cheatsheet (layer 45)
    await page.keyboard.press("?");
    await expect(cheatsheet).toBeVisible();

    // Open workspace switcher (layer 42) via Ctrl+K (works even with cheatsheet open)
    await page.keyboard.press("ControlOrMeta+k");
    await expect(switcher).toBeVisible();
    await expect(cheatsheet).toBeVisible();

    // First Escape: closes cheatsheet (higher priority = 45)
    await page.keyboard.press("Escape");
    await expect(cheatsheet).not.toBeVisible();
    await expect(switcher).toBeVisible();

    // Second Escape: closes workspace switcher (42)
    await page.keyboard.press("Escape");
    await expect(switcher).not.toBeVisible();
  });

  test("Layer cleanup: no double-fire after closing", async ({ page, mockApi }) => {
    await bootApp(page, mockApi);

    const cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });

    // Open and close cheatsheet
    await page.keyboard.press("?");
    await expect(cheatsheet).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(cheatsheet).not.toBeVisible();

    // Press Escape again — should be a no-op, no errors
    await page.keyboard.press("Escape");

    // Verify app is still in a healthy state
    await expect(page.locator('[role="banner"]')).toBeVisible();
    await expect(cheatsheet).not.toBeVisible();
  });

  test("CreateIssueModal (layer 40) closes on Escape", async ({ page, mockApi }) => {
    await bootApp(page, mockApi);

    await page.getByTestId("new-issue-button").click();
    const modal = page.getByRole("dialog", { name: "Create Issue" });
    await expect(modal).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(modal).not.toBeVisible();
  });

  test("Cheatsheet (45) closes before CreateIssueModal (40) when both open", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    const cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    const modal = page.getByRole("dialog", { name: "Create Issue" });

    // Open cheatsheet first (layer 45)
    await page.keyboard.press("?");
    await expect(cheatsheet).toBeVisible();

    // Open the modal by dispatching click directly to the button DOM element
    // (the cheatsheet backdrop overlay intercepts normal clicks)
    await page.getByTestId("new-issue-button").dispatchEvent("click");
    await expect(modal).toBeVisible();
    await expect(cheatsheet).toBeVisible();

    // First Escape: closes cheatsheet (45 > 40)
    await page.keyboard.press("Escape");
    await expect(cheatsheet).not.toBeVisible();
    await expect(modal).toBeVisible();

    // Second Escape: closes modal (40)
    await page.keyboard.press("Escape");
    await expect(modal).not.toBeVisible();
  });
});
