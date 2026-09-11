import { expect, test, type Page, type Route } from "@playwright/test";

import { observeTransition } from "../helpers/ui-transition-probe";
import { ok, setupFleetMocks } from "./helpers/fleet";

const A = "collection-a";
const B = "collection-b";

type Endpoint = "issues" | "graph" | "ready" | "blocked";

type MockIssue = {
  id: string;
  title: string;
  repo: string;
  source_repo: string;
  status: string;
  priority: number;
  issue_type: string;
  created_at: string;
  updated_at: string;
  dependencies: unknown[];
  dependents: unknown[];
  blocked_by: string[];
  blocked_by_count: number;
};

type HeldRead = {
  route: Route;
  settled: Promise<"finished" | "failed">;
  workspace: string;
  endpoint: Endpoint;
  repos: string;
  snapshot: MockIssue[];
};

function issue(id: string, title: string, repo = "frontend"): MockIssue {
  return {
    id,
    title,
    repo,
    source_repo: repo,
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
    dependencies: [],
    dependents: [],
    blocked_by: [],
    blocked_by_count: 0,
  };
}

function copy(rows: MockIssue[]) {
  return rows.map((row) => ({ ...row, dependencies: [], dependents: [] }));
}

function workspaceData(id: string) {
  const name = id === A ? "Collection Alpha" : "Collection Beta";
  return {
    id,
    name,
    path: `/tmp/${id}`,
    repos: [
      { name: "frontend", path: `/tmp/${id}/frontend` },
      { name: "backend", path: `/tmp/${id}/backend` },
    ],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: A,
        name: "Collection Alpha",
        path: `/tmp/${A}`,
        active: id === A,
        repo_count: 2,
        is_default: true,
      },
      {
        id: B,
        name: "Collection Beta",
        path: `/tmp/${B}`,
        active: id === B,
        repo_count: 2,
        is_default: false,
      },
    ],
    workspace_order: [A, B],
    default_workspace: "Collection Alpha",
  };
}

function endpointFor(pathname: string): Endpoint | null {
  if (pathname.endsWith("/issues/graph")) return "graph";
  if (pathname.endsWith("/ready")) return "ready";
  if (pathname.endsWith("/blocked")) return "blocked";
  if (pathname.endsWith("/issues")) return "issues";
  return null;
}

function requestSettlement(page: Page, target: ReturnType<Route["request"]>) {
  return new Promise<"finished" | "failed">((resolve) => {
    const cleanup = () => {
      page.off("requestfinished", finished);
      page.off("requestfailed", failed);
    };
    const finished = (request: ReturnType<Route["request"]>) => {
      if (request !== target) return;
      cleanup();
      resolve("finished");
    };
    const failed = (request: ReturnType<Route["request"]>) => {
      if (request !== target) return;
      cleanup();
      resolve("failed");
    };
    page.on("requestfinished", finished);
    page.on("requestfailed", failed);
  });
}

async function collectionApp(page: Page) {
  const rows = new Map<string, MockIssue[]>([
    [`${A}:frontend`, [issue("A-FE", "Alpha frontend", "frontend")]],
    [`${A}:backend`, [issue("A-BE", "Alpha backend", "backend")]],
    [`${B}:frontend`, [issue("B-FE", "Beta frontend", "frontend")]],
    [`${B}:backend`, [issue("B-BE", "Beta backend", "backend")]],
  ]);
  const blocked = new Map<string, MockIssue[]>([
    [A, [issue("A-FE", "Alpha frontend", "frontend")]],
    [B, [issue("B-FE", "Beta frontend", "frontend")]],
  ]);
  const held: HeldRead[] = [];
  const pendingSSE: Route[] = [];
  const holds = new Set<Endpoint>();
  const reads: Array<{
    workspace: string;
    endpoint: Endpoint;
    repos: string;
    titles: string[];
  }> = [];
  let nextFailure: Endpoint | null = null;

  await setupFleetMocks(page, [
    ...rows.get(`${A}:frontend`)!,
    ...rows.get(`${A}:backend`)!,
  ]);

  const selectedRows = (workspace: string, repos: string) => {
    const selected = repos ? repos.split(",").sort() : ["backend", "frontend"];
    return selected.flatMap((repo) => rows.get(`${workspace}:${repo}`) ?? []);
  };

  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const { pathname } = url;

    if (pathname === "/api/config") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ mode: "open" }),
      });
    }
    if (pathname === "/api/backends") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      });
    }
    if (pathname === "/api/monitor/status") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          workspace: { mode: "workspace", name: workspaceData(A).name },
          agents: [],
          tasks: {
            needs_planning: 0,
            ready_to_implement: 0,
            in_progress: 0,
            need_review: 0,
            backlog: 0,
            epics: 0,
          },
          in_progress_list: [],
          agent_tasks: {},
          stats: { open: 2, closed: 0, total: 2, completion: 0 },
          sync: {
            db_synced: true,
            db_last_sync: new Date().toISOString(),
            git_needs_push: 0,
            git_needs_pull: 0,
          },
          timestamp: new Date().toISOString(),
        }),
      });
    }
    if (pathname === "/api/monitor/usage") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_input_tokens: 0,
          total_output_tokens: 0,
          total_cache_read_tokens: 0,
          total_cache_write_tokens: 0,
          total_cost: 0,
          session_count: 0,
          by_agent: [],
          by_backend: [],
          daily_costs: [],
          sessions: [],
          timestamp: new Date().toISOString(),
        }),
      });
    }
    if (pathname === "/api/workspaces/active") {
      const current = page.url().match(/\/ws\/([^/]+)/)?.[1] ?? A;
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(workspaceData(current))),
      });
    }

    const workspaceMatch = pathname.match(
      /^\/api\/workspaces\/([^/]+)(?:\/|$)/,
    );
    const workspace = workspaceMatch?.[1];
    if (workspace && pathname === `/api/workspaces/${workspace}`) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(workspaceData(workspace))),
      });
    }
    if (
      workspace &&
      pathname === `/api/workspaces/${workspace}/config/backend`
    ) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(
          ok({
            backend: "claude",
            source: "workspace",
            available: [],
            agents: [],
          }),
        ),
      });
    }
    if (workspace && pathname.endsWith("/events/token")) {
      return route.fulfill({ status: 404, body: "open mode" });
    }
    if (workspace && pathname.endsWith("/events")) {
      pendingSSE.push(route);
      return;
    }

    const endpoint = workspace ? endpointFor(pathname) : null;
    if (workspace && endpoint && request.method() === "GET") {
      const repos = (url.searchParams.get("source_repos") ?? "")
        .split(",")
        .filter(Boolean)
        .sort()
        .join(",");
      const snapshot = copy(
        endpoint === "blocked"
          ? (blocked.get(workspace) ?? []).filter(
              (row) => !repos || repos.split(",").includes(row.repo),
            )
          : selectedRows(workspace, repos),
      );
      reads.push({
        workspace,
        endpoint,
        repos,
        titles: snapshot.map((row) => row.title),
      });
      if (nextFailure === endpoint) {
        nextFailure = null;
        return route.fulfill({
          status: 503,
          body: "injected collection failure",
        });
      }
      if (holds.has(endpoint)) {
        held.push({
          route,
          settled: requestSettlement(page, request),
          workspace,
          endpoint,
          repos,
          snapshot,
        });
        return;
      }
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(snapshot)),
      });
    }

    if (workspace && pathname.endsWith("/stats")) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: 2,
          open_issues: 2,
          in_progress_issues: 0,
          closed_issues: 0,
          blocked_issues: 1,
          deferred_issues: 0,
          ready_issues: 1,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
          average_lead_time_hours: 0,
        }),
      });
    }
    if (workspace && pathname.endsWith("/monitor/status")) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          workspace: { mode: "workspace", name: workspaceData(workspace).name },
          agents: [],
          tasks: {},
          in_progress_list: [],
          agent_tasks: {},
          stats: {
            open: 2,
            closed: 0,
            total: 2,
            completion: 0,
            remaining: 2,
            in_progress: 0,
            review: 0,
            blocked: 1,
          },
          sync: {
            db_synced: true,
            db_last_sync: new Date().toISOString(),
            git_needs_push: 0,
            git_needs_pull: 0,
          },
          timestamp: new Date().toISOString(),
        }),
      });
    }
    if (
      workspace &&
      (pathname.endsWith("/terminal/tabs") || pathname.endsWith("/usage"))
    ) {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      });
    }
    if (pathname.startsWith("/api/")) {
      return route.fallback();
    }
    return route.continue();
  });

  const release = async (
    match: Partial<Pick<HeldRead, "workspace" | "endpoint" | "repos">>,
    position: "first" | "last" = "first",
  ) => {
    const matches = held
      .map((entry, index) => ({ entry, index }))
      .filter(({ entry }) =>
        Object.entries(match).every(
          ([key, value]) => entry[key as keyof HeldRead] === value,
        ),
      );
    const selected = position === "last" ? matches.at(-1) : matches[0];
    if (!selected)
      throw new Error(`No held read matches ${JSON.stringify(match)}`);
    held.splice(selected.index, 1);
    const delivery = await selected.entry.route
      .fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(selected.entry.snapshot)),
      })
      .then(() => "fulfilled" as const)
      .catch(() => "route-already-canceled" as const);
    const settled = await selected.entry.settled;
    if (delivery === "route-already-canceled" && settled !== "failed") {
      throw new Error("Held route was canceled without a failed settlement");
    }
    return settled;
  };

  return {
    rows,
    blocked,
    reads,
    held,
    hold(endpoint: Endpoint, enabled = true) {
      if (enabled) holds.add(endpoint);
      else holds.delete(endpoint);
    },
    failNext(endpoint: Endpoint) {
      nextFailure = endpoint;
    },
    release,
    async releaseAll() {
      while (held.length) {
        const entry = held[0]!;
        await release({
          workspace: entry.workspace,
          endpoint: entry.endpoint,
          repos: entry.repos,
        });
      }
    },
    async sendMutation(workspace = A) {
      await expect.poll(() => pendingSSE.length).toBeGreaterThan(0);
      // A scope change opens a replacement stream before the obsolete one is
      // necessarily torn down. Deliver to the newest/current stream.
      const route = pendingSSE.pop()!;
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: `id: collection-${Date.now()}\nevent: mutation\ndata: ${JSON.stringify({ type: "update", entity_type: "issue", entity_id: "changed", issue_id: "changed", workspace_id: workspace, source_repo: "frontend" })}\n\n`,
      });
    },
  };
}

async function settleFrames(page: Page) {
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
}

async function setViewScope(page: Page, path: string) {
  await page.evaluate((nextPath) => {
    window.history.pushState({}, "", nextPath);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, path);
  await expect(page).toHaveURL(new RegExp(`${path.replace("?", "\\?")}$`));
}

async function setRepoScope(page: Page, repo: string) {
  await page.evaluate((nextRepo) => {
    const url = new URL(window.location.href);
    url.searchParams.set("repoFilter", nextRepo);
    window.history.pushState({}, "", url);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, repo);
  await expect(page).toHaveURL(new RegExp(`[?&]repoFilter=${repo}(?:&|$)`));
}

async function switchWorkspace(page: Page, workspace: string) {
  const name = workspaceData(workspace).name;
  await page.getByRole("button", { name: /Active workspace:/ }).click();
  const dialog = page.getByRole("dialog", { name: "Switch workspace" });
  await dialog.getByRole("searchbox", { name: "Search workspaces" }).fill(name);
  await dialog
    .locator("[data-workspace-item]")
    .filter({ hasText: name })
    .click();
  await expect(page).toHaveURL(
    new RegExp(`/ws/${workspace}/(?:home)?(?:[?#]|$)`),
  );
  await page
    .getByRole("navigation", { name: "Primary" })
    .getByRole("button", { name: "Workspaces", exact: true })
    .click();
  await expect(page).toHaveURL(new RegExp(`/ws/${workspace}/kanban(?:[?#]|$)`));
}

test("List retains its loaded collection through pending and failed same-scope refreshes @sse-ui-transition @T11-LIST", async ({
  page,
}) => {
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/table`);
  const row = page.getByTestId("issue-row-A-FE");
  await expect(row).toBeVisible();
  app.hold("issues");
  await setRepoScope(page, "frontend");
  await expect
    .poll(() =>
      app.held.some(
        (read) => read.endpoint === "issues" && read.repos === "frontend",
      ),
    )
    .toBe(true);
  await app.release({ workspace: A, endpoint: "issues", repos: "frontend" });
  await expect(
    page.getByText("Alpha backend", { exact: true }),
  ).not.toBeVisible();
  expect(
    app.reads.some(
      (read) => read.endpoint === "issues" && read.repos === "frontend",
    ),
  ).toBe(true);
  const probe = await observeTransition(page, {
    root: '[data-testid="issue-table"]',
    protectedNodes: ['[data-testid="issue-row-A-FE"]'],
    forbiddenWithinRoot: ['[data-testid="loading-container"]'],
    scope: { workspace: A, mode: "table", generation: 1 },
  });
  await app.sendMutation();
  await expect
    .poll(() => app.held.filter((read) => read.endpoint === "issues").length)
    .toBeGreaterThan(0);
  await expect(row).toBeVisible();
  await app.release({ workspace: A, endpoint: "issues" });
  app.failNext("issues");
  await app.sendMutation();
  await expect(
    page.getByText("Unable to refresh — showing last known state.", {
      exact: false,
    }),
  ).toBeVisible();
  await expect(row).toBeVisible();
  await probe.assertSatisfied();
  await probe.dispose();
});

test("Graph retains its node identities during a same-scope collection refresh @sse-ui-transition @T11-GRAPH", async ({
  page,
}) => {
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/graph`);
  await expect(
    page.getByRole("article", { name: "Issue: Alpha frontend" }),
  ).toBeVisible();
  app.hold("graph");
  await setRepoScope(page, "frontend");
  await expect
    .poll(() =>
      app.held.some(
        (read) => read.endpoint === "graph" && read.repos === "frontend",
      ),
    )
    .toBe(true);
  await app.release({ workspace: A, endpoint: "graph", repos: "frontend" });
  const node = page.getByRole("article", { name: "Issue: Alpha frontend" });
  await expect(node).toBeVisible();
  await expect(
    page.getByRole("article", { name: "Issue: Alpha backend" }),
  ).not.toBeVisible();
  const probe = await observeTransition(page, {
    root: '[data-testid="graph-view"]',
    protectedNodes: ['article[data-priority="2"]'],
    forbiddenWithinRoot: ['[data-testid="loading-container"]'],
    scope: { workspace: A, mode: "graph", generation: 1 },
  });
  app.rows.get(`${A}:frontend`)![0]!.title = "Alpha frontend refreshed";
  await app.sendMutation();
  await expect
    .poll(() => app.held.filter((read) => read.endpoint === "graph").length)
    .toBeGreaterThan(0);
  await expect(node).toBeVisible();
  expect(await app.release({ workspace: A, endpoint: "graph" })).toBe(
    "finished",
  );
  await expect(
    page.getByRole("article", { name: "Issue: Alpha frontend refreshed" }),
  ).toBeVisible();
  await probe.assertSatisfied();
  await probe.dispose();
});

test("Blocked projection keeps the current graph node while its authority refreshes @sse-ui-transition @T11-BLOCKED", async ({
  page,
}) => {
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/graph`);
  await expect(
    page.getByRole("article", { name: "Issue: Alpha frontend" }),
  ).toHaveAttribute("data-is-ready", "false");
  app.hold("blocked");
  await setRepoScope(page, "frontend");
  await expect
    .poll(() =>
      app.held.some(
        (read) => read.endpoint === "blocked" && read.repos === "frontend",
      ),
    )
    .toBe(true);
  await app.release({ workspace: A, endpoint: "blocked", repos: "frontend" });
  const node = page.getByRole("article", { name: "Issue: Alpha frontend" });
  await expect(node).toHaveAttribute("data-is-ready", "false");
  const probe = await observeTransition(page, {
    root: '[data-testid="graph-view"]',
    protectedNodes: ['article[aria-label="Issue: Alpha frontend"]'],
    scope: { workspace: A, mode: "graph-blocked", generation: 1 },
  });
  app.blocked.set(A, []);
  await app.sendMutation();
  await expect
    .poll(() => app.held.filter((read) => read.endpoint === "blocked").length)
    .toBeGreaterThan(0);
  await expect(node).toHaveAttribute("data-is-ready", "false");
  await app.release({ workspace: A, endpoint: "blocked" });
  await expect(node).toHaveAttribute("data-is-ready", "true");
  await probe.assertSatisfied();
  await probe.dispose();
});

test("Returning to Graph re-enables the dormant blocked collection and applies new authority @sse-ui-transition @T11-DORMANT", async ({
  page,
}) => {
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/graph`);
  await expect(
    page.getByRole("article", { name: "Issue: Alpha frontend" }),
  ).toHaveAttribute("data-is-ready", "false");
  await setViewScope(page, `/ws/${A}/kanban?groupBy=none`);
  await expect(page.getByRole("region", { name: "Open issues" })).toBeVisible();
  app.blocked.set(A, []);
  const before = app.reads.filter((read) => read.endpoint === "blocked").length;
  await setViewScope(page, `/ws/${A}/graph`);
  await expect
    .poll(() => app.reads.filter((read) => read.endpoint === "blocked").length)
    .toBeGreaterThan(before);
  await expect(
    page.getByRole("article", { name: "Issue: Alpha frontend" }),
  ).toHaveAttribute("data-is-ready", "true");
});

test("Ready mode refreshes in the background without replacing the loaded Monitor surface @sse-ui-transition @T11-READY", async ({
  page,
}) => {
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/monitor`);
  const dashboard = page.getByTestId("monitor-dashboard");
  await expect(dashboard).toBeVisible();
  app.hold("ready");
  await setRepoScope(page, "frontend");
  await expect
    .poll(() =>
      app.held.some(
        (read) => read.endpoint === "ready" && read.repos === "frontend",
      ),
    )
    .toBe(true);
  await app.release({ workspace: A, endpoint: "ready", repos: "frontend" });
  expect(
    app.reads.some(
      (read) => read.endpoint === "ready" && read.repos === "frontend",
    ),
  ).toBe(true);
  const probe = await observeTransition(page, {
    root: '[data-testid="monitor-dashboard"]',
    protectedNodes: ['section[aria-labelledby="project-health-heading"]'],
    scope: { workspace: A, mode: "ready", generation: 1 },
  });
  await app.sendMutation();
  await expect
    .poll(() => app.held.filter((read) => read.endpoint === "ready").length)
    .toBeGreaterThan(0);
  await expect(dashboard).toBeVisible();
  expect(await app.release({ workspace: A, endpoint: "ready" })).toBe(
    "finished",
  );
  await settleFrames(page);
  await expect(dashboard).toBeVisible();
  await probe.assertSatisfied();
  await probe.dispose();
});

test("Repository A-B-A transitions reject reverse-order obsolete collection reads @sse-ui-transition @T04-REPO", async ({
  page,
}) => {
  await page.addInitScript(() => {
    const original = window.fetch;
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      if (init?.signal) {
        const { signal: _signal, ...rest } = init;
        return original.call(this, input, rest);
      }
      return original.call(this, input, init);
    };
  });
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/kanban?groupBy=none`);
  await expect(page.getByText("Alpha frontend", { exact: true })).toBeVisible();
  app.hold("issues");
  await setRepoScope(page, "frontend");
  await expect
    .poll(() => app.held.some((read) => read.repos === "frontend"))
    .toBe(true);
  await setRepoScope(page, "backend");
  await expect
    .poll(() => app.held.some((read) => read.repos === "backend"))
    .toBe(true);
  app.rows.get(`${A}:frontend`)![0]!.title = "Alpha frontend current";
  await setRepoScope(page, "frontend");
  await expect
    .poll(() => app.held.filter((read) => read.repos === "frontend").length)
    .toBeGreaterThan(1);
  expect(
    await app.release(
      { workspace: A, endpoint: "issues", repos: "frontend" },
      "last",
    ),
  ).toBe("finished");
  await expect(
    page.getByText("Alpha frontend current", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Alpha frontend", { exact: true }),
  ).not.toBeVisible();
  await expect(
    page.getByText("Alpha backend", { exact: true }),
  ).not.toBeVisible();
  const currentScope = await observeTransition(page, {
    root: "#main-content",
    forbiddenWithinRoot: [
      'article[aria-label="Issue: Alpha backend"]',
      'article[aria-label="Issue: Alpha frontend"]',
    ],
    scope: {
      workspace: A,
      repository: "frontend",
      mode: "kanban",
      generation: 3,
    },
  });
  expect(["finished", "failed"]).toContain(
    await app.release({ workspace: A, endpoint: "issues", repos: "backend" }),
  );
  expect(["finished", "failed"]).toContain(
    await app.release({ workspace: A, endpoint: "issues", repos: "frontend" }),
  );
  await settleFrames(page);
  await expect(
    page.getByText("Alpha frontend current", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Alpha frontend", { exact: true }),
  ).not.toBeVisible();
  await expect(
    page.getByText("Alpha backend", { exact: true }),
  ).not.toBeVisible();
  await currentScope.assertSatisfied();
  await currentScope.dispose();
});

test("Workspace A-B-A transitions reject reverse-order obsolete collection reads @sse-ui-transition @T04-WORKSPACE", async ({
  page,
}) => {
  await page.addInitScript(() => {
    const original = window.fetch;
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      if (init?.signal) {
        const { signal: _signal, ...rest } = init;
        return original.call(this, input, rest);
      }
      return original.call(this, input, init);
    };
  });
  const app = await collectionApp(page);
  await page.goto(`/ws/${A}/kanban?groupBy=none`);
  await expect(page.getByText("Alpha frontend", { exact: true })).toBeVisible();
  app.hold("issues");
  await switchWorkspace(page, B);
  await expect
    .poll(() => app.held.some((read) => read.workspace === B))
    .toBe(true);
  await switchWorkspace(page, A);
  await expect
    .poll(() => app.held.filter((read) => read.workspace === A).length)
    .toBeGreaterThan(0);
  await app.release({ workspace: A, endpoint: "issues" }, "last");
  await expect(page.getByText("Alpha frontend", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Beta frontend", { exact: true }),
  ).not.toBeVisible();
  const currentScope = await observeTransition(page, {
    root: "#main-content",
    forbiddenWithinRoot: ['article[aria-label="Issue: Beta frontend"]'],
    scope: { workspace: A, mode: "kanban", generation: 3 },
  });
  expect(["finished", "failed"]).toContain(
    await app.release({ workspace: B, endpoint: "issues" }),
  );
  await settleFrames(page);
  await expect(page.getByText("Alpha frontend", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Beta frontend", { exact: true }),
  ).not.toBeVisible();
  await currentScope.assertSatisfied();
  await currentScope.dispose();
  await app.releaseAll();
});
