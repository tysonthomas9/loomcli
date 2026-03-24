import { test, expect } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
  threeTabOpts,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for terminal keyboard shortcuts.
 * Covers Ctrl+F (search), Escape (close search / navigate back),
 * tab bar keyboard navigation (Arrow keys, Home, End),
 * and the session name prompt interaction via new-tab button.
 */

test.describe("Terminal keyboard shortcuts", () => {
  test.describe("Search shortcuts", () => {
    test("Ctrl+F opens search bar", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
      await expect(page.getByTestId("terminal-search-input")).toBeFocused();
    });

    test("Ctrl+F toggles search bar closed when open", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
      await page.keyboard.press("Control+f");
      await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
    });

    test("Escape closes search bar when open", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("Enter in search bar triggers find next", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await page.getByTestId("terminal-search-input").fill("test");
      await page.getByTestId("terminal-search-input").press("Enter");
      await expect(page.getByTestId("terminal-search-input")).toBeFocused();
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
    });

    test("Shift+Enter in search bar triggers find previous", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await page.getByTestId("terminal-search-input").fill("query");
      await page.getByTestId("terminal-search-input").press("Shift+Enter");
      await expect(page.getByTestId("terminal-search-input")).toBeFocused();
    });
  });

  test.describe("Tab bar keyboard navigation", () => {
    test("ArrowRight in tab list navigates to next tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-1").focus();
      await page.keyboard.press("ArrowRight");
      await expect(page.getByTestId("terminal-tab-session-2")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("ArrowLeft wraps from first to last tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-1").focus();
      await page.keyboard.press("ArrowLeft");
      await expect(page.getByTestId("terminal-tab-session-3")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("ArrowRight wraps from last to first tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-3").click();
      await page.getByTestId("terminal-tab-session-3").focus();
      await page.keyboard.press("ArrowRight");
      await expect(page.getByTestId("terminal-tab-session-1")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("Home key moves to first tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-3").click();
      await page.getByTestId("terminal-tab-session-3").focus();
      await page.keyboard.press("Home");
      await expect(page.getByTestId("terminal-tab-session-1")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("End key moves to last tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-1").focus();
      await page.keyboard.press("End");
      await expect(page.getByTestId("terminal-tab-session-3")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });
  });

  test.describe("Tab creation and closing shortcuts", () => {
    test("new tab button opens session name prompt", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toBeVisible();
    });

    test("session name prompt Escape closes it", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "true");
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("closing a tab with close button works", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-close-session-2").click();
      await expect(
        page.getByTestId("terminal-tab-session-2"),
      ).not.toBeVisible();
      await expect(page.getByTestId("terminal-tab-session-1")).toBeVisible();
      await expect(page.getByTestId("terminal-tab-session-3")).toBeVisible();
    });

    test("closing active tab switches to adjacent", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-close-session-1").click();
      await expect(page.getByTestId("terminal-tab-session-2")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("cannot close last remaining tab", async ({ page }) => {
      await setupTerminalMocks(page, {
        sessions: [{ name: "solo", label: "Solo", created: 1 }],
        tabMetadata: [
          {
            session_name: "solo",
            label: "Solo",
            notes: "",
            sort_order: 0,
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        ],
      });
      await navigateToTerminal(page);

      await expect(page.getByTestId("terminal-tab-close-solo")).toHaveCount(0);
    });
  });

  test.describe("Escape key behavior", () => {
    test("Escape does nothing when no overlays are open", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Escape");
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("Escape closes search bar before anything else", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(page.getByTestId("terminal-search-bar")).toHaveCount(0);
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });
  });
});
