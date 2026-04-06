/**
 * Screenshot test fixture.
 * Pre-configures page with auth/SSE/loom mocks that every visual regression test needs.
 */

import { test as base, expect, type Page } from "@playwright/test";

interface ScreenshotFixtures {
  screenshotPage: Page;
}

/**
 * Match API endpoint URLs only (not Vite module paths like /src/api/events.ts).
 * API endpoints are served at /api/... directly (no /src/ prefix).
 */
function isApiUrl(url: URL, prefix: string): boolean {
  return url.pathname === prefix || url.pathname.startsWith(prefix + "/") || url.pathname.startsWith(prefix + "?");
}

export const test = base.extend<ScreenshotFixtures>({
  screenshotPage: async ({ page }, use) => {
    // Mock app config endpoint (boot process requires this before rendering)
    await page.route(
      (url) => url.pathname === "/api/config",
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ mode: "open" }),
        });
      },
    );

    // Mock auth token endpoint
    await page.route(
      (url) => url.pathname === "/api/auth/token",
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "test-token-screenshot" }),
        });
      },
    );

    // Abort SSE to prevent networkidle timeout
    // Use URL predicate to avoid matching Vite module /src/api/events.ts
    await page.route(
      (url) => isApiUrl(url, "/api/events"),
      async (route) => {
        await route.abort();
      },
    );

    // Abort monitor server requests
    await page.route(
      (url) => url.pathname.startsWith("/api/monitor/") || url.pathname === "/health",
      async (route) => {
        await route.abort();
      },
    );

    await use(page);
  },
});

/**
 * Wait for content to stabilize before taking a screenshot.
 */
export async function waitForStableContent(page: Page): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(100);
}

export { expect };
