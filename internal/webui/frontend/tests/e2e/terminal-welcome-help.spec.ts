import { test, expect } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for terminal welcome state and help/action elements.
 * Covers the initial terminal view rendering, loading state,
 * tab bar action buttons, session prompt interactions,
 * and the NavRail terminal button.
 */

test.describe("Terminal welcome and help", () => {
  test.describe("Terminal view initial state", () => {
    test("terminal view renders with tab bar on navigation", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect(page.getByTestId("terminal-view")).toBeVisible();
      await expect(page.getByTestId("terminal-tab-bar")).toBeVisible();
    });

    test("terminal view shows tab with default session", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect(page.getByTestId("terminal-tab-session-1")).toBeVisible();
      await expect(
        page.getByTestId("terminal-tab-label-session-1"),
      ).toHaveText("Session 1");
    });

    test("first tab is active by default", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect(page.getByTestId("terminal-tab-session-1")).toHaveAttribute(
        "aria-selected",
        "true",
      );
    });

    test("terminal instance container is rendered for active tab", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect(page.locator("#terminal-panel-session-1")).toBeVisible();
    });
  });

  test.describe("Tab bar action buttons", () => {
    test("new tab button is visible with correct aria-label", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const newTabBtn = page.getByTestId("terminal-new-tab-button");
      await expect(newTabBtn).toBeVisible();
      await expect(newTabBtn).toHaveAttribute(
        "aria-label",
        "New terminal tab",
      );
    });

    test("full-height toggle button is visible", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const toggle = page.getByTestId("terminal-fullheight-toggle");
      await expect(toggle).toBeVisible();
      await expect(toggle).toHaveAttribute("aria-label", "Toggle full height");
    });

    test("full-height toggle starts unpressed", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect(
        page.getByTestId("terminal-fullheight-toggle"),
      ).toHaveAttribute("aria-pressed", "false");
    });
  });

  test.describe("Session name prompt from new tab", () => {
    test("clicking new tab opens session name prompt", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();

      const overlay = page.getByTestId("session-name-prompt-overlay");
      await expect(overlay).toHaveAttribute("aria-hidden", "false");
      await expect(
        page.getByTestId("session-name-prompt-modal"),
      ).toBeVisible();
    });

    test("session prompt shows New Terminal Session title", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(page.getByText("New Terminal Session")).toBeVisible();
    });

    test("canceling prompt keeps terminal view intact", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await page.getByTestId("session-name-cancel-button").click();

      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "true");
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("creating a new session adds a tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await page.getByTestId("session-name-input").fill("my-new-session");
      await page.getByTestId("session-name-confirm-button").click();

      await expect(
        page.getByTestId("terminal-tab-my-new-session"),
      ).toBeVisible();
      await expect(
        page.getByTestId("terminal-tab-my-new-session"),
      ).toHaveAttribute("aria-selected", "true");
    });
  });

  test.describe("NavRail terminal button", () => {
    test("terminal button in nav rail has correct aria-label", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await page.goto("/");

      await expect(
        page.locator('button[aria-label="Terminal"]'),
      ).toBeVisible();
    });

    test("clicking terminal nav button shows terminal view", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await page.goto("/");

      await page.locator('button[aria-label="Terminal"]').click();
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("terminal nav button has active state when terminal is shown", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await page.goto("/");

      await page.locator('button[aria-label="Terminal"]').click();
      await expect(
        page.locator('button[aria-label="Terminal"]'),
      ).toHaveAttribute("data-active", "true");
    });
  });
});
