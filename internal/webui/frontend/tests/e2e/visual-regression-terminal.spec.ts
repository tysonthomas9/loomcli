/**
 * Visual regression screenshots for terminal-related components:
 * SessionNamePrompt, WelcomeBanner, and HelpPopover.
 *
 * These components have unit tests but CSS modules are mocked there,
 * so visual regression with real CSS is needed to catch layout regressions.
 */

import { test, expect, waitForStableContent } from "../fixtures/screenshot";
import {
  sessionNamePromptUrl,
  welcomeBannerUrl,
  helpPopoverUrl,
} from "../helpers/fixture-routes";

// --- SessionNamePrompt ---------------------------------------------------

test.describe("Visual Regression - SessionNamePrompt", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("closed state", async ({ screenshotPage: page }) => {
    await page.goto(sessionNamePromptUrl({ state: "closed" }));
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("session-prompt-closed.png");
  });

  test("open empty", async ({ screenshotPage: page }) => {
    await page.goto(sessionNamePromptUrl({ state: "open" }));
    await waitForStableContent(page);

    // Verify modal is visible
    const modal = page.getByTestId("session-name-prompt-modal");
    await expect(modal).toBeVisible();

    await expect(page).toHaveScreenshot("session-prompt-open-empty.png");
  });

  test("open valid input", async ({ screenshotPage: page }) => {
    await page.goto(sessionNamePromptUrl({ state: "open" }));
    await waitForStableContent(page);

    const input = page.getByTestId("session-name-input");
    await input.fill("auth-redesign");
    await waitForStableContent(page);

    await expect(page).toHaveScreenshot("session-prompt-open-valid.png");
  });

  test("open invalid chars", async ({ screenshotPage: page }) => {
    await page.goto(sessionNamePromptUrl({ state: "open" }));
    await waitForStableContent(page);

    const input = page.getByTestId("session-name-input");
    await input.fill("invalid name!");
    await waitForStableContent(page);

    // Verify error message appears
    const error = page.getByTestId("session-name-error");
    await expect(error).toBeVisible();

    await expect(page).toHaveScreenshot("session-prompt-open-invalid.png");
  });

  test("open duplicate name", async ({ screenshotPage: page }) => {
    await page.goto(
      sessionNamePromptUrl({
        state: "open",
        existingNames: ["my-session", "other"],
      }),
    );
    await waitForStableContent(page);

    const input = page.getByTestId("session-name-input");
    await input.fill("my-session");
    await waitForStableContent(page);

    // Verify duplicate error message
    const error = page.getByTestId("session-name-error");
    await expect(error).toBeVisible();

    await expect(page).toHaveScreenshot("session-prompt-open-duplicate.png");
  });
});

// --- WelcomeBanner -------------------------------------------------------

test.describe("Visual Regression - WelcomeBanner", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("claude backend", async ({ screenshotPage: page }) => {
    await page.goto(welcomeBannerUrl({ backend: "claude" }));
    await waitForStableContent(page);

    const banner = page.getByTestId("welcome-banner");
    await expect(banner).toBeVisible();

    await expect(page).toHaveScreenshot("welcome-banner-claude.png");
  });

  test("codex backend", async ({ screenshotPage: page }) => {
    await page.goto(welcomeBannerUrl({ backend: "codex" }));
    await waitForStableContent(page);

    const banner = page.getByTestId("welcome-banner");
    await expect(banner).toBeVisible();

    await expect(page).toHaveScreenshot("welcome-banner-codex.png");
  });

  test("unknown fallback", async ({ screenshotPage: page }) => {
    await page.goto(welcomeBannerUrl({ backend: "unknown" }));
    await waitForStableContent(page);

    const banner = page.getByTestId("welcome-banner");
    await expect(banner).toBeVisible();

    await expect(page).toHaveScreenshot("welcome-banner-unknown.png");
  });
});

// --- HelpPopover ---------------------------------------------------------

test.describe("Visual Regression - HelpPopover", () => {
  test.use({ viewport: { width: 1280, height: 720 } });

  test("open", async ({ screenshotPage: page }) => {
    await page.goto(helpPopoverUrl());
    await waitForStableContent(page);

    const popover = page.getByTestId("terminal-help-popover");
    await expect(popover).toBeVisible();

    await expect(page).toHaveScreenshot("help-popover-open.png");
  });
});
