/**
 * E2E: WorkspaceTree sidebar drag-resize.
 *
 * Regression coverage for the mid-drag listener teardown bug: WorkspaceTree
 * re-renders on drag start (setIsResizing), which used to recreate the
 * onDragEnd callback and tear down the document pointer listeners, killing
 * the drag after the first pixel. A real pointer drag must keep widening the
 * sidebar across many pointermove events and persist the width (debounced).
 */

import { test, expect, type Page } from "@playwright/test";

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

const workspaceData = {
  id: WORKSPACE_ID,
  name: "default",
  path: "/tmp/test-ws",
  repos: [
    {
      name: "repo",
      path: "/repos/repo",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "default",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

async function setupMocks(page: Page): Promise<void> {
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();

      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }

      if (url.includes(WS_API + "/events")) {
        await route.abort();
        return;
      }

      const afterWs = url.split(WS_API)[1] || "";
      if (
        afterWs === "" ||
        afterWs === "/" ||
        afterWs.startsWith("?") ||
        afterWs.startsWith("/?")
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }

      if (afterWs.startsWith("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
    },
  );

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: [],
        tasks: {},
        agent_tasks: {},
        timestamp: "2026-06-05T00:00:00Z",
      }),
    });
  });
}

test("dragging the sidebar handle resizes continuously and persists", async ({
  page,
}) => {
  await setupMocks(page);
  await page.goto(`/ws/${WORKSPACE_ID}/kanban`);

  const handle = page.getByTestId("workspace-tree-resize-handle");
  await expect(handle).toBeVisible();

  // Nested <aside> wrappers both contain the handle; the innermost one
  // carries the width.
  const aside = page.locator("aside", { has: handle }).last();
  const before = (await aside.boundingBox())?.width ?? 0;

  const box = await handle.boundingBox();
  if (!box) throw new Error("resize handle has no bounding box");
  const startX = box.x + box.width / 2;
  const startY = box.y + box.height / 2;

  // Real drag: pointerdown, then many small moves. The teardown bug killed
  // the drag after the first re-render, so a multi-step move is the point.
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  for (let i = 1; i <= 10; i++) {
    await page.mouse.move(startX + i * 8, startY);
  }
  await page.mouse.up();

  const after = (await aside.boundingBox())?.width ?? 0;
  expect(after).toBeGreaterThan(before + 40);

  // Width persists to workspace-scoped storage (debounced ~200ms).
  await expect
    .poll(async () =>
      page.evaluate(
        (ws) => localStorage.getItem(`loom:${ws}:workspace-tree-width`),
        WORKSPACE_ID,
      ),
    )
    .toBe(String(Math.round(after)));
});
