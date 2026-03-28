/**
 * E2E tests for global keyboard shortcuts.
 *
 * Validates view switching via number keys, ? cheatsheet toggle,
 * Cmd/Ctrl+K search/workspace switcher, and input suppression behavior.
 *
 * Note: app boots to ?view=settings to ensure KeyboardShortcutProvider
 * is active (empty kanban state doesn't render the provider).
 * View detection uses NavRail buttons with data-active attribute.
 */

import { test, expect } from "../fixtures";
import { bootApp, createMockWorkspaces } from "../helpers";

test.describe("Keyboard: Global shortcuts", () => {
  test("Number keys switch views (3=terminal, 0=settings)", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    // We start on settings. Verify settings is active.
    var settingsBtn = page.locator('button[aria-label="Settings"]');
    await expect(settingsBtn).toHaveAttribute("data-active", "true");

    // Press 3 to switch to terminal (always has provider, won't lose shortcuts)
    await page.keyboard.press("3");
    var terminalBtn = page.locator('button[aria-label="Terminal"]');
    await expect(terminalBtn).toHaveAttribute("data-active", "true");

    // Press 0 to return to settings
    await page.keyboard.press("0");
    await expect(settingsBtn).toHaveAttribute("data-active", "true");
  });

  test("? opens cheatsheet when no input focused", async ({ page, mockApi }) => {
    await bootApp(page, mockApi);

    await page.keyboard.press("?");
    var cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    await expect(cheatsheet).toBeVisible();
  });

  test("? suppressed when search input focused", async ({ page, mockApi }) => {
    await bootApp(page, mockApi);

    // Focus the search input via Ctrl+K (single-repo mode = focus search)
    await page.keyboard.press("ControlOrMeta+k");
    var searchInput = page.getByTestId("search-input-field");
    await expect(searchInput).toBeFocused();

    // Type ? — should appear in input, not open cheatsheet
    await page.keyboard.type("?");

    var cheatsheet = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    await expect(cheatsheet).not.toBeVisible();
    await expect(searchInput).toHaveValue("?");
  });

  test("Cmd+K focuses search in single-repo mode", async ({ page, mockApi }) => {
    await bootApp(page, mockApi); // single-repo (repos=[])

    await page.keyboard.press("ControlOrMeta+k");

    var searchInput = page.getByTestId("search-input-field");
    await expect(searchInput).toBeFocused();
  });

  test("Cmd+K opens WorkspaceSwitcher in multi-repo mode", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi, {
      multiWorkspace: true,
      workspaces: createMockWorkspaces(3),
    });

    await page.keyboard.press("ControlOrMeta+k");

    var switcher = page.getByRole("dialog", { name: "Switch workspace" });
    await expect(switcher).toBeVisible();
  });

  test("Cmd+K works even when input is focused (bypass suppression)", async ({
    page, mockApi,
  }) => {
    await bootApp(page, mockApi);

    // Focus the search input first
    await page.keyboard.press("ControlOrMeta+k");
    var searchInput = page.getByTestId("search-input-field");
    await expect(searchInput).toBeFocused();

    // Type something in the input
    await page.keyboard.type("test");
    await expect(searchInput).toHaveValue("test");

    // Press Ctrl+K again — should still work (bypass input suppression)
    await page.keyboard.press("ControlOrMeta+k");
    await expect(searchInput).toBeFocused();
  });

  test("Number keys suppressed in text inputs", async ({ page, mockApi }) => {
    await bootApp(page, mockApi);

    // We start on settings. Verify settings button is active.
    var settingsBtn = page.locator('button[aria-label="Settings"]');
    await expect(settingsBtn).toHaveAttribute("data-active", "true");

    // Focus the search input
    await page.keyboard.press("ControlOrMeta+k");
    var searchInput = page.getByTestId("search-input-field");
    await expect(searchInput).toBeFocused();

    // Type "2" — should appear in input, NOT switch to table view
    await page.keyboard.type("2");
    await expect(searchInput).toHaveValue("2");

    // Settings should still be active
    await expect(settingsBtn).toHaveAttribute("data-active", "true");
  });
});
