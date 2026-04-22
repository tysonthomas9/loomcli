/* eslint-disable @typescript-eslint/no-explicit-any, @typescript-eslint/no-unused-vars */
/**
 * E2E tests for Terminal disconnect and reconnect.
 *
 * Covers WebSocket connection lifecycle: initial connection overlay,
 * network disconnect, auto-reconnect, scrollback recovery, backoff
 * exhaustion, and backend crash (close code 4001).
 *
 * All backend interactions are mocked via page.route() and page.addInitScript()
 * for WebSocket — no real backend needed.
 * Uses workspace-scoped routing (same pattern as terminal-keyboard-shortcuts).
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const SESSION_NAME = "test-session-01";
const WORKSPACE_ID = "default";
const MOCK_TOKEN = "mock-token-abc";
const WS_PREFIX = `/api/workspaces/${WORKSPACE_ID}`;

const WORKSPACE_DATA = {
  id: WORKSPACE_ID,
  name: "Disconnect Test Workspace",
  path: "/tmp/disconnect-test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "Disconnect Test Workspace",
      path: "/tmp/disconnect-test-ws",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  default_workspace: WORKSPACE_ID,
};

const MOCK_STATS = {
  total_issues: 0,
  open_issues: 0,
  in_progress_issues: 0,
  closed_issues: 0,
  blocked_issues: 0,
  deferred_issues: 0,
  ready_issues: 0,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 0,
};

interface TabMetadata {
  session_name: string;
  label: string;
  sort_order: number;
  pinned: boolean;
  notes: string;
  created_at: string;
  updated_at: string;
}

const SINGLE_TAB: TabMetadata = {
  session_name: SESSION_NAME,
  label: SESSION_NAME,
  sort_order: 0,
  pinned: false,
  notes: "",
  created_at: "2026-03-28T00:00:00Z",
  updated_at: "2026-03-28T00:00:00Z",
};

// ---------------------------------------------------------------------------
// WebSocket Mock
// ---------------------------------------------------------------------------

interface WSMockConfig {
  autoOpen?: boolean;
  autoCloseCode?: number;
  autoCloseReason?: string;
  /** Replace backoff delays (>= 800ms) with 10ms for fast exhaustion tests. */
  speedUpTimers?: boolean;
}

/**
 * Inject a controllable WebSocket mock via page.addInitScript().
 * Must be called BEFORE page.goto().
 *
 * Creates window.__mockWSInstances (array of mock WS objects) and
 * window.__mockWSConfig for controlling auto-behavior.
 */
async function injectWebSocketMock(
  page: Page,
  config: WSMockConfig = {},
): Promise<void> {
  await page.addInitScript((cfg) => {
    localStorage.setItem("terminal-onboarding-dismissed", "1");

    // Speed up backoff timers for exhaustion tests
    if (cfg.speedUpTimers) {
      const origST = window.setTimeout;
      (window as any).setTimeout = ((
        cb: TimerHandler,
        ms?: number,
        ...rest: any[]
      ) =>
        origST.call(
          window,
          cb,
          typeof ms === "number" && ms >= 800 ? 10 : ms,
          ...rest,
        )) as typeof window.setTimeout;
    }

    (window as any).__mockWSInstances = [];
    (window as any).__mockWSConfig = { autoOpen: false, ...cfg };

    class MockWebSocket {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      url: string;
      readyState: number;
      binaryType: string;
      _sends: any[];
      onopen: ((ev: Event) => void) | null;
      onclose: ((ev: CloseEvent) => void) | null;
      onerror: ((ev: Event) => void) | null;
      onmessage: ((ev: MessageEvent) => void) | null;

      constructor(url: string) {
        this.url = url;
        this.readyState = MockWebSocket.CONNECTING;
        this.binaryType = "blob";
        this._sends = [];
        this.onopen = null;
        this.onclose = null;
        this.onerror = null;
        this.onmessage = null;

        (window as any).__mockWSInstances.push(this);

        const currentCfg = (window as any).__mockWSConfig;

        if (currentCfg.autoOpen) {
          // Auto-open, then optionally auto-close
          Promise.resolve().then(() => {
            if (this.readyState === MockWebSocket.CLOSED) return;
            this.readyState = MockWebSocket.OPEN;
            this.onopen?.(new Event("open"));

            if (currentCfg.autoCloseCode != null) {
              Promise.resolve().then(() => {
                if (this.readyState === MockWebSocket.CLOSED) return;
                this.readyState = MockWebSocket.CLOSED;
                this.onclose?.(
                  new CloseEvent("close", {
                    code: currentCfg.autoCloseCode,
                    reason: currentCfg.autoCloseReason || "",
                    wasClean: false,
                  }),
                );
              });
            }
          });
        } else if (currentCfg.autoCloseCode != null) {
          // Close without opening — simulates connection failure
          // (hasConnected never becomes true)
          Promise.resolve().then(() => {
            if (this.readyState === MockWebSocket.CLOSED) return;
            this.readyState = MockWebSocket.CLOSED;
            this.onerror?.(new Event("error"));
            this.onclose?.(
              new CloseEvent("close", {
                code: currentCfg.autoCloseCode,
                reason: currentCfg.autoCloseReason || "",
                wasClean: false,
              }),
            );
          });
        }
      }

      send(data: any) {
        this._sends.push(data);
      }

      close(_code?: number, _reason?: string) {
        this.readyState = MockWebSocket.CLOSED;
      }

      addEventListener() {}
      removeEventListener() {}
      dispatchEvent() {
        return true;
      }
    }

    (window as any).WebSocket = MockWebSocket;
  }, config);
}

// ---------------------------------------------------------------------------
// Request tracking
// ---------------------------------------------------------------------------

interface RequestTracker {
  calls: Array<{ url: string; method: string }>;
}

// ---------------------------------------------------------------------------
// HTTP Mock Setup
// ---------------------------------------------------------------------------

interface MockTrackers {
  tokenTracker: RequestTracker;
  scrollbackTracker: RequestTracker;
  restartTracker: RequestTracker;
}

interface SetupOptions {
  /** Override tab metadata list (default: single tab). */
  tabs?: TabMetadata[];
}

async function setupHttpMocks(
  page: Page,
  options: SetupOptions = {},
): Promise<MockTrackers> {
  const tabList = options.tabs ?? [SINGLE_TAB];
  const tokenTracker: RequestTracker = { calls: [] };
  const scrollbackTracker: RequestTracker = { calls: [] };
  const restartTracker: RequestTracker = { calls: [] };

  // Neutralize AbortController signals (React StrictMode workaround)
  await page.addInitScript(() => {
    const origFetch = window.fetch;
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      if (init?.signal) {
        const { signal: _signal, ...rest } = init;
        return origFetch.call(this, input, rest);
      }
      return origFetch.call(this, input, init);
    };
  });

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  // Daemon health
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "ok",
        daemon: {
          connected: true,
          status: "running",
          uptime: 1000,
          version: "test",
        },
      }),
    });
  });

  // Terminal token
  await page.route("**/api/terminal/token**", async (route) => {
    tokenTracker.calls.push({
      url: route.request().url(),
      method: route.request().method(),
    });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: MOCK_TOKEN }),
    });
  });

  // Terminal state (GET/PATCH) — non-workspace-scoped fallback
  await page.route("**/api/terminal/state", async (route) => {
    if (route.request().method() === "PATCH") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { active_tab: SESSION_NAME },
      }),
    });
  });

  // Terminal session-status
  await page.route("**/api/terminal/session-status**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ alive: true }),
    });
  });

  // Terminal restart
  await page.route("**/api/terminal/restart**", async (route) => {
    restartTracker.calls.push({
      url: route.request().url(),
      method: route.request().method(),
    });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, backend: "shell" }),
    });
  });

  // Terminal spawn
  await page.route("**/api/terminal/spawn", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, session: SESSION_NAME }),
    });
  });

  // Config backend
  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          backend: "shell",
          source: "project",
          available: ["shell"],
          agents: [],
        },
      }),
    });
  });

  // Monitor server endpoints
  await page.route("**/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });

  // Workspace-scoped API endpoints (single handler)
  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();
      const method = route.request().method();

      // Workspace resolution
      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: WORKSPACE_DATA }),
        });
        return;
      }

      // SSE events: fulfill with connected event then close
      if (url.includes(WS_PREFIX + "/events")) {
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          headers: {
            "Cache-Control": "no-cache",
            Connection: "keep-alive",
          },
          body: 'event: connected\ndata: {"message":"connected"}\n\n',
        });
        return;
      }

      // Terminal tabs list
      if (
        url.includes(WS_PREFIX + "/terminal/tabs") &&
        !url.match(/\/terminal\/tabs\/[^/]/)
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: tabList }),
        });
        return;
      }

      // Terminal tab by session
      if (url.match(/\/terminal\/tabs\/[^/?]+/)) {
        if (method === "DELETE") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: SINGLE_TAB }),
        });
        return;
      }

      // Terminal sessions by issue
      if (url.includes(WS_PREFIX + "/terminal/sessions/by-issue")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
        return;
      }

      // Terminal state (workspace-scoped)
      if (url.includes(WS_PREFIX + "/terminal/state")) {
        if (method === "PATCH") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: { active_tab: SESSION_NAME },
          }),
        });
        return;
      }

      // Issues / graph
      if (url.includes(WS_PREFIX + "/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }

      // Ready / blocked
      if (url.includes(WS_PREFIX + "/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }
      if (url.includes(WS_PREFIX + "/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }

      // Stats
      if (url.includes(WS_PREFIX + "/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: MOCK_STATS }),
        });
        return;
      }

      // Generic terminal catch-all
      if (url.includes(WS_PREFIX + "/terminal/")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
        return;
      }

      // Exact workspace path
      if (url.includes(WS_PREFIX)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: WORKSPACE_DATA }),
        });
        return;
      }

      // Unknown workspace route
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
    },
  );

  // Scrollback — dedicated route registered AFTER workspace handler.
  // Playwright tries last-registered routes first, so this intercepts
  // scrollback requests before the workspace handler's catch-all can.
  await page.route("**/terminal/sessions/*/scrollback*", async (route) => {
    scrollbackTracker.calls.push({
      url: route.request().url(),
      method: route.request().method(),
    });
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: { content: "$ mock scrollback\r\n", lines: 1 },
      }),
    });
  });

  return { tokenTracker, scrollbackTracker, restartTracker };
}

// ---------------------------------------------------------------------------
// Navigation helper
// ---------------------------------------------------------------------------

async function navigateToTerminal(page: Page): Promise<void> {
  await page.goto(`/ws/${WORKSPACE_ID}/?view=terminal`);
  await page.waitForSelector('[role="banner"]', { timeout: 15_000 });
  await expect(page.getByTestId("terminal-tab-bar")).toBeVisible({
    timeout: 10_000,
  });
}

// ---------------------------------------------------------------------------
// WebSocket control helpers
// ---------------------------------------------------------------------------

async function getWSInstanceCount(page: Page): Promise<number> {
  return page.evaluate(() => (window as any).__mockWSInstances.length);
}

async function fireMockWSOpen(page: Page, index: number): Promise<void> {
  await page.evaluate((idx) => {
    const ws = (window as any).__mockWSInstances[idx];
    if (ws) {
      ws.readyState = 1; // OPEN
      ws.onopen?.(new Event("open"));
    }
  }, index);
}

async function fireMockWSClose(
  page: Page,
  index: number,
  code = 1006,
  reason = "",
): Promise<void> {
  await page.evaluate(
    ({ idx, code, reason }) => {
      const ws = (window as any).__mockWSInstances[idx];
      if (ws) {
        ws.readyState = 3; // CLOSED
        ws.onclose?.(
          new CloseEvent("close", {
            code,
            reason,
            wasClean: code === 1000,
          }),
        );
      }
    },
    { idx: index, code, reason },
  );
}

async function reconfigureWSMock(
  page: Page,
  config: Partial<WSMockConfig>,
): Promise<void> {
  await page.evaluate((cfg) => {
    (window as any).__mockWSConfig = {
      ...(window as any).__mockWSConfig,
      ...cfg,
    };
  }, config);
}

/**
 * Wait for at least one WS instance with `onopen` handler set and return its
 * index. Optionally filter by session name in the URL (needed when multiple
 * tabs each create their own WS).
 * React StrictMode double-mounts, so instance 0 is typically cancelled.
 */
async function waitForActiveWS(
  page: Page,
  sessionFilter?: string,
): Promise<number> {
  await expect
    .poll(
      () =>
        page.evaluate(
          (filter) => {
            const insts = (window as any).__mockWSInstances;
            return insts.filter(
              (ws: any) =>
                typeof ws.onopen === "function" &&
                (!filter || (ws.url && ws.url.includes(filter))),
            ).length;
          },
          sessionFilter ?? null,
        ),
      { timeout: 10_000 },
    )
    .toBeGreaterThanOrEqual(1);
  // Return the index of the last matching instance
  return page.evaluate(
    (filter) => {
      const insts = (window as any).__mockWSInstances;
      for (let i = insts.length - 1; i >= 0; i--) {
        if (typeof insts[i].onopen !== "function") continue;
        if (filter && !(insts[i].url && insts[i].url.includes(filter)))
          continue;
        return i;
      }
      return insts.length - 1;
    },
    sessionFilter ?? null,
  );
}

// ===========================================================================
// Tests
// ===========================================================================

test.describe("Terminal disconnect and reconnect", () => {
  // -----------------------------------------------------------------------
  // Initial Connection
  // -----------------------------------------------------------------------
  test.describe("Initial Connection", () => {
    test("shows connecting overlay on first connection", async ({ page }) => {
      await injectWebSocketMock(page); // no autoOpen → WS stays in CONNECTING
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      const overlay = page.getByTestId("terminal-connection-overlay");
      await expect(overlay).toBeVisible({ timeout: 10_000 });
      await expect(overlay).toContainText("Connecting...");
      await expect(overlay).toHaveAttribute("role", "status");
      await expect(page.getByTestId("crash-overlay")).not.toBeVisible();
    });

    test("hides overlay when connection succeeds", async ({ page }) => {
      await injectWebSocketMock(page);
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Wait for active WS instance (StrictMode creates a cancelled one first)
      const idx = await waitForActiveWS(page);
      await fireMockWSOpen(page, idx);

      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });
    });
  });

  // -----------------------------------------------------------------------
  // Network Disconnect
  // -----------------------------------------------------------------------
  test.describe("Network Disconnect", () => {
    test("shows disconnected overlay when WebSocket closes", async ({
      page,
    }) => {
      await injectWebSocketMock(page);
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect then disconnect
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });

      await fireMockWSClose(page, wsIdx, 1006);

      const overlay = page.getByTestId("terminal-connection-overlay");
      await expect(overlay).toBeVisible({ timeout: 5_000 });
      await expect(overlay).toContainText("Disconnected");
      await expect(
        page.getByTestId("terminal-reconnect-button"),
      ).toBeVisible();
      await expect(overlay).toContainText("Auto-reconnecting...");
    });

    test("reconnect button triggers new connection attempt", async ({
      page,
    }) => {
      await injectWebSocketMock(page);
      const { tokenTracker } = await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect then disconnect
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });
      await fireMockWSClose(page, wsIdx, 1006);
      await expect(
        page.getByTestId("terminal-reconnect-button"),
      ).toBeVisible({ timeout: 5_000 });

      const countBefore = await getWSInstanceCount(page);
      const tokenCountBefore = tokenTracker.calls.length;

      await page.getByTestId("terminal-reconnect-button").click();

      // New WS instance should be created (token fetched + WS constructed)
      await expect
        .poll(() => getWSInstanceCount(page), { timeout: 5_000 })
        .toBeGreaterThan(countBefore);
      // Token endpoint should have been called
      await expect
        .poll(() => tokenTracker.calls.length, { timeout: 5_000 })
        .toBeGreaterThan(tokenCountBefore);
    });

    test("auto-reconnect fires after disconnect", async ({ page }) => {
      await injectWebSocketMock(page);
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect then disconnect
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });

      const countBeforeDisconnect = await getWSInstanceCount(page);
      await fireMockWSClose(page, wsIdx, 1006);

      // Auto-reconnect should create a new WS without manual button click
      await expect
        .poll(() => getWSInstanceCount(page), { timeout: 5_000 })
        .toBeGreaterThan(countBeforeDisconnect);
    });
  });

  // -----------------------------------------------------------------------
  // Reconnect with Scrollback Recovery
  // -----------------------------------------------------------------------
  test.describe("Reconnect with Scrollback Recovery", () => {
    test("fetches scrollback on reconnect after prior connection", async ({
      page,
    }) => {
      await injectWebSocketMock(page);
      const { scrollbackTracker } = await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect (hasConnected → true) then disconnect
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });
      await fireMockWSClose(page, wsIdx, 1006);

      // Scrollback is fetched during auto-reconnect (before new WS created)
      await expect
        .poll(() => scrollbackTracker.calls.length, { timeout: 10_000 })
        .toBeGreaterThanOrEqual(1);
    });

    test("skips scrollback on initial connection failure", async ({
      page,
    }) => {
      // WS errors + closes WITHOUT opening (hasConnected stays false)
      await injectWebSocketMock(page, {
        autoCloseCode: 1006,
        speedUpTimers: true,
      });
      const { scrollbackTracker } = await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Wait for at least one reconnect attempt
      await expect
        .poll(() => getWSInstanceCount(page), { timeout: 10_000 })
        .toBeGreaterThan(1);

      // Scrollback should NOT have been fetched (hasConnected was never true)
      expect(scrollbackTracker.calls.length).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // Backoff Exhaustion
  // -----------------------------------------------------------------------
  test.describe("Backoff Exhaustion", () => {
    test("shows error state after max initial attempts (3)", async ({
      page,
    }) => {
      // WS errors + closes on every attempt, timers sped up
      await injectWebSocketMock(page, {
        autoCloseCode: 1006,
        speedUpTimers: true,
      });
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // INITIAL_CONNECT_CONFIG: 3 attempts, baseDelay=3000, maxDelay=15000
      // With speedUpTimers all delays are 10ms — exhaustion happens in ~30ms
      const overlay = page.getByTestId("terminal-connection-overlay");
      await expect(overlay).toContainText("Connection failed", {
        timeout: 10_000,
      });
      await expect(
        page.getByTestId("terminal-reconnect-button"),
      ).toBeVisible();
      // Error state has no "Auto-reconnecting..." subtext
      await expect(overlay).not.toContainText("Auto-reconnecting...");
    });

    test("shows session expired after mid-session reconnect exhaustion", async ({
      page,
    }) => {
      // First connection succeeds (autoOpen), subsequent ones will fail
      await injectWebSocketMock(page, {
        autoOpen: true,
        speedUpTimers: true,
      });
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Wait for initial successful connection (overlay disappears)
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 10_000 });

      // Reconfigure mock to fail future connections (auto-open then auto-close)
      await reconfigureWSMock(page, { autoCloseCode: 1006 });

      // Disconnect the current (successful) connection
      const currentCount = await getWSInstanceCount(page);
      await fireMockWSClose(page, currentCount - 1, 1006);

      // DEFAULT_RECONNECT_CONFIG: 10 attempts + 30s wall-clock timeout
      // With speedUpTimers both exhaust in ~100ms
      const reconnectOverlay = page.getByTestId("reconnecting-overlay");
      await expect(reconnectOverlay).toContainText("Session expired", {
        timeout: 10_000,
      });
      await expect(
        page.getByTestId("reconnect-expired-button"),
      ).toBeVisible();
    });
  });

  // -----------------------------------------------------------------------
  // Backend Crash — Close Code 4001
  // -----------------------------------------------------------------------
  test.describe("Backend Crash — Close Code 4001", () => {
    test("shows crash overlay when backend exits", async ({ page }) => {
      await injectWebSocketMock(page);
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });

      // Crash (close code 4001)
      await fireMockWSClose(page, wsIdx, 4001, "process exited with code 1");

      const crashOverlay = page.getByTestId("crash-overlay");
      await expect(crashOverlay).toBeVisible({ timeout: 5_000 });
      await expect(crashOverlay).toHaveAttribute("role", "alert");
      await expect(
        page.getByTestId("crash-overlay-reason"),
      ).toContainText("process exited with code 1");
      await expect(page.getByTestId("crash-overlay-restart")).toBeVisible();
      await expect(page.getByTestId("crash-overlay-close")).toBeVisible();
      // TerminalConnectionOverlay must NOT be visible (crashReason blocks it)
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible();
    });

    test("crash overlay restart button triggers restart flow", async ({
      page,
    }) => {
      await injectWebSocketMock(page);
      const { tokenTracker, restartTracker } = await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect then crash
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });
      await fireMockWSClose(page, wsIdx, 4001, "process exited with code 1");
      await expect(page.getByTestId("crash-overlay")).toBeVisible({
        timeout: 5_000,
      });

      const tokenCountBefore = tokenTracker.calls.length;
      const wsCountBefore = await getWSInstanceCount(page);

      // Click restart
      await page.getByTestId("crash-overlay-restart").click();

      // Token should be fetched for restart
      await expect
        .poll(() => tokenTracker.calls.length, { timeout: 5_000 })
        .toBeGreaterThan(tokenCountBefore);

      // Restart endpoint should be called with correct URL params
      await expect
        .poll(() => restartTracker.calls.length, { timeout: 5_000 })
        .toBeGreaterThanOrEqual(1);

      const restartCall =
        restartTracker.calls[restartTracker.calls.length - 1];
      expect(restartCall.url).toContain(
        `session=${encodeURIComponent(SESSION_NAME)}`,
      );
      expect(restartCall.url).toContain(
        `token=${encodeURIComponent(MOCK_TOKEN)}`,
      );

      // Crash overlay should disappear (crashReason cleared)
      await expect(page.getByTestId("crash-overlay")).not.toBeVisible({
        timeout: 5_000,
      });

      // New WS connection attempt should start
      await expect
        .poll(() => getWSInstanceCount(page), { timeout: 5_000 })
        .toBeGreaterThan(wsCountBefore);
    });

    test("crash overlay close button removes overlay", async ({ page }) => {
      // Need 2 tabs — handleTabClose is a no-op when there's only 1 tab.
      const TWO_TABS: TabMetadata[] = [
        SINGLE_TAB,
        {
          session_name: "test-session-02",
          label: "test-session-02",
          sort_order: 1,
          pinned: false,
          notes: "",
          created_at: "2026-03-28T00:00:00Z",
          updated_at: "2026-03-28T00:00:00Z",
        },
      ];
      await injectWebSocketMock(page);
      await setupHttpMocks(page, { tabs: TWO_TABS });
      await navigateToTerminal(page);

      // Connect tab 1 by name (with 2 tabs we must target the right WS)
      const wsIdx = await waitForActiveWS(page, SESSION_NAME);
      await fireMockWSOpen(page, wsIdx);

      // Verify connection state is "connected" for tab 1's WS (React processes
      // state asynchronously — poll the app's internal connection state)
      await expect
        .poll(
          () =>
            page.evaluate(
              (idx) =>
                (window as any).__mockWSInstances[idx]?.readyState === 1,
              wsIdx,
            ),
          { timeout: 5_000 },
        )
        .toBeTruthy();

      // Crash the active tab
      await fireMockWSClose(page, wsIdx, 4001, "process exited");
      await expect(page.getByTestId("crash-overlay")).toBeVisible({
        timeout: 5_000,
      });

      // Close the crashed tab — switches to tab 2, crash overlay disappears
      await page.getByTestId("crash-overlay-close").click();

      await expect(page.getByTestId("crash-overlay")).not.toBeVisible({
        timeout: 5_000,
      });
    });

    test("does NOT auto-reconnect on crash", async ({ page }) => {
      await injectWebSocketMock(page);
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connect then crash
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);
      await expect(
        page.getByTestId("terminal-connection-overlay"),
      ).not.toBeVisible({ timeout: 5_000 });
      const countBefore = await getWSInstanceCount(page);
      await fireMockWSClose(page, wsIdx, 4001, "crashed");

      // Wait and verify no new WS was created (code 4001 skips auto-reconnect)
      await page.waitForTimeout(2_000);
      const countAfter = await getWSInstanceCount(page);
      expect(countAfter).toBe(countBefore);
    });
  });

  // -----------------------------------------------------------------------
  // Connection State Transitions
  // -----------------------------------------------------------------------
  test.describe("Connection State Transitions", () => {
    test("transitions through connecting → connected (overlay disappears)", async ({
      page,
    }) => {
      await injectWebSocketMock(page); // no autoOpen
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Connecting overlay should be visible first
      const overlay = page.getByTestId("terminal-connection-overlay");
      await expect(overlay).toBeVisible({ timeout: 10_000 });
      await expect(overlay).toContainText("Connecting...");

      // Fire open event
      const wsIdx = await waitForActiveWS(page);
      await fireMockWSOpen(page, wsIdx);

      // Overlay disappears, no crash overlay
      await expect(overlay).not.toBeVisible({ timeout: 5_000 });
      await expect(page.getByTestId("crash-overlay")).not.toBeVisible();
    });

    test("manual reconnect from error state", async ({ page }) => {
      // Exhaust initial retries to reach error state
      await injectWebSocketMock(page, {
        autoCloseCode: 1006,
        speedUpTimers: true,
      });
      await setupHttpMocks(page);
      await navigateToTerminal(page);

      // Wait for error state
      const overlay = page.getByTestId("terminal-connection-overlay");
      await expect(overlay).toContainText("Connection failed", {
        timeout: 10_000,
      });

      // Click reconnect from error state
      const wsCountBefore = await getWSInstanceCount(page);
      await page.getByTestId("terminal-reconnect-button").click();

      // New WS instance should be created (fresh connection attempt)
      await expect
        .poll(() => getWSInstanceCount(page), { timeout: 5_000 })
        .toBeGreaterThan(wsCountBefore);
    });
  });
});
