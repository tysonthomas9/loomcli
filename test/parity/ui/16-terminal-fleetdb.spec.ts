/**
 * 16 Terminal fleet-db acceptance — proves the terminal surface that remains
 * after tmux lifecycle removal works when issue state is fleet-db-backed.
 */
import type { Page } from "@playwright/test";
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import {
  discoverWorkspaceId,
  findFleetIssueByTitle,
  SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

type RouteHitRecorder = (url: string) => void;

test.describe("16 terminal fleet-db acceptance", () => {
  test.describe.configure({ timeout: 150_000 });

  test("terminal route, metadata, state, and websocket attach work on fleet", async ({
    tabs,
    recordRouteHit,
  }) => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(PARITY_URLS.reference),
      discoverWorkspaceId(PARITY_URLS.fleet),
    ]);

    await Promise.all([
      tabs.reference.goto(
        `${PARITY_URLS.reference}/ws/${referenceWs}/terminal`,
      ),
      tabs.fleet.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/terminal`),
    ]);
    await Promise.all([
      assertTerminalShell(tabs.reference),
      assertTerminalShell(tabs.fleet),
    ]);

    const { id: issueId } = await findFleetIssueByTitle(
      SEED_FIXTURE.children[0],
    );
    const session = `parity-terminal-${Date.now()}`;
    const tabUrl = `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/terminal/tabs/${session}`;

    const initialTabs = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/terminal/tabs`,
      recordRouteHit,
    );
    expect(Array.isArray(initialTabs.data), "terminal tab list").toBeTruthy();

    const created = await apiJson(tabUrl, recordRouteHit, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        label: "Parity terminal",
        sort_order: 1,
        notes: "created by parity 16",
        pinned: false,
      }),
    });
    expect(created.data?.session_name, "created terminal session").toBe(
      session,
    );

    const readBack = await apiJson(tabUrl, recordRouteHit);
    expect(readBack.data?.label, "terminal tab label").toBe("Parity terminal");

    const patched = await apiJson(tabUrl, recordRouteHit, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        notes: "patched by parity 16",
        pinned: true,
        issue_id: issueId,
      }),
    });
    expect(patched.data?.issue_id, "terminal tab issue link").toBe(issueId);
    expect(patched.data?.pinned, "terminal tab pinned state").toBe(true);

    const byIssue = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/terminal/sessions/by-issue`,
      recordRouteHit,
    );
    expect(byIssue.data?.[issueId] ?? [], "sessions linked to issue").toContain(
      session,
    );

    await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/terminal/state`,
      recordRouteHit,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ active_tab: session }),
      },
    );
    const state = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/terminal/state`,
      recordRouteHit,
    );
    expect(state.active_tab, "terminal active tab").toBe(session);

    const tokenBody = await apiJson(
      `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/terminal/token?session=${encodeURIComponent(session)}`,
      recordRouteHit,
    );
    const token = tokenBody.token;
    expect(token, "terminal websocket token").toBeTruthy();

    await assertTerminalWsOpens(tabs.fleet, fleetWs, session, token);

    await apiJson(tabUrl, recordRouteHit, { method: "DELETE" });
    const deleted = await fetchReachable(tabUrl);
    recordRouteHit(tabUrl);
    expect(deleted.status, "deleted terminal tab status").toBe(404);
    await deleted.body?.cancel().catch(() => undefined);
  });
});

async function assertTerminalShell(page: Page): Promise<void> {
  await page.waitForLoadState("domcontentloaded").catch(() => undefined);
  await expect(page.getByTestId("terminal-view")).toBeVisible({
    timeout: 15_000,
  });
  const tabBar = page.getByTestId("terminal-tab-bar");
  const emptyState = page.getByText("No backends configured");
  await expect(tabBar.or(emptyState)).toBeVisible({ timeout: 15_000 });
}

async function assertTerminalWsOpens(
  page: Page,
  workspace: string,
  session: string,
  token: string,
): Promise<void> {
  const result = await page.evaluate(
    async ({ workspace, session, token }) => {
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${window.location.host}/api/workspaces/${workspace}/terminal/ws?session=${encodeURIComponent(session)}&token=${encodeURIComponent(token)}`;
      return new Promise<string>((resolve, reject) => {
        const socket = new WebSocket(url);
        const timer = window.setTimeout(() => {
          socket.close();
          reject(new Error("terminal websocket open timed out"));
        }, 10_000);
        socket.onopen = () => {
          window.clearTimeout(timer);
          socket.close();
          resolve("open");
        };
        socket.onerror = () => {
          window.clearTimeout(timer);
          reject(new Error("terminal websocket failed to open"));
        };
      });
    },
    { workspace, session, token },
  );
  expect(result, "terminal websocket attach").toBe("open");
}

async function apiJson(
  url: string,
  recordRouteHit: RouteHitRecorder,
  init?: RequestInit,
): Promise<any> {
  recordRouteHit(url);
  const response = await fetchReachable(url, init);
  expect(response.ok, `${url} status=${response.status}`).toBeTruthy();
  return response.json();
}

async function fetchReachable(
  url: string,
  init?: RequestInit,
): Promise<Response> {
  let last: Response | null = null;
  for (let attempt = 0; attempt < 8; attempt++) {
    const response = await fetch(url, init);
    last = response;
    if (response.ok || ![429, 500, 503].includes(response.status)) {
      return response;
    }
    await response.body?.cancel().catch(() => undefined);
    await new Promise((resolve) => setTimeout(resolve, 500 + attempt * 250));
  }
  return last ?? fetch(url, init);
}
