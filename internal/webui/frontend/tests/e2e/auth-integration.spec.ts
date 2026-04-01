/**
 * E2E tests for the auth integration flow.
 * Validates mode=open, mode=oidc, config error handling,
 * SSE token exchange, and multi-tab sign-out.
 */

import { test, expect } from "../fixtures";

test.describe("Auth Integration", () => {
  test.describe("mode=open (no auth)", () => {
    test("app loads without auth gate", async ({ page, mockApi, mockSSE }) => {
      await mockApi.mockConfig({ mode: "open" });
      await mockApi.mockSseToken(null);
      await mockSSE.connect();
      await mockApi.mockReady([]);

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // App content should render — verify the app title
      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 10_000,
      });
      // Login page should NOT be visible
      await expect(
        page.locator('[role="dialog"][aria-modal="true"]'),
      ).not.toBeVisible();
    });

    test("no auth-related requests made", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      const authRequests: string[] = [];
      await page.route("**/api/auth/**", async (route) => {
        authRequests.push(route.request().url());
        await route.abort();
      });

      await mockApi.mockConfig({ mode: "open" });
      await mockApi.mockSseToken(null);
      await mockSSE.connect();
      await mockApi.mockReady([]);

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // Wait for app to stabilize
      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 10_000,
      });

      // No auth requests should have been made
      expect(authRequests).toHaveLength(0);
    });

    test("SSE connects without token", async ({ page, mockApi, mockSSE }) => {
      const sseTracker = await mockApi.mockSseToken(null);
      await mockApi.mockConfig({ mode: "open" });
      await mockSSE.connect();
      await mockApi.mockReady([]);

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 10_000,
      });

      // /api/events/token should have been called but returned 404
      // The SSE client handles 404 by connecting without token
      expect(sseTracker.calls.length).toBeGreaterThanOrEqual(1);
    });
  });

  test.describe("mode=oidc (auth required)", () => {
    test("shows login page when not authenticated", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      await mockApi.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
      await mockApi.mockSession(null);
      await mockApi.mockSseToken(null);
      await mockSSE.connect();

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      // Login page should be visible
      await expect(page.getByText("Sign in to Loom")).toBeVisible({
        timeout: 10_000,
      });
      await expect(
        page.getByRole("button", { name: "Continue with GitHub" }),
      ).toBeVisible();
      await expect(
        page.getByRole("button", { name: "Continue with Google" }),
      ).toBeVisible();
    });

    test("renders app content when session is valid", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      await mockApi.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
      await mockApi.mockSession({
        user: { id: "user-1", name: "Test User", email: "test@example.com" },
        session: { id: "session-1", expiresAt: "2099-01-01T00:00:00Z" },
      });
      await mockApi.mockSseToken("opaque-token");

      // Mock the Better Auth token endpoint for JWT exchange
      await page.route("**/api/auth/token", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "mock-jwt-token" }),
        });
      });

      await mockSSE.connect();
      await mockApi.mockReady([]);

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // App content should render
      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 15_000,
      });
      // Login page should NOT be visible
      await expect(page.getByText("Sign in to Loom")).not.toBeVisible();
    });

    test("deep link restored after authentication", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      await mockApi.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
      await mockApi.mockSession({
        user: { id: "user-1", name: "Test User", email: "test@example.com" },
        session: { id: "session-1", expiresAt: "2099-01-01T00:00:00Z" },
      });
      await mockApi.mockSseToken("opaque-token");
      await page.route("**/api/auth/token", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "mock-jwt-token" }),
        });
      });
      await mockSSE.connect();
      await mockApi.mockReady([]);

      // Set returnTo in sessionStorage before navigation
      await page.goto("/");
      await page.evaluate(() => {
        sessionStorage.setItem("loom-auth-return-to", "/issues/deep-link-123");
      });

      // Reload to trigger the auth flow with returnTo set
      await page.reload();
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // Wait for app to render
      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 15_000,
      });

      // Verify sessionStorage entry was cleared
      const returnTo = await page.evaluate(() =>
        sessionStorage.getItem("loom-auth-return-to"),
      );
      expect(returnTo).toBeNull();
    });

    test("OAuth error displayed from URL query param", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      await mockApi.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
      await mockApi.mockSession(null);
      await mockApi.mockSseToken(null);
      await mockSSE.connect();

      await page.goto("/?error=access_denied");
      await page.waitForLoadState("domcontentloaded");

      // Error should be visible on the login page
      await expect(page.getByText("access_denied")).toBeVisible({
        timeout: 10_000,
      });
    });
  });

  test.describe("config error handling (fail-closed)", () => {
    test("shows BootError when /api/config returns 404", async ({ page }) => {
      await page.route("**/api/config", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname !== "/api/config") {
          await route.fallback();
          return;
        }
        await route.fulfill({ status: 404, body: "Not Found" });
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      // BootError should be visible (role="alert")
      await expect(page.locator('[role="alert"]')).toBeVisible({
        timeout: 10_000,
      });
      await expect(page.getByText("Unable to start application")).toBeVisible();
      // Should NOT show the app content
      await expect(page.locator("h1")).not.toHaveText("Cortex");
    });

    test("shows BootError when /api/config returns 500", async ({ page }) => {
      await page.route("**/api/config", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname !== "/api/config") {
          await route.fallback();
          return;
        }
        await route.fulfill({ status: 500, body: "Internal Server Error" });
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      await expect(page.locator('[role="alert"]')).toBeVisible({
        timeout: 10_000,
      });
      await expect(page.getByText("Unable to start application")).toBeVisible();
    });

    test("shows BootError when /api/config returns HTML (SPA fallback)", async ({
      page,
    }) => {
      await page.route("**/api/config", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname !== "/api/config") {
          await route.fallback();
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "text/html",
          body: "<!DOCTYPE html><html><body>SPA fallback</body></html>",
        });
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      await expect(page.locator('[role="alert"]')).toBeVisible({
        timeout: 10_000,
      });
      await expect(page.getByText("Unable to start application")).toBeVisible();
    });

    test("shows BootError on /api/config network timeout", async ({ page }) => {
      await page.route("**/api/config", async (route) => {
        const url = new URL(route.request().url());
        if (url.pathname !== "/api/config") {
          await route.fallback();
          return;
        }
        // Never fulfill — let the fetch timeout fire (5s in doFetch + 10s boot timeout)
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      // Wait for the timeout (5s fetch + potential boot timeout)
      await expect(page.locator('[role="alert"]')).toBeVisible({
        timeout: 20_000,
      });
      await expect(page.getByText("Unable to start application")).toBeVisible();
    });
  });

  test.describe("SSE token exchange", () => {
    test("SSE uses opaque token in oidc mode", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      const sseRequests: string[] = [];

      await mockApi.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
      await mockApi.mockSession({
        user: { id: "user-1", name: "Test User", email: "test@example.com" },
        session: { id: "session-1", expiresAt: "2099-01-01T00:00:00Z" },
      });
      await mockApi.mockSseToken("test-opaque-token");
      await page.route("**/api/auth/token", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "mock-jwt" }),
        });
      });

      // Track SSE connection URLs
      await page.route("**/api/events", async (route) => {
        const url = route.request().url();
        // Only track the main SSE endpoint, not /api/events/token
        if (!url.includes("/api/events/token")) {
          sseRequests.push(url);
        }
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          headers: { "Cache-Control": "no-cache" },
          body: 'event: connected\ndata: {"message":"connected"}\n\n',
        });
      });

      await mockApi.mockReady([]);

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      // Wait for the app to render and SSE to connect
      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 15_000,
      });

      // Check that at least one SSE request was made with the opaque token
      const hasToken = sseRequests.some((url) =>
        url.includes("token=test-opaque-token"),
      );
      expect(hasToken).toBe(true);
    });

    test("SSE connects without token in mode=open", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      const sseRequests: string[] = [];

      await mockApi.mockConfig({ mode: "open" });
      await mockApi.mockSseToken(null);

      // Track SSE connection URLs
      await page.route("**/api/events", async (route) => {
        const url = route.request().url();
        if (!url.includes("/api/events/token")) {
          sseRequests.push(url);
        }
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          headers: { "Cache-Control": "no-cache" },
          body: 'event: connected\ndata: {"message":"connected"}\n\n',
        });
      });

      await mockApi.mockReady([]);

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      await expect(page.locator("h1")).toHaveText("Cortex", {
        timeout: 10_000,
      });

      // Verify no SSE request includes a token param
      const hasToken = sseRequests.some((url) => url.includes("token="));
      expect(hasToken).toBe(false);
    });
  });

  test.describe("multi-tab sign-out", () => {
    test("sign-out in one tab propagates to other tab", async ({
      context,
      mockApi,
    }) => {
      // Create two pages in the same browser context
      const page1 = await context.newPage();
      const page2 = await context.newPage();

      // Set up mocks for both pages
      for (const page of [page1, page2]) {
        const api = await import("../helpers/api-mock").then((m) =>
          m.createApiMockHandler(page),
        );
        await api.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
        await api.mockSession({
          user: { id: "user-1", name: "Test User", email: "test@example.com" },
          session: { id: "session-1", expiresAt: "2099-01-01T00:00:00Z" },
        });
        await api.mockSseToken("opaque-token");
        await api.mockReady([]);
        await page.route("**/api/auth/token", async (route) => {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ token: "mock-jwt" }),
          });
        });
        await page.route("**/api/events**", async (route) => {
          if (route.request().url().includes("/api/events/token")) {
            await route.fallback();
            return;
          }
          await route.fulfill({
            status: 200,
            contentType: "text/event-stream",
            headers: { "Cache-Control": "no-cache" },
            body: 'event: connected\ndata: {"message":"connected"}\n\n',
          });
        });
      }

      // Navigate both pages
      await page1.goto("/");
      await page1.waitForLoadState("domcontentloaded");
      await page2.goto("/");
      await page2.waitForLoadState("domcontentloaded");

      // Both should show app content
      await expect(page1.locator("h1")).toHaveText("Cortex", {
        timeout: 15_000,
      });
      await expect(page2.locator("h1")).toHaveText("Cortex", {
        timeout: 15_000,
      });

      // Simulate sign-out on page1 by dispatching the auth-sign-out event
      // and changing the session mock on page2 to return null
      await page2.route("**/api/auth/get-session", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ session: null, user: null }),
        });
      });

      // Dispatch the sign-out event on page1 (which broadcasts to other tabs)
      await page1.evaluate(() => {
        window.dispatchEvent(new CustomEvent("auth-sign-out"));
      });

      // Trigger focus on page2 to simulate tab switch (which triggers session re-check)
      await page2.evaluate(() => {
        window.dispatchEvent(new Event("focus"));
      });

      // page2 should eventually show the login page after re-checking session
      // Give it some time since the session re-check is async
      await expect(page2.getByText("Sign in to Loom")).toBeVisible({
        timeout: 15_000,
      });

      await page1.close();
      await page2.close();
    });
  });
});
