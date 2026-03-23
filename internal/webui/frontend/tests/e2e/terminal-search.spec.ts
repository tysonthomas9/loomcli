import { test, expect } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for terminal search overlay.
 * Covers opening/closing search bar, typing, navigation buttons,
 * keyboard shortcuts (Enter, Shift+Enter, Escape), and auto-focus.
 *
 * The SearchBar is a pure DOM overlay; xterm.js canvas content is not tested.
 */

async function openSearch(page: import("@playwright/test").Page) {
  await page.keyboard.press("Control+f");
  await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
}

test.describe("Terminal search overlay", () => {
  test.beforeEach(async ({ page }) => {
    await setupTerminalMocks(page);
    await navigateToTerminal(page);
  });

  test("search bar is hidden by default", async ({ page }) => {
    await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
  });

  test("Ctrl+F opens search bar", async ({ page }) => {
    await page.keyboard.press("Control+f");
    await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
  });

  test("Ctrl+F toggles search bar closed", async ({ page }) => {
    await openSearch(page);
    await page.keyboard.press("Control+f");
    await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
  });

  test("Escape closes search bar", async ({ page }) => {
    await openSearch(page);
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
  });

  test("search input auto-focuses on open", async ({ page }) => {
    await openSearch(page);
    await expect(page.getByTestId("terminal-search-input")).toBeFocused();
  });

  test("typing in search input updates value", async ({ page }) => {
    await openSearch(page);
    await page.getByTestId("terminal-search-input").fill("hello");
    await expect(page.getByTestId("terminal-search-input")).toHaveValue(
      "hello",
    );
  });
});

test.describe("Terminal search navigation", () => {
  test.beforeEach(async ({ page }) => {
    await setupTerminalMocks(page);
    await navigateToTerminal(page);
    await openSearch(page);
  });

  test("previous match button is clickable", async ({ page }) => {
    await page.getByTestId("terminal-search-input").fill("test");
    await page.getByRole("button", { name: "Previous match" }).click();
    await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
  });

  test("next match button is clickable", async ({ page }) => {
    await page.getByTestId("terminal-search-input").fill("test");
    await page.getByRole("button", { name: "Next match" }).click();
    await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
  });

  test("Enter triggers find next", async ({ page }) => {
    await page.getByTestId("terminal-search-input").fill("test");
    await page.getByTestId("terminal-search-input").press("Enter");
    await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
    await expect(page.getByTestId("terminal-search-input")).toBeFocused();
  });

  test("Shift+Enter triggers find previous", async ({ page }) => {
    await page.getByTestId("terminal-search-input").fill("test");
    await page.getByTestId("terminal-search-input").press("Shift+Enter");
    await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
    await expect(page.getByTestId("terminal-search-input")).toBeFocused();
  });

  test("close button closes search", async ({ page }) => {
    await page.getByRole("button", { name: "Close search" }).click();
    await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
  });
});

test.describe("Terminal search keyboard integration", () => {
  test.beforeEach(async ({ page }) => {
    await setupTerminalMocks(page);
    await navigateToTerminal(page);
  });

  test("search does not steal Escape from app when closed", async ({
    page,
  }) => {
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
  });

  test("search input has correct placeholder", async ({ page }) => {
    await openSearch(page);
    await expect(page.getByTestId("terminal-search-input")).toHaveAttribute(
      "placeholder",
      "Find...",
    );
  });

  test("search input has correct aria-label", async ({ page }) => {
    await openSearch(page);
    await expect(page.getByTestId("terminal-search-input")).toHaveAttribute(
      "aria-label",
      "Search terminal",
    );
  });
});
