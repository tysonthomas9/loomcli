import { test, expect } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for terminal split view feature.
 * Covers tab panel layout, full-height toggle, multi-tab interactions, and single-tab state.
 */

const twoSessions = [
  { name: "session-1", label: "Session 1", created: 1 },
  { name: "session-2", label: "Session 2", created: 2 },
];

const twoTabMeta = [
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
];

const twoTabOpts = { sessions: twoSessions, tabMetadata: twoTabMeta };

test.describe("Terminal split view", () => {
  test.describe("Tab panel layout", () => {
    test("active tab panel is visible, others are hidden", async ({
      page,
    }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      await expect(page.locator("#terminal-panel-session-1")).toBeVisible();
      await expect(page.locator("#terminal-panel-session-2")).toBeHidden();
    });

    test("switching tabs changes visible panel", async ({ page }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-session-2").click();
      await expect(page.locator("#terminal-panel-session-1")).toBeHidden();
      await expect(page.locator("#terminal-panel-session-2")).toBeVisible();
    });

    test("each tab panel has correct role and ARIA attributes", async ({
      page,
    }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      const panel = page.locator("#terminal-panel-session-1");
      await expect(panel).toHaveAttribute("role", "tabpanel");
      await expect(panel).toHaveAttribute(
        "aria-labelledby",
        "terminal-tab-session-1",
      );
    });
  });

  test.describe("Full height toggle", () => {
    test("full height toggle changes layout", async ({ page }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      const toggle = page.getByTestId("terminal-fullheight-toggle");
      await expect(toggle).toHaveAttribute("aria-pressed", "false");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "true");

      const container = page.getByTestId("terminal-view");
      const classes = await container.getAttribute("class");
      expect(classes).toBeTruthy();
    });

    test("toggling full height back to normal", async ({ page }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      const toggle = page.getByTestId("terminal-fullheight-toggle");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "true");
      await toggle.click();
      await expect(toggle).toHaveAttribute("aria-pressed", "false");
    });
  });

  test.describe("Multi-tab interaction with panels", () => {
    test("creating a new tab and switching shows correct panels", async ({
      page,
    }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await page.getByTestId("session-name-input").fill("new-tab");
      await page.getByTestId("session-name-confirm-button").click();

      await expect(page.getByTestId("terminal-tab-new-tab")).toBeVisible();
      await expect(page.locator("#terminal-panel-new-tab")).toBeVisible();
      await expect(page.locator("#terminal-panel-session-1")).toBeHidden();
    });

    test("closing a tab removes its panel", async ({ page }) => {
      await setupTerminalMocks(page, twoTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-tab-close-session-2").click();
      await expect(page.locator("#terminal-panel-session-2")).toHaveCount(0);
    });
  });

  test.describe("Single tab state", () => {
    test("single tab fills the terminal area", async ({ page }) => {
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

      const panel = page.locator("#terminal-panel-only-tab");
      await expect(panel).toBeVisible();
      await expect(panel).toHaveAttribute("role", "tabpanel");
    });
  });
});
