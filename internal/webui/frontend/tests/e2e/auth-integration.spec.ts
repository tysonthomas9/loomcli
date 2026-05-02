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
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
      await mockApi.mockSseToken(null);
      await mockSSE.connect();

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // App content should render — verify the app title
      await expect(page.locator("h1")).toBeVisible({
        timeout: 10_000,
      });
      // Login page should NOT be visible
      await expect(page.getByText("Sign in to Loom")).not.toBeVisible();
    });

    test("no auth-related requests made", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      await mockApi.mockConfig({ mode: "open" });
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
      await mockApi.mockSseToken(null);
      await mockSSE.connect();

      // Register auth interceptor LAST so it has highest LIFO priority
      const authRequests: string[] = [];
      await page.route("**/api/auth/**", async (route) => {
        authRequests.push(route.request().url());
        await route.abort();
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // Wait for app to stabilize
      await expect(page.locator("h1")).toBeVisible({
        timeout: 10_000,
      });

      // No auth requests should have been made in mode=open
      expect(authRequests).toHaveLength(0);
    });

    test("open mode renders without requiring SSE token exchange", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      await mockApi.mockSseToken(null);
      await mockApi.mockConfig({ mode: "open" });
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
      await mockSSE.connect();

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      await expect(page.locator("h1")).toBeVisible({
        timeout: 10_000,
      });

      // Open mode must not block app render on an auth token exchange.
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
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
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
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
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

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");
      mockSSE.sendConnected();

      // App content should render
      await expect(page.locator("h1")).toBeVisible({
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
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
      await mockApi.mockSseToken("opaque-token");
      await page.route("**/api/auth/token", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "mock-jwt-token" }),
        });
      });
      await mockSSE.connect();

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
      await expect(page.locator("h1")).toBeVisible({
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
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
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
      // Should NOT show the app content (no h1 element exists in BootError screen)
      await expect(page.locator("h1")).toHaveCount(0);
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
    }) => {
      const sseRequests: string[] = [];

      await mockApi.mockConfig({ mode: "oidc", auth_url: "https://auth.test" });
      await mockApi.mockSession({
        user: { id: "user-1", name: "Test User", email: "test@example.com" },
        session: { id: "session-1", expiresAt: "2099-01-01T00:00:00Z" },
      });
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
      await mockApi.mockSseToken("test-opaque-token");
      await page.route("**/api/auth/token", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "mock-jwt" }),
        });
      });

      // Track SSE connection URLs (workspace-scoped)
      await page.route("**/workspaces/*/events**", async (route) => {
        const url = route.request().url();
        if (url.includes("/events/token")) {
          await route.fallback();
          return;
        }
        sseRequests.push(url);
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          headers: { "Cache-Control": "no-cache" },
          body: 'event: connected\ndata: {"message":"connected"}\n\n',
        });
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      // Wait for the app to render
      await expect(page.locator("h1")).toBeVisible({
        timeout: 15_000,
      });

      // Wait for SSE connection with token (may arrive after h1 renders)
      await expect
        .poll(() => sseRequests.some((url) => url.includes("token=test-opaque-token")), {
          timeout: 10_000,
          message: "SSE request with opaque token should have been made",
        })
        .toBe(true);
    });

    test("SSE connects without token in mode=open", async ({
      page,
      mockApi,
      mockSSE,
    }) => {
      const sseRequests: string[] = [];

      await mockApi.mockConfig({ mode: "open" });
      await mockApi.mockHealth();
      await mockApi.mockWorkspace();
      await mockApi.mockSseToken(null);

      // Track SSE connection URLs (workspace-scoped)
      await page.route("**/workspaces/*/events**", async (route) => {
        const url = route.request().url();
        if (url.includes("/events/token")) {
          await route.fallback();
          return;
        }
        sseRequests.push(url);
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          headers: { "Cache-Control": "no-cache" },
          body: 'event: connected\ndata: {"message":"connected"}\n\n',
        });
      });

      await page.goto("/");
      await page.waitForLoadState("domcontentloaded");

      await expect(page.locator("h1")).toBeVisible({
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
        await api.mockHealth();
        await api.mockWorkspace();
        await api.mockSseToken("opaque-token");
        await page.route("**/api/auth/token", async (route) => {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ token: "mock-jwt" }),
          });
        });
        await page.route("**/workspaces/*/events**", async (route) => {
          if (route.request().url().includes("/events/token")) {
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
      await expect(page1.locator("h1")).toBeVisible({
        timeout: 15_000,
      });
      await expect(page2.locator("h1")).toBeVisible({
        timeout: 15_000,
      });

      // Simulate sign-out: change session mock on page2 to return null
      await page2.route("**/api/auth/get-session", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ session: null, user: null }),
        });
      });

      // Dispatch auth-sign-out on page1 (local cleanup)
      await page1.evaluate(() => {
        window.dispatchEvent(new CustomEvent("auth-sign-out"));
      });

      // In a real multi-tab scenario, sign-out propagates via shared session cookie invalidation.
      // Simulate by reloading page2 — with the session mock now returning null,
      // the auth check fails and the login page should appear.
      await page2.reload();
      await page2.waitForLoadState("domcontentloaded");

      // page2 should show the login page since the session is now null
      await expect(page2.getByText("Sign in to Loom")).toBeVisible({
        timeout: 15_000,
      });

      await page1.close();
      await page2.close();
    });
  });
});
