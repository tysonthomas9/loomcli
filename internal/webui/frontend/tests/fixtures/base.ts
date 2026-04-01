/**
 * Custom Playwright test fixture that extends @playwright/test.
 * Provides pre-configured fixtures for mocking APIs, SSE, and navigation.
 */

import { test as base, expect, type Page } from "@playwright/test";
import { createApiMockHandler, type ApiMockHandler } from "../helpers/api-mock";
import { createSSEMock, type SSEMockController } from "../helpers/sse-mock";

/**
 * Extended test fixtures available to all E2E tests.
 */
export interface TestFixtures {
  /** Helper object for setting up API route mocks. */
  mockApi: ApiMockHandler;
  /** Helper for mocking SSE streams. */
  mockSSE: SSEMockController;
  /**
   * Pre-navigated page with auth initialized and SSE/API defaults mocked.
   * Use this when you want a fully set up app page.
   */
  appPage: Page;
}

/**
 * Extended test with custom fixtures.
 * Import this instead of @playwright/test in E2E test files:
 *
 *   import { test, expect } from '../fixtures';
 */
export const test = base.extend<TestFixtures>({
  // Provide an ApiMockHandler scoped to the current page
  mockApi: async ({ page }, use) => {
    const handler = createApiMockHandler(page);
    await use(handler);
  },

  // Provide an SSE mock controller scoped to the current page
  mockSSE: async ({ page }, use) => {
    const controller = createSSEMock(page);
    await use(controller);
    // Clean up: close any open SSE streams
    controller.close();
  },

  // Provide a fully set up page: config + auth mocked, SSE intercepted, navigated to app
  appPage: async ({ page, mockApi, mockSSE }, use) => {
    // Mock /api/config first — fetchAppConfig() runs before anything else on boot
    await mockApi.mockConfig({ mode: "open" });
    // Set up auth mock before navigation (initAuth fires on app mount)
    await mockApi.mockAuth();
    // Set up SSE intercept so the app doesn't hang waiting for a real server
    await mockSSE.connect();
    // Intercept /api/events/token before the SSE mock's broader glob catches it
    await mockApi.mockSseToken(null);
    // Navigate to the app
    await page.goto("/");
    await page.waitForLoadState("domcontentloaded");
    // Send the SSE connected event
    mockSSE.sendConnected();
    await use(page);
  },
});

export { expect };
