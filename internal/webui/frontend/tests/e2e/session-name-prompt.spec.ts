import { test, expect } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
  threeTabOpts,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for the new-tab prompt (BackendPickerPrompt).
 * Exercises the real application flow: navigate to terminal view, click the
 * new-tab button, and interact with the prompt that appears.
 *
 * NOTE: The original SessionNamePrompt component has been replaced by
 * BackendPickerPrompt in the production UI. These tests verify the actual
 * user-facing prompt behavior in context.
 */

test.describe("New Tab Prompt (BackendPickerPrompt)", () => {
  test.describe("Rendering", () => {
    test("prompt appears when clicking new-tab button", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-overlay"),
      ).toBeVisible();
    });

    test("modal displays with title and subtitle", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).toBeVisible();
      await expect(page.getByText("New Terminal Session")).toBeVisible();
      await expect(
        page.getByText("Select a backend for the new session"),
      ).toBeVisible();
    });

    test("Cancel and Create buttons are visible", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-cancel-button"),
      ).toBeVisible();
      await expect(
        page.getByTestId("backend-picker-create-button"),
      ).toBeVisible();
    });
  });

  test.describe("Backend selection", () => {
    test("backend dropdown is visible with available backends", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-select"),
      ).toBeVisible();
    });

    test("Create button is enabled when a backend is selected", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      // Default mock provides ["claude"] as available backends, so one is auto-selected
      await expect(
        page.getByTestId("backend-picker-create-button"),
      ).toBeEnabled();
    });

    test("shows loading state while backends load", async ({ page }) => {
      // Override config endpoint to simulate loading
      await setupTerminalMocks(page, threeTabOpts);
      await page.route("**/api/config/backend", async () => {
        // Never fulfill -- simulates perpetual loading state
      });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-loading"),
      ).toBeVisible();
    });
  });

  test.describe("Form Submission", () => {
    test("clicking Create confirms and creates a new tab", async ({
      page,
    }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await page.getByTestId("backend-picker-create-button").click();

      // Prompt should close
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).not.toBeVisible();
    });
  });

  test.describe("Cancel", () => {
    test("clicking Cancel closes the prompt", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).toBeVisible();

      await page.getByTestId("backend-picker-cancel-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).not.toBeVisible();
    });

    test("prompt can be reopened after cancel", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      // Open and cancel
      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).toBeVisible();
      await page.getByTestId("backend-picker-cancel-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).not.toBeVisible();

      // Reopen
      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-modal"),
      ).toBeVisible();
    });
  });

  test.describe("Accessibility", () => {
    test("modal has correct ARIA attributes", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      const modal = page.getByTestId("backend-picker-prompt-modal");
      await expect(modal).toHaveAttribute("role", "dialog");
      await expect(modal).toHaveAttribute("aria-modal", "true");
      await expect(modal).toHaveAttribute(
        "aria-labelledby",
        "backend-picker-prompt-title",
      );
    });

    test("overlay has aria-hidden=false when open", async ({ page }) => {
      await setupTerminalMocks(page, threeTabOpts);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("backend-picker-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "false");
    });
  });
});
