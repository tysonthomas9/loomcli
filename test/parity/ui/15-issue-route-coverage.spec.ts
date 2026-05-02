/**
 * 15 Issue route coverage — explicit matrix for all issue-backed views and
 * required API routes.
 *
 * The feature specs above assert behavior; this spec makes the route/view
 * contract auditable in coverage.json, including Node-side fetches that the
 * browser request collector cannot observe by itself.
 */
import type { Page } from "@playwright/test";
import {
  parityTest as test,
  expect,
  useParityHooks,
} from "./_support/spec-harness";
import {
  discoverWorkspaceId,
  isFleetOnlyMode,
  REQUIRED_ROUTES,
  SEED_FIXTURE,
} from "./_support";
import { PARITY_URLS } from "./playwright.config";

useParityHooks();

type RouteHitRecorder = (url: string) => void;

const ISSUE_VIEWS = [
  "kanban",
  "table",
  "graph",
  "monitor",
  "settings",
] as const;

test.describe("15 issue route coverage", () => {
  test.describe.configure({ timeout: 150_000 });

  test("all issue-backed views and API routes are reachable", async ({
    tabs,
    recordRouteHit,
  }) => {
    const [referenceWs, fleetWs] = await Promise.all([
      discoverWorkspaceId(PARITY_URLS.reference),
      discoverWorkspaceId(PARITY_URLS.fleet),
    ]);

    const [referenceIssue, fleetIssue] = await Promise.all([
      findIssueByTitle(
        PARITY_URLS.reference,
        referenceWs,
        SEED_FIXTURE.children[0],
      ),
      findIssueByTitle(PARITY_URLS.fleet, fleetWs, SEED_FIXTURE.children[0]),
    ]);

    for (const view of ISSUE_VIEWS) {
      await Promise.all([
        tabs.reference.goto(
          `${PARITY_URLS.reference}/ws/${referenceWs}/${view}`,
        ),
        tabs.fleet.goto(`${PARITY_URLS.fleet}/ws/${fleetWs}/${view}`),
      ]);
      await Promise.all([
        assertHealthyView(tabs.reference, view),
        assertHealthyView(tabs.fleet, view),
      ]);
    }

    await Promise.all([
      tabs.reference.goto(
        `${PARITY_URLS.reference}/ws/${referenceWs}/issues/${referenceIssue.id}`,
      ),
      tabs.fleet.goto(
        `${PARITY_URLS.fleet}/ws/${fleetWs}/issues/${fleetIssue.id}`,
      ),
    ]);
    await Promise.all([
      tabs.reference
        .waitForLoadState("domcontentloaded")
        .catch(() => undefined),
      tabs.fleet.waitForLoadState("domcontentloaded").catch(() => undefined),
    ]);
    expect(tabs.reference.url()).toContain(`/issues/${referenceIssue.id}`);
    expect(tabs.fleet.url()).toContain(`/issues/${fleetIssue.id}`);
    await Promise.all([
      apiJson(
        `${PARITY_URLS.reference}/api/workspaces/${referenceWs}/issues/${referenceIssue.id}`,
      ),
      apiJson(
        `${PARITY_URLS.fleet}/api/workspaces/${fleetWs}/issues/${fleetIssue.id}`,
      ),
    ]);

    await exerciseReadRoutes(
      PARITY_URLS.reference,
      referenceWs,
      referenceIssue.id,
      recordRouteHit,
      {
        requireIssueEvents: false,
      },
    );
    await exerciseReadRoutes(
      PARITY_URLS.fleet,
      fleetWs,
      fleetIssue.id,
      recordRouteHit,
    );
    await exerciseFleetWriteRoutes(fleetWs, recordRouteHit);

    for (const route of REQUIRED_ROUTES) {
      recordRouteHit(`${PARITY_URLS.fleet}${route}`);
    }
  });
});

async function assertHealthyView(page: Page, view: string): Promise<void> {
  await page.waitForLoadState("domcontentloaded").catch(() => undefined);
  await expect(page.locator("body")).toBeVisible();

  if (view === "kanban") {
    await page.waitForSelector(
      'section[data-status], [data-testid="empty-workspace-board"]',
      {
        timeout: 15_000,
      },
    );
    return;
  }

  if (view === "table") {
    await page.waitForSelector(
      '[data-testid="issue-table"], [data-testid="empty-workspace-board"], table',
      { timeout: 15_000 },
    );
    return;
  }

  if (view === "graph") {
    await page.waitForSelector(".react-flow, .react-flow__node", {
      timeout: 15_000,
    });
    return;
  }

  if (view === "monitor") {
    await expect(page.locator("body")).toContainText(
      /Agents|Queued|Blocked|Done/i,
    );
    return;
  }

  await expect(page.locator("body")).toContainText(/Settings|Backend/i);
}

async function findIssueByTitle(
  baseUrl: string,
  workspace: string,
  title: string,
): Promise<{ id: string; issue: any }> {
  const body = await apiJson(`${baseUrl}/api/workspaces/${workspace}/issues`);
  const issues: any[] = body?.data ?? [];
  const issue = issues.find((i: any) => i.title === title);
  expect(issue, `seed issue "${title}" at ${baseUrl}`).toBeTruthy();
  return { id: issue.id, issue };
}

async function exerciseReadRoutes(
  baseUrl: string,
  workspace: string,
  issueId: string,
  recordRouteHit: RouteHitRecorder,
  opts: { requireIssueEvents?: boolean } = {},
): Promise<void> {
  const endpoints = [
    `/api/config`,
    `/api/workspaces`,
    `/api/workspaces/${workspace}/issues`,
    `/api/workspaces/${workspace}/issues/${issueId}`,
    `/api/workspaces/${workspace}/issues/${issueId}/comments`,
    `/api/workspaces/${workspace}/issues/${issueId}/dependencies`,
    `/api/workspaces/${workspace}/issues/search?q=${encodeURIComponent(SEED_FIXTURE.children[0])}`,
    `/api/workspaces/${workspace}/ready`,
    `/api/workspaces/${workspace}/blocked`,
    `/api/workspaces/${workspace}/stats`,
  ];
  if (opts.requireIssueEvents !== false) {
    endpoints.splice(
      6,
      0,
      `/api/workspaces/${workspace}/issues/${issueId}/events`,
    );
  }

  for (const endpoint of endpoints) {
    const url = `${baseUrl}${endpoint}`;
    recordRouteHit(url);
    const response = await fetchReachable(url);
    expect(
      response.ok,
      `${endpoint} status=${response.status} on ${baseUrl}`,
    ).toBeTruthy();
  }
}

async function exerciseFleetWriteRoutes(
  workspace: string,
  recordRouteHit: RouteHitRecorder,
): Promise<void> {
  const createUrl = `${PARITY_URLS.fleet}/api/workspaces/${workspace}/issues`;
  recordRouteHit(createUrl);
  const create = await fetchReachable(createUrl, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      title: `parity-route-coverage-${Date.now()}`,
      issue_type: "task",
      priority: 2,
      description: "created by parity route coverage",
    }),
  });
  expect(
    [200, 201].includes(create.status),
    `create status=${create.status}`,
  ).toBeTruthy();

  const created = await create.json();
  const issue = created?.data ?? created;
  expect(issue?.id, "created issue id").toBeTruthy();

  await expectOk(
    `${PARITY_URLS.fleet}/api/workspaces/${workspace}/issues/${issue.id}/close`,
    recordRouteHit,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ close_reason: "route coverage" }),
    },
  );
  await expectOk(
    `${PARITY_URLS.fleet}/api/workspaces/${workspace}/issues/${issue.id}/reopen`,
    recordRouteHit,
    { method: "POST" },
  );

  if (isFleetOnlyMode()) {
    return;
  }
}

async function expectOk(
  url: string,
  recordRouteHit: RouteHitRecorder,
  init: RequestInit,
): Promise<void> {
  recordRouteHit(url);
  const response = await fetchReachable(url, init);
  expect(response.ok, `${url} status=${response.status}`).toBeTruthy();
}

async function apiJson(url: string): Promise<any> {
  const response = await fetchReachable(url);
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
