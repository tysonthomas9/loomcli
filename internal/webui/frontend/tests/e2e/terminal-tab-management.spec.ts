import { test, expect } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for terminal tab management.
 * Covers tab rendering, creation, closing, switching, renaming, and keyboard navigation.
 */

const threeSessions = [
  { name: "session-1", label: "Session 1", created: 1 },
  { name: "session-2", label: "Session 2", created: 2 },
  { name: "session-3", label: "Session 3", created: 3 },
];

const threeTabMeta = [
  {
    session_name: "session-1",
    label: "Session 1",
    notes: "",
    sort_order: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    session_name: "session-2",
    label: "Session 2",
    notes: "",
    sort_order: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    session_name: "session-3",
    label: "Session 3",
    notes: "",
    sort_order: 2,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

const threeTabOpts = { sessions: threeSessions, tabMetadata: threeTabMeta };

test.describe("Terminal Tab Management", () => {
  test.describe("Tab rendering", () => {
    test("renders tab bar with initial tabs", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await expect(page.getByTestId("terminal-tab-bar")).toBeVisible();
      const tabList = page.locator('[role="tablist"]');
      await expect(tabList).toBeVisible();
      await expect(tabList).toHaveAttribute("aria-label", "Terminal tabs");
    });

    test("shows correct labels for each tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await expect(
        page.getByTestId("terminal-tab-label-session-1"),
      ).toHaveText("Session 1");
      await expect(
        page.getByTestId("terminal-tab-label-session-2"),
      ).toHaveText("Session 2");
      await expect(
        page.getByTestId("terminal-tab-label-session-3"),
      ).toHaveText("Session 3");
    });

    test("marks active tab with aria-selected=true", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await expect(page.getByTestId("terminal-tab-session-1")).toHaveAttribute(
        "aria-selected",
        "true",
      );
      await expect(page.getByTestId("terminal-tab-session-2")).toHaveAttribute(
        "aria-selected",
        "false",
      );
    });

    test("shows connection state dots with data-status attribute", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      const statusDot = page.getByTestId("terminal-tab-status-session-1");
      await expect(statusDot).toBeVisible();
      const status = await statusDot.getAttribute("data-status");
      expect(["disconnected", "connecting"]).toContain(status);
    });
  });

  test.describe("Tab creation", () => {
    test("clicking + button triggers new tab flow", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toBeVisible();
    });

    test("new tab appears after confirming session name", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await page.getByTestId("session-name-input").fill("my-new-tab");
      await page.getByTestId("session-name-confirm-button").click();

      await expect(page.getByTestId("terminal-tab-my-new-tab")).toBeVisible();
      await expect(
        page.getByTestId("terminal-tab-my-new-tab"),
      ).toHaveAttribute("aria-selected", "true");
    });
  });

  test.describe("Tab closing", () => {
    test("clicking close button removes the tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-close-session-2").click();
      await expect(
        page.getByTestId("terminal-tab-session-2"),
      ).not.toBeVisible();
    });

    test("cannot close the last remaining tab", async ({ page }) => {
      await setupTerminalMocks(page, {
        sessions: [{ name: "only-tab", label: "Only Tab", created: 1 }],
        tabMetadata: [
          {
            session_name: "only-tab",
            label: "Only Tab",
            notes: "",
            sort_order: 0,
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        ],
      });
      await navigateToTerminal(page);

      await expect(
        page.getByTestId("terminal-tab-close-only-tab"),
      ).toHaveCount(0);
    });

    test("after closing active tab, adjacent tab becomes active", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-close-session-1").click();
      await expect(page.getByTestId("terminal-tab-session-2")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });
  });

  test.describe("Tab switching", () => {
    test("clicking an inactive tab makes it active", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-2").click();
      await expect(page.getByTestId("terminal-tab-session-2")).toHaveAttribute(
        "aria-selected",
        "true",
      );
      await expect(page.getByTestId("terminal-tab-session-1")).toHaveAttribute(
        "aria-selected",
        "false",
      );
    });
  });

  test.describe("Tab renaming", () => {
    // TerminalView does not currently pass onTabRename to TerminalTabBar,
    // so double-click is a no-op and edit mode cannot be entered.

    test("double-clicking tab label does not enter edit mode when rename not wired", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-label-session-1").dblclick();
      await expect(
        page.getByTestId("terminal-tab-rename-input-session-1"),
      ).toHaveCount(0);
      await expect(
        page.getByTestId("terminal-tab-label-session-1"),
      ).toBeVisible();
    });

    test("tab label remains after double-click attempt", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-label-session-1").dblclick();
      await expect(
        page.getByTestId("terminal-tab-label-session-1"),
      ).toHaveText("Session 1");
    });
  });

  test.describe("Keyboard navigation", () => {
    test("ArrowRight moves focus to next tab", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-1").focus();
      await page.keyboard.press("ArrowRight");
      await expect(page.getByTestId("terminal-tab-session-2")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("ArrowLeft moves focus to previous tab (wraps)", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-1").focus();
      await page.keyboard.press("ArrowLeft");
      await expect(page.getByTestId("terminal-tab-session-3")).toHaveAttribute(
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

  test.describe("Full height toggle", () => {
    test("full-height toggle button is visible and toggles aria-pressed", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      const toggle = page.getByTestId("terminal-fullheight-toggle");
      await expect(toggle).toBeVisible();
      await expect(toggle).toHaveAttribute("aria-pressed", "false");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "true");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "false");
    });
  });

  test.describe("Terminal panels", () => {
    test("active tab panel is visible while others are hidden", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await expect(page.locator("#terminal-panel-session-1")).toBeVisible();
      await expect(page.locator("#terminal-panel-session-2")).toBeHidden();
    });

    test("switching tabs changes which panel is visible", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-2").click();
      await expect(page.locator("#terminal-panel-session-1")).toBeHidden();
      await expect(page.locator("#terminal-panel-session-2")).toBeVisible();
    });
  });
});
