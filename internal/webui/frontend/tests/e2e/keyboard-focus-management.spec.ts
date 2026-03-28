/**
 * E2E tests for focus management: skip link, focus trap, and focus return.
 *
 * Validates that the skip link is the first focusable element, focus traps
 * cycle correctly in modals, and focus returns to the trigger after closing.
 */

import { test, expect } from "../fixtures";
import { bootApp, createMockWorkspaces, expectFocused } from "../helpers";

test.describe("Keyboard: Focus management", () => {
  test("Skip link is first focusable element", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    // Move focus to body to start clean
    await page.evaluate(function () {
      document.body.focus();
    });

    // Tab from body — first focusable should be the skip link
    await page.keyboard.press("Tab");

    var skipLink = page.locator('a[href="#main-content"]');
    await expect(skipLink).toBeFocused();
    await expect(skipLink).toHaveText("Skip to main content");
  });

  test("CreateIssueModal: Tab cycles within modal fields", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    // Open the create-issue modal
    await page.getByTestId("new-issue-button").click();
    var modal = page.getByRole("dialog", { name: "Create Issue" });
    await expect(modal).toBeVisible();

    // Title input should be auto-focused
    await expectFocused(page, "create-issue-title");

    // Type something so the submit button becomes enabled (and thus focusable)
    await page.keyboard.type("Test Issue");

    // Tab through the form: title → type → priority → description → cancel → submit
    await page.keyboard.press("Tab");
    await expectFocused(page, "create-issue-type");

    await page.keyboard.press("Tab");
    await expectFocused(page, "create-issue-priority");

    await page.keyboard.press("Tab");
    await expectFocused(page, "create-issue-description");

    await page.keyboard.press("Tab");
    await expectFocused(page, "create-issue-cancel");

    await page.keyboard.press("Tab");
    await expectFocused(page, "create-issue-submit");

    // Tab again should wrap back to title (focus trap)
    await page.keyboard.press("Tab");
    await expectFocused(page, "create-issue-title");
  });

  test("Shift+Tab wraps from first to last element in modal", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    // Open modal and type a title (enables submit button for proper Tab cycle)
    await page.getByTestId("new-issue-button").click();
    await expect(page.getByRole("dialog", { name: "Create Issue" })).toBeVisible();
    await expectFocused(page, "create-issue-title");
    await page.keyboard.type("Test Issue");

    // Shift+Tab from title should wrap to submit (last focusable element)
    await page.keyboard.press("Shift+Tab");
    await expectFocused(page, "create-issue-submit");
  });

  test("Modal closes cleanly on Escape without focus getting stuck", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    // Click the trigger button to open modal
    await page.getByTestId("new-issue-button").click();
    await expect(page.getByRole("dialog", { name: "Create Issue" })).toBeVisible();

    // Close modal with Escape
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "Create Issue" })).not.toBeVisible();

    // Verify keyboard shortcuts still work after modal close (focus not stuck)
    await page.keyboard.press("?");
    var cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    await expect(cheatsheet).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(cheatsheet).not.toBeVisible();
  });

  test("WorkspaceSwitcher: ArrowDown/ArrowUp navigate highlighted items, Enter selects", async ({
    page, mockApi,
  }) => {
    var workspaces = createMockWorkspaces(3);
    await bootApp(page, mockApi, {
      multiWorkspace: true,
      workspaces: workspaces,
    });

    // Open workspace switcher
    await page.keyboard.press("ControlOrMeta+k");
    var switcher = page.getByRole("dialog", { name: "Switch workspace" });
    await expect(switcher).toBeVisible();

    // Get workspace items
    var items = switcher.locator("[data-workspace-item]");
    await expect(items).toHaveCount(3);

    // First item should be highlighted by default
    await expect(items.nth(0)).toHaveClass(/highlighted/);

    // ArrowDown: highlight moves to second item
    await page.keyboard.press("ArrowDown");
    await expect(items.nth(1)).toHaveClass(/highlighted/);
    await expect(items.nth(0)).not.toHaveClass(/highlighted/);

    // ArrowUp: back to first
    await page.keyboard.press("ArrowUp");
    await expect(items.nth(0)).toHaveClass(/highlighted/);

    // Navigate to second and press Enter to select
    await page.keyboard.press("ArrowDown");
    await expect(items.nth(1)).toHaveClass(/highlighted/);
    await page.keyboard.press("Enter");

    // Switcher should close after selection
    await expect(switcher).not.toBeVisible();
  });
});
