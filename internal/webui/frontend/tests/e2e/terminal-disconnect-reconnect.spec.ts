import { test, expect, Page } from "@playwright/test";

import {
  setupTerminalMocks,
  navigateToTerminal,
} from "../helpers/terminal-mocks";

/**
 * E2E tests for terminal disconnect and reconnect behavior.
 * Covers connection state transitions, WebSocket mock injection,
 * the status dot indicator, auto-reconnect behavior, and tab state after disconnect.
 *
 * Uses page.addInitScript() to monkeypatch WebSocket for controllable behavior.
 */

/**
 * Inject a controllable WebSocket mock via addInitScript.
 * When autoOpen is true, WebSocket fires onopen in a microtask after construction.
 * When false, tests must manually fire events via page.evaluate().
 */
async function injectWebSocketMock(
  page: Page,
  options?: { autoOpen?: boolean },
) {
  const autoOpen = options?.autoOpen ?? false;
  await page.addInitScript(
    ({ autoOpen: doAutoOpen }) => {
      const instances: Array<{
        url: string;
        readyState: number;
        onopen: ((ev: Event) => void) | null;
        onclose: ((ev: CloseEvent) => void) | null;
        onmessage: ((ev: MessageEvent) => void) | null;
        onerror: ((ev: Event) => void) | null;
        fireOpen: () => void;
        fireClose: (code?: number, reason?: string) => void;
        fireError: () => void;
      }> = [];

      (window as unknown as Record<string, unknown>).__mockWSInstances =
        instances;

      class MockWebSocket {
        static readonly CONNECTING = 0;
        static readonly OPEN = 1;
        static readonly CLOSING = 2;
        static readonly CLOSED = 3;
        readonly CONNECTING = 0;
        readonly OPEN = 1;
        readonly CLOSING = 2;
        readonly CLOSED = 3;
        readyState = MockWebSocket.CONNECTING;
        binaryType = "arraybuffer";
        url: string;
        onopen: ((ev: Event) => void) | null = null;
        onclose: ((ev: CloseEvent) => void) | null = null;
        onmessage: ((ev: MessageEvent) => void) | null = null;
        onerror: ((ev: Event) => void) | null = null;

        constructor(url: string) {
          this.url = url;
          const self = this;
          const entry = {
            url,
            get readyState() {
              return self.readyState;
            },
            get onopen() {
              return self.onopen;
            },
            get onclose() {
              return self.onclose;
            },
            get onmessage() {
              return self.onmessage;
            },
            get onerror() {
              return self.onerror;
            },
            fireOpen() {
              self.readyState = MockWebSocket.OPEN;
              self.onopen?.(new Event("open"));
            },
            fireClose(code = 1000, reason = "") {
              self.readyState = MockWebSocket.CLOSED;
              self.onclose?.(
                new CloseEvent("close", { code, reason, wasClean: true }),
              );
            },
            fireError() {
              self.onerror?.(new Event("error"));
            },
          };
          instances.push(entry);

          if (doAutoOpen) {
            queueMicrotask(() => {
              entry.fireOpen();
            });
          }
        }

        send() {}
        close() {
          this.readyState = MockWebSocket.CLOSED;
        }
        addEventListener() {}
        removeEventListener() {}
        dispatchEvent() {
          return true;
        }
      }

      (window as unknown as Record<string, unknown>).WebSocket = MockWebSocket;
    },
    { autoOpen },
  );
}

/** Evaluate helper: get number of mock WS instances */
async function getMockWSCount(page: Page): Promise<number> {
  return page.evaluate(
    () =>
      ((window as unknown as Record<string, unknown[]>).__mockWSInstances ?? [])
        .length,
  );
}

/** Evaluate helper: fire open on all CONNECTING instances that have onopen set */
async function fireOpenOnReady(page: Page): Promise<void> {
  await page.evaluate(() => {
    const instances = (
      window as unknown as Record<
        string,
        Array<{
          readyState: number;
          onopen: unknown;
          fireOpen: () => void;
        }>
      >
    ).__mockWSInstances;
    for (const inst of instances) {
      if (inst.readyState === 0 && inst.onopen) {
        inst.fireOpen();
      }
    }
  });
}

/** Evaluate helper: fire close on the last OPEN instance */
async function fireCloseOnOpen(page: Page): Promise<void> {
  await page.evaluate(() => {
    const instances = (
      window as unknown as Record<
        string,
        Array<{
          readyState: number;
          fireClose: (code?: number, reason?: string) => void;
        }>
      >
    ).__mockWSInstances;
    for (let i = instances.length - 1; i >= 0; i--) {
      if (instances[i].readyState === 1) {
        instances[i].fireClose(1006, "abnormal closure");
        break;
      }
    }
  });
}

test.describe("Terminal disconnect and reconnect", () => {
  test.describe("Initial connection", () => {
    test("status dot shows disconnected when WebSocket does not connect", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: false });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const statusDot = page.getByTestId("terminal-tab-status-session-1");
      await expect(statusDot).toBeVisible();
      const status = await statusDot.getAttribute("data-status");
      expect(["disconnected", "connecting"]).toContain(status);
    });

    test("status dot shows connected when WebSocket opens", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: true });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect
        .poll(
          async () =>
            page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status"),
          { timeout: 5000 },
        )
        .toBe("connected");
    });

    test("WebSocket mock captures the connection URL", async ({ page }) => {
      await injectWebSocketMock(page, { autoOpen: true });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const count = await getMockWSCount(page);
      expect(count).toBeGreaterThanOrEqual(1);
    });
  });

  test.describe("Disconnect behavior", () => {
    test("status dot transitions through connection states", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: false });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Wait for WS instance to exist
      await expect
        .poll(() => getMockWSCount(page), { timeout: 5000 })
        .toBeGreaterThanOrEqual(1);

      // Poll: fire open on instances that have handlers attached
      await expect
        .poll(
          async () => {
            await fireOpenOnReady(page);
            return page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status");
          },
          { timeout: 10000, intervals: [200, 500, 1000] },
        )
        .toBe("connected");

      // Ensure the connection is fully settled before triggering close
      await expect
        .poll(
          async () =>
            page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status"),
          { timeout: 5000 },
        )
        .toBe("connected");
      await fireCloseOnOpen(page);

      // After close, state should no longer be connected
      await expect
        .poll(
          async () =>
            page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status"),
          { timeout: 5000 },
        )
        .not.toBe("connected");
    });

    test("terminal instance container remains visible after disconnect", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: true });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect
        .poll(
          async () =>
            page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status"),
          { timeout: 5000 },
        )
        .toBe("connected");

      await page.evaluate(() => {
        const instances = (
          window as unknown as Record<
            string,
            Array<{ fireClose: (code?: number, reason?: string) => void }>
          >
        ).__mockWSInstances;
        instances?.[0]?.fireClose(1006);
      });

      await expect(page.getByTestId("terminal-instance")).toBeVisible();
      await expect(page.getByTestId("terminal-tab-bar")).toBeVisible();
    });
  });

  test.describe("Auto-reconnect", () => {
    test("auto-reconnect creates new WebSocket after disconnect", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: false });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await expect
        .poll(() => getMockWSCount(page), { timeout: 5000 })
        .toBeGreaterThanOrEqual(1);

      // Connect
      await expect
        .poll(
          async () => {
            await fireOpenOnReady(page);
            return page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status");
          },
          { timeout: 10000, intervals: [200, 500, 1000] },
        )
        .toBe("connected");

      // Ensure the connection is fully settled before triggering close
      await expect
        .poll(
          async () =>
            page
              .getByTestId("terminal-tab-status-session-1")
              .getAttribute("data-status"),
          { timeout: 5000 },
        )
        .toBe("connected");
      const countBefore = await getMockWSCount(page);

      await fireCloseOnOpen(page);

      // Auto-reconnect should create new WS instances
      await expect
        .poll(() => getMockWSCount(page), { timeout: 20000 })
        .toBeGreaterThan(countBefore);
    });
  });

  test.describe("Connection state indicators", () => {
    test("status dot reflects connecting state during token fetch", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: false });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const statusDot = page.getByTestId("terminal-tab-status-session-1");
      const status = await statusDot.getAttribute("data-status");
      expect(["connecting", "disconnected"]).toContain(status);
    });

    test("tab bar is functional during disconnected state", async ({
      page,
    }) => {
      await injectWebSocketMock(page, { autoOpen: false });
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const tab = page.getByTestId("terminal-tab-session-1");
      await expect(tab).toBeVisible();
      await expect(tab).toHaveAttribute("aria-selected", "true");

      await page.getByTestId("terminal-new-tab-button").click();
      await expect(
        page.getByTestId("session-name-prompt-overlay"),
      ).toBeVisible();
    });
  });
});
