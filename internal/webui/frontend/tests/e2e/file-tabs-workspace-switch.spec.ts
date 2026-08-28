import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * Regression cover for tab pruning against another workspace's ref universe.
 *
 * Switching workspaces leaves the app in a transient state the store cannot
 * see from the inside: the route's workspace id is already the new workspace,
 * while the agent and repo lists behind the valid-ref universe still describe
 * the one just left (workspaceStore.startPolling bumps the generation but does
 * not clear `workspace`). Pruning in that window closes the new workspace's
 * tabs against the old workspace's universe — and persists the closure, so a
 * reload does not bring them back.
 *
 * This was first reproduced by hand against a local-mode stack: with a
 * codex-coder tab open in LOCALMODE, serving the Files page a workspace
 * payload stamped TABBUG emptied
 * `loom:LOCALMODE:file-browser-tabs:v3` from one tab to zero. The route below
 * is that reproduction, made deterministic — the stale payload is served
 * outright rather than raced for.
 */

const WS = "ws-a"; // the workspace being viewed
const STALE_WS = "ws-b"; // the workspace the context still describes
const AGENT = "codex-coder";
const REPO = "source-repo";

const TABS_KEY = `loom:${WS}:file-browser-tabs:v3`;

/** One agent-scoped tab, in the shape the store persists. */
const persistedTabs = {
  v: 4,
  groups: [
    {
      tabs: [
        {
          ref: {
            kind: "checkout",
            checkout: { scope: "agent", target: AGENT, repo: REPO },
          },
          path: "README.md",
        },
      ],
      active: null,
    },
  ],
  mru: [],
};

/**
 * A workspace payload stamped with STALE_WS and carrying no agents — what the
 * context still reports for a beat after switching away from it. `repos` is
 * kept, so the only ref that disappears from the universe is the agent one:
 * the tab below is pruned for being agent-scoped, not because the universe is
 * empty.
 */
const staleWorkspaceData = {
  id: STALE_WS,
  name: STALE_WS,
  path: `/workspaces/${STALE_WS}`,
  repos: [
    {
      name: REPO,
      path: `/workspaces/${STALE_WS}/${REPO}`,
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS,
      name: WS,
      path: `/workspaces/${WS}`,
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: [WS],
  default_workspace: WS,
};

/** The agent checkout the tab points at does exist on the server. */
const checkouts = {
  checkouts: [
    {
      kind: "repo",
      repo: REPO,
      exists: true,
      branch: "main",
      change_count: 0,
    },
    {
      kind: "agent",
      agent: AGENT,
      repo: REPO,
      exists: true,
      branch: AGENT,
      change_count: 3,
    },
  ],
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

async function setupMocks(page: Page) {
  await page.addInitScript(
    ([key, value]) => {
      window.localStorage.setItem(key, value);
    },
    [TABS_KEY, JSON.stringify(persistedTabs)] as const,
  );

  await page.route("**/api/config", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    }),
  );

  await page.route("**/api/backends", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([
        {
          name: "shell",
          available: true,
          display_name: "Shell",
          configured: true,
        },
      ]),
    }),
  );

  await page.route("**/api/health", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    }),
  );

  await page.route("**/api/auth/token", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    }),
  );

  await page.route("**/api/workspaces/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const json = (body: string, contentType = "application/json") =>
      route.fulfill({ status: 200, contentType, body });

    if (
      pathname === "/api/workspaces/active" ||
      pathname.match(/^\/api\/workspaces\/[^/]+\/?$/)
    ) {
      return json(ok(staleWorkspaceData));
    }

    if (pathname.match(/\/api\/workspaces\/[^/]+\/events/)) {
      return route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
        body: 'event: connected\ndata: {"message":"connected"}\n\n',
      });
    }

    if (pathname.match(/\/files\/checkouts$/)) {
      return json(JSON.stringify(checkouts));
    }

    if (pathname.match(/\/files\/capabilities$/)) {
      return json(
        JSON.stringify({ read: true, write: true, sensitive: false }),
      );
    }

    if (pathname.includes("/config/backend")) {
      return json(
        ok({
          backend: "shell",
          source: "workspace",
          available: ["shell"],
          agents: [],
        }),
      );
    }

    if (pathname.match(/\/terminal\/state$/)) {
      return json(JSON.stringify({ active_tab: "" }));
    }

    return json(ok([]));
  });
}

function persistedTabPaths(page: Page): Promise<string[]> {
  return page.evaluate((key) => {
    const raw = window.localStorage.getItem(key);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as {
      groups?: { tabs?: { path: string }[] }[];
    };
    return (parsed.groups ?? []).flatMap((g) =>
      (g.tabs ?? []).map((t) => t.path),
    );
  }, TABS_KEY);
}

test.describe("File tabs across a workspace switch", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await page.setViewportSize({ width: 1440, height: 900 });
  });

  test("keeps tabs when the ref universe belongs to another workspace", async ({
    page,
  }) => {
    await page.goto(`/ws/${WS}/files`, { waitUntil: "domcontentloaded" });
    await expect(page.locator('nav[aria-label="Primary"]')).toBeVisible();

    // The explorer has mounted and settled its checkout listing — the point at
    // which the unguarded store would prune.
    await expect(
      page.getByRole("heading", { level: 2, name: "Repos", exact: true }),
    ).toBeVisible();

    // The tab is still open...
    await expect(page.getByRole("tab", { name: /README\.md/ })).toBeVisible();

    // ...and, the part that actually hurts, still persisted. Pruning writes
    // through, so a lost tab here does not come back on reload.
    await expect
      .poll(() => persistedTabPaths(page), {
        message: "persisted tabs were pruned against another workspace",
      })
      .toEqual(["README.md"]);
  });

  test("survives a reload in that state", async ({ page }) => {
    await page.goto(`/ws/${WS}/files`, { waitUntil: "domcontentloaded" });
    await expect(page.getByRole("tab", { name: /README\.md/ })).toBeVisible();

    await page.reload({ waitUntil: "domcontentloaded" });

    await expect(page.getByRole("tab", { name: /README\.md/ })).toBeVisible();
    expect(await persistedTabPaths(page)).toEqual(["README.md"]);
  });
});
