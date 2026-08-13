import { expect, test } from "../fixtures";
import type { Page } from "@playwright/test";

import { WORKSPACE_ID, WS_API, setupFleetMocks } from "./helpers/fleet";

const SESSION_NAME = "lead-shell-3";
const HISTORY_LINES = 50_000;
const GENERATION = "wheel-history-generation";

const TAB = {
  session_name: SESSION_NAME,
  label: SESSION_NAME,
  sort_order: 0,
  pinned: false,
  notes: "",
  backend: "shell",
  writable: true,
  pty_alive: true,
  attached_clients: 0,
  created_at: "2026-08-07T00:00:00Z",
  updated_at: "2026-08-07T00:00:00Z",
};

async function installBrowserInstrumentation(page: Page): Promise<void> {
  await page.addInitScript((historyLines) => {
    localStorage.setItem("terminal-onboarding-dismissed", "1");
    localStorage.removeItem("terminal.history.mode");

    type WheelTrace = {
      target: string;
      trusted: boolean;
      defaultPrevented: boolean;
      preventDefaultStack?: string;
      stopPropagationStack?: string;
    };

    const describeTarget = (target: EventTarget | null): string => {
      if (!(target instanceof Element)) return String(target);
      const classes = Array.from(target.classList).join(".");
      return `${target.tagName.toLowerCase()}${classes ? `.${classes}` : ""}`;
    };

    const originalPreventDefault = Event.prototype.preventDefault;
    Event.prototype.preventDefault = function () {
      let trace: WheelTrace | undefined;
      if (this.type === "wheel") {
        trace = (window as typeof window & { __wheelTrace?: WheelTrace })
          .__wheelTrace;
        if (trace) trace.preventDefaultStack = new Error().stack;
      }
      originalPreventDefault.call(this);
      if (trace) trace.defaultPrevented = this.defaultPrevented;
    };

    const originalStopPropagation = Event.prototype.stopPropagation;
    Event.prototype.stopPropagation = function () {
      if (this.type === "wheel") {
        const trace = (window as typeof window & { __wheelTrace?: WheelTrace })
          .__wheelTrace;
        if (trace) trace.stopPropagationStack = new Error().stack;
      }
      originalStopPropagation.call(this);
    };

    document.addEventListener(
      "wheel",
      (event) => {
        const trace: WheelTrace = {
          target: describeTarget(event.target),
          trusted: event.isTrusted,
          defaultPrevented: event.defaultPrevented,
        };
        (window as typeof window & { __wheelTrace?: WheelTrace }).__wheelTrace =
          trace;
        queueMicrotask(() => {
          trace.defaultPrevented = event.defaultPrevented;
        });
      },
      { capture: true, passive: true },
    );

    type MockSocket = {
      readyState: number;
      onopen: ((event: Event) => void) | null;
      onclose: ((event: CloseEvent) => void) | null;
      onmessage: ((event: MessageEvent) => void) | null;
      sent: unknown[];
    };

    (
      window as typeof window & { __mockWSInstances?: MockSocket[] }
    ).__mockWSInstances = [];

    class MockWebSocket {
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      static readonly CLOSING = 2;
      static readonly CLOSED = 3;

      readonly url: string;
      readyState = MockWebSocket.CONNECTING;
      binaryType = "blob";
      sent: unknown[] = [];
      onopen: ((event: Event) => void) | null = null;
      onclose: ((event: CloseEvent) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;

      constructor(url: string) {
        this.url = url;
        (
          window as typeof window & { __mockWSInstances?: MockWebSocket[] }
        ).__mockWSInstances?.push(this);
        queueMicrotask(() => {
          if (this.readyState !== MockWebSocket.CONNECTING) return;
          this.readyState = MockWebSocket.OPEN;
          this.onopen?.(new Event("open"));
          this.onmessage?.(
            new MessageEvent("message", {
              data: JSON.stringify({
                type: "terminal-history",
                firstScreenLine: historyLines,
              }),
            }),
          );
          this.onmessage?.(
            new MessageEvent("message", {
              data: "live terminal tail$ ",
            }),
          );
        });
      }

      send(data: unknown) {
        this.sent.push(data);
      }

      close() {
        this.readyState = MockWebSocket.CLOSED;
      }

      addEventListener() {}
      removeEventListener() {}
      dispatchEvent() {
        return true;
      }
    }

    (
      window as typeof window & { WebSocket: typeof globalThis.WebSocket }
    ).WebSocket = MockWebSocket as unknown as typeof globalThis.WebSocket;
  }, HISTORY_LINES);
}

async function setupTerminalHistory(page: Page): Promise<void> {
  await installBrowserInstrumentation(page);
  await setupFleetMocks(page, []);

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "browser-test-token" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "ok",
        daemon: { connected: true, status: "running" },
      }),
    });
  });

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });

  await page.route("**/api/config/terminal", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          grace_period_ms: 0,
          idle_timeout_ms: 0,
          max_sessions: 40,
        },
      }),
    });
  });

  await page.route(
    (url) => url.pathname.startsWith(`${WS_API}/terminal/`),
    async (route) => {
      const url = new URL(route.request().url());
      const method = route.request().method();

      if (url.pathname === `${WS_API}/terminal/tabs`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [TAB] }),
        });
        return;
      }

      if (url.pathname === `${WS_API}/terminal/state`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(
            method === "PATCH"
              ? { success: true }
              : { success: true, data: { active_tab: SESSION_NAME } },
          ),
        });
        return;
      }

      if (url.pathname === `${WS_API}/terminal/token`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "terminal-ws-token" }),
        });
        return;
      }

      if (url.pathname.endsWith("/history/meta")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            generation: GENERATION,
            totalLines: HISTORY_LINES,
            firstScreenLine: HISTORY_LINES,
            startedAt: 1,
            cols: 120,
            rows: 24,
            altScreen: false,
            gaps: 0,
            unhandledSequences: { count: 0, prefixes: {} },
            historyLimited: false,
            closed: false,
          }),
        });
        return;
      }

      if (url.pathname.endsWith("/history")) {
        const from = Number(url.searchParams.get("from") ?? "0");
        const count = Number(url.searchParams.get("count") ?? "200");
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            generation: GENERATION,
            lines: Array.from({ length: count }, (_, offset) => {
              const index = from + offset;
              return {
                i: index,
                t: index,
                cols: 120,
                runs: [{ text: `recorded line ${index}` }],
              };
            }),
          }),
        });
        return;
      }

      if (url.pathname.includes("/terminal/tabs/")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: TAB }),
        });
        return;
      }

      if (url.pathname.endsWith("/sessions/by-issue")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: {} }),
      });
    },
  );

  await page.goto(`/ws/${WORKSPACE_ID}/terminal`);
  await expect(page.getByTestId("terminal-tab-bar")).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByTestId("terminal-history-scroller")).toBeVisible();
  await expect(page.locator(".xterm-screen")).toBeVisible();
}

test("mouse wheel over xterm scrolls durable virtual history", async ({
  page,
}) => {
  await setupTerminalHistory(page);

  const scroller = page.getByTestId("terminal-history-scroller");
  await expect
    .poll(() =>
      scroller.evaluate(
        (element) =>
          element.scrollHeight - element.scrollTop - element.clientHeight,
      ),
    )
    .toBeLessThan(40);

  const before = await scroller.evaluate((element) => element.scrollTop);
  await page.evaluate(() => {
    type Socket = {
      onmessage: ((event: MessageEvent) => void) | null;
    };
    const sockets = (window as typeof window & { __mockWSInstances?: Socket[] })
      .__mockWSInstances;
    sockets
      ?.at(-1)
      ?.onmessage?.(new MessageEvent("message", { data: "\u001b[?1000h" }));
  });
  await expect(page.locator(".xterm")).toHaveClass(/enable-mouse-events/);
  const screen = page.locator(".xterm-screen");
  const box = await screen.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2);
  await page.mouse.wheel(0, -600);

  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBeLessThan(before);
  const after = await scroller.evaluate((element) => element.scrollTop);
  const trace = await page.evaluate(
    () =>
      (
        window as typeof window & {
          __wheelTrace?: {
            target: string;
            trusted: boolean;
            defaultPrevented: boolean;
            preventDefaultStack?: string;
            stopPropagationStack?: string;
          };
        }
      ).__wheelTrace,
  );
  console.log(
    JSON.stringify(
      {
        before,
        after,
        target: trace?.target,
        trusted: trace?.trusted,
        defaultPrevented: trace?.defaultPrevented,
        preventDefaultStack: trace?.preventDefaultStack,
        stopPropagationStack: trace?.stopPropagationStack,
      },
      null,
      2,
    ),
  );

  await expect
    .poll(() =>
      scroller
        .locator("[data-line-index]")
        .evaluateAll((rows) =>
          rows.reduce(
            (oldest, row) =>
              Math.min(oldest, Number(row.getAttribute("data-line-index"))),
            Number.POSITIVE_INFINITY,
          ),
        ),
    )
    .toBeLessThan(HISTORY_LINES - 1);
  await expect(scroller.getByText(/recorded line \d+/).first()).toBeVisible();
});

test("history navigation keeps terminal focus and typing follows the live tail", async ({
  page,
}) => {
  await setupTerminalHistory(page);

  const scroller = page.getByTestId("terminal-history-scroller");
  const screen = page.locator(".xterm-screen");
  const textarea = page.locator(".xterm-helper-textarea");
  await screen.click();
  await expect(textarea).toBeFocused();

  const bottom = await scroller.evaluate((element) => element.scrollTop);
  const box = await screen.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2);
  for (let step = 0; step < 6; step += 1) {
    await page.mouse.wheel(0, -37.5);
  }
  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBeLessThan(bottom);
  await expect(textarea).toBeFocused();

  await page.keyboard.type("x");
  await expect
    .poll(() =>
      page.evaluate(() => {
        type Socket = { sent: unknown[] };
        const sockets = (
          window as typeof window & { __mockWSInstances?: Socket[] }
        ).__mockWSInstances;
        return sockets?.some((socket) => socket.sent.includes("x")) ?? false;
      }),
    )
    .toBe(true);
  // Assert we are pinned to the live tail, not at a fixed pixel value: the
  // maximum reachable scrollTop is scrollHeight - clientHeight, which is
  // always less than the total content height. Comparing against
  // HISTORY_LINES * 20 passes or fails on layout luck.
  await expect
    .poll(() =>
      scroller.evaluate(
        (element) =>
          element.scrollHeight - element.clientHeight - element.scrollTop,
      ),
    )
    .toBeLessThanOrEqual(2);
  await expect(textarea).toBeFocused();
});

test("touch pan over xterm scrolls durable history", async ({ page }) => {
  await setupTerminalHistory(page);

  const scroller = page.getByTestId("terminal-history-scroller");
  const screen = page.locator(".xterm-screen");
  const box = await screen.boundingBox();
  expect(box).not.toBeNull();
  const before = await scroller.evaluate((element) => element.scrollTop);
  const x = box!.x + box!.width / 2;
  const startY = box!.y + box!.height / 2;
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setTouchEmulationEnabled", {
    enabled: true,
    maxTouchPoints: 1,
  });
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchStart",
    touchPoints: [{ x, y: startY }],
  });
  for (let step = 1; step <= 6; step += 1) {
    await cdp.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [{ x, y: startY + step * 30 }],
    });
  }
  await cdp.send("Input.dispatchTouchEvent", {
    type: "touchEnd",
    touchPoints: [],
  });

  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBeLessThan(before);
  await cdp.detach();
});

test("shift navigation keys scroll virtual history without becoming terminal input", async ({
  page,
}) => {
  await setupTerminalHistory(page);

  const scroller = page.getByTestId("terminal-history-scroller");
  const textarea = page.locator(".xterm-helper-textarea");
  await page.locator(".xterm-screen").click();
  await expect(textarea).toBeFocused();

  const terminalInputFrames = () =>
    page.evaluate(() => {
      type Socket = { sent: unknown[] };
      const sockets = (
        window as typeof window & { __mockWSInstances?: Socket[] }
      ).__mockWSInstances;
      return (
        sockets
          ?.flatMap((socket) => socket.sent)
          .filter((frame) => {
            if (typeof frame !== "string") return true;
            return !frame.startsWith("\u001b[RESIZE:");
          }) ?? []
      );
    });

  const bottom = await scroller.evaluate((element) => element.scrollTop);
  await page.keyboard.press("Shift+PageUp");
  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBeLessThan(bottom);

  await page.keyboard.press("Shift+Home");
  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBe(0);
  await expect(scroller.locator('[data-line-index="0"]')).toBeVisible();

  await page.keyboard.press("Shift+PageDown");
  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(0);

  await page.keyboard.press("Shift+End");
  await expect
    .poll(() => scroller.evaluate((element) => element.scrollTop))
    .toBeGreaterThanOrEqual(HISTORY_LINES * 20);

  expect(await terminalInputFrames()).toEqual([]);
  await expect(textarea).toBeFocused();
});
