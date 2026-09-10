import { expect, test, type Page, type Route } from "@playwright/test";

import { observeTransition } from "../helpers/ui-transition-probe";
import { ok, setupFleetMocks, workspacePath } from "./helpers/fleet";

const WS = "default";
const API = `/api/workspaces/${WS}`;

type MockIssue = {
  id: string;
  title: string;
  status: string;
  priority: number;
  issue_type: string;
  created_at: string;
  updated_at: string;
  dependencies: Array<
    Pick<
      MockIssue,
      "id" | "title" | "status" | "priority" | "created_at" | "updated_at"
    > & {
      dependency_type: string;
    }
  >;
  dependents: unknown[];
};

function issue(id: string, title: string): MockIssue {
  return {
    id,
    title,
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
    dependencies: [],
    dependents: [],
  };
}

async function mockedApp(page: Page, rows: MockIssue[]) {
  await setupFleetMocks(page, rows);
  const pendingDetails: Array<{
    route: Route;
    id: string;
    snapshot: MockIssue;
  }> = [];
  const pendingCollections: Array<{ route: Route; snapshot: MockIssue[] }> = [];
  const pendingSSE: Route[] = [];
  let holdDetails = false;
  let holdCollections = false;
  let nextCollectionStatus: number | null = null;
  let detailStatus = 200;
  let detailRequests = 0;
  let collectionRequests = 0;
  let sseConnections = 0;

  await page.route("**/*", async (route) => {
    const request = route.request();
    if (
      request.method() !== "GET" ||
      new URL(request.url()).pathname !== `${API}/issues`
    )
      return route.fallback();
    collectionRequests++;
    if (nextCollectionStatus !== null) {
      const status = nextCollectionStatus;
      nextCollectionStatus = null;
      await route.fulfill({ status, body: "injected collection failure" });
      return;
    }
    if (holdCollections) {
      pendingCollections.push({
        route,
        snapshot: rows.map((row) => ({
          ...row,
          dependencies: [...row.dependencies],
          dependents: [],
        })),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(rows)),
    });
  });

  await page.route(`**${API}/issues/*`, async (route) => {
    const request = route.request();
    const id = new URL(request.url()).pathname.split("/").pop()!;
    const row = rows.find((candidate) => candidate.id === id);
    if (!row) return route.fallback();
    if (request.method() === "PATCH") {
      Object.assign(row, request.postDataJSON(), {
        updated_at: new Date().toISOString(),
      });
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(row)),
      });
      return;
    }
    if (request.method() !== "GET") return route.fallback();
    detailRequests++;
    if (holdDetails) {
      pendingDetails.push({
        route,
        id,
        snapshot: {
          ...row,
          dependencies: [...row.dependencies],
          dependents: [],
        },
      });
      return;
    }
    await route.fulfill({
      status: detailStatus,
      contentType: "application/json",
      body: JSON.stringify(
        detailStatus === 200
          ? ok(row)
          : { success: false, error: `detail status ${detailStatus}` },
      ),
    });
  });

  await page.route("**/api/workspaces/*/events**", async (route) => {
    if (route.request().url().includes("/events/token"))
      return route.fulfill({ status: 404, body: "open mode" });
    sseConnections++;
    pendingSSE.push(route);
  });

  await page.route("**/api/workspaces/*/monitor/status**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        workspace: { mode: "workspace", name: "Default" },
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
        stats: {
          open: rows.length,
          closed: 0,
          total: rows.length,
          completion: 0,
          remaining: rows.length,
          in_progress: 0,
          review: 0,
          blocked: 0,
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
  });

  const fulfillDetail = async (
    entry: { route: Route; id: string; snapshot: MockIssue },
    status = 200,
  ) => {
    await entry.route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(
        status === 200
          ? ok(entry.snapshot)
          : { success: false, error: `detail status ${status}` },
      ),
    });
  };

  return {
    rows,
    get detailRequests() {
      return detailRequests;
    },
    get collectionRequests() {
      return collectionRequests;
    },
    get pendingDetailCount() {
      return pendingDetails.length;
    },
    get pendingCollectionCount() {
      return pendingCollections.length;
    },
    get pendingDetailIds() {
      return pendingDetails.map(({ id }) => id);
    },
    get pendingDetailTitles() {
      return pendingDetails.map(({ snapshot }) => snapshot.title);
    },
    get pendingCollectionTitles() {
      return pendingCollections.map(({ snapshot }) =>
        snapshot.map(({ title }) => title),
      );
    },
    get sseConnections() {
      return sseConnections;
    },
    holdDetails(value = true) {
      holdDetails = value;
    },
    holdCollections(value = true) {
      holdCollections = value;
    },
    failNextCollection(status = 503) {
      nextCollectionStatus = status;
    },
    setDetailStatus(status: number) {
      detailStatus = status;
    },
    async releaseDetail(status = 200) {
      const entry = pendingDetails.shift();
      if (!entry) throw new Error("No held detail request");
      await fulfillDetail(entry, status);
    },
    async releaseAllDetails(status = 200) {
      while (pendingDetails.length) await this.releaseDetail(status);
    },
    async releaseDetailFor(id: string, position: "first" | "last" = "first") {
      const matches = pendingDetails
        .map((entry, index) => ({ entry, index }))
        .filter(({ entry }) => entry.id === id);
      const match = position === "last" ? matches.at(-1) : matches[0];
      if (!match) throw new Error(`No held detail request for ${id}`);
      pendingDetails.splice(match.index, 1);
      await fulfillDetail(match.entry);
    },
    async releaseCollection(position = 0) {
      const entry = pendingCollections.splice(position, 1)[0];
      if (!entry) throw new Error("No held collection request");
      await entry.route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(entry.snapshot)),
      });
    },
    async releaseAllCollections() {
      while (pendingCollections.length) await this.releaseCollection();
    },
    async releaseCollectionContaining(
      title: string,
      position: "first" | "last" = "first",
    ) {
      const matches = pendingCollections
        .map((entry, index) => ({ entry, index }))
        .filter(({ entry }) =>
          entry.snapshot.some((row) => row.title === title),
        );
      const match = position === "last" ? matches.at(-1) : matches[0];
      if (!match)
        throw new Error(`No held collection request containing ${title}`);
      await this.releaseCollection(match.index);
    },
    async send(event: "connected" | "mutation" | "resync", data: unknown) {
      await expect
        .poll(() => pendingSSE.length, { timeout: 10_000 })
        .toBeGreaterThan(0);
      const route = pendingSSE.shift()!;
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: `id: mocked-${Date.now()}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`,
      });
    },
  };
}

async function openPanel(page: Page, row: MockIssue) {
  await page.goto(workspacePath("/?groupBy=none"));
  await expect(
    page.getByText(row.title, { exact: true }).first(),
  ).toBeVisible();
  await page.getByText(row.title, { exact: true }).first().click();
  await expect(page.getByTestId("issue-id")).toContainText(row.id);
}

test("T01 close through loaded panel retains controls through both refresh cycles @sse-ui-transition @T01", async ({
  page,
}) => {
  const row = issue("T01-A", "T01 retained panel");
  const app = await mockedApp(page, [row]);
  await openPanel(page, row);
  await app.send("connected", {});
  const probe = await observeTransition(page, {
    root: '[data-testid="issue-detail-panel"]',
    protectedNodes: ['select[aria-label="Change issue status"]'],
    forbiddenWithinRoot: ['[data-testid="panel-loading"]'],
    scope: { workspace: WS, selectedIssue: row.id, generation: 1 },
  });
  app.holdDetails();
  app.holdCollections();
  const before = app.detailRequests;
  await page
    .getByRole("combobox", { name: "Change issue status" })
    .selectOption("closed");
  row.priority = 1;
  row.updated_at = new Date(Date.now() + 1_000).toISOString();
  await app.send("mutation", {
    type: "status",
    issue_id: row.id,
    workspace_id: WS,
    old_status: "open",
    new_status: "closed",
    timestamp: new Date().toISOString(),
  });
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await expect.poll(() => app.pendingCollectionCount).toBeGreaterThan(0);
  await probe.assertSatisfied();
  await app.releaseAllDetails();
  await expect.poll(() => app.pendingDetailCount).toBe(0);
  await app.releaseAllCollections();
  await expect
    .poll(() => app.pendingDetailCount, { timeout: 10_000 })
    .toBeGreaterThan(0);
  await app.releaseAllDetails();
  await expect(
    page.getByRole("combobox", { name: "Change issue status" }),
  ).toHaveValue("closed");
  await probe.assertSatisfied();
  await probe.dispose();
});

test("T02 populated collection stays mounted during same-scope refresh @sse-ui-transition @T02", async ({
  page,
}) => {
  const row = issue("T02-A", "T02 retained card");
  const app = await mockedApp(page, [row]);
  await page.goto(workspacePath("/?groupBy=none"));
  await expect(
    page.getByText(row.title, { exact: true }).first(),
  ).toBeVisible();
  await app.send("connected", {});
  const probe = await observeTransition(page, {
    root: "#main-content",
    forbiddenWithinRoot: ['[data-testid="loading-container"]'],
    scope: { workspace: WS, query: "groupBy=none", generation: 1 },
  });
  app.holdCollections();
  const before = app.collectionRequests;
  await app.send("mutation", {
    type: "status",
    issue_id: row.id,
    workspace_id: WS,
    old_status: "open",
    new_status: "in_progress",
    timestamp: new Date().toISOString(),
  });
  await expect
    .poll(() => app.collectionRequests, { timeout: 10_000 })
    .toBeGreaterThan(before);
  await expect(
    page.getByText(row.title, { exact: true }).first(),
  ).toBeVisible();
  await probe.assertSatisfied();
  await app.releaseAllCollections();
  await probe.assertSatisfied();

  // Converge to an authoritative empty snapshot, then invalidate that empty
  // snapshot again. The empty board must remain mounted while its read waits.
  app.rows.splice(0, app.rows.length);
  app.holdCollections(false);
  const beforeEmpty = app.collectionRequests;
  await app.send("mutation", {
    type: "status",
    issue_id: row.id,
    workspace_id: WS,
    old_status: "in_progress",
    new_status: "closed",
    timestamp: new Date().toISOString(),
  });
  await expect
    .poll(() => app.collectionRequests, { timeout: 10_000 })
    .toBeGreaterThan(beforeEmpty);
  await expect(page.getByTestId("empty-workspace-board")).toBeVisible();
  app.holdCollections();
  const emptyRefresh = app.collectionRequests;
  await app.send("mutation", {
    type: "update",
    issue_id: "not-in-projection",
    workspace_id: WS,
    timestamp: new Date().toISOString(),
  });
  await expect
    .poll(() => app.collectionRequests, { timeout: 10_000 })
    .toBeGreaterThan(emptyRefresh);
  await expect(page.getByTestId("empty-workspace-board")).toBeVisible();
  await probe.assertSatisfied();
  await app.releaseAllCollections();
  await probe.dispose();
});

test("T03 failed collection refresh retains snapshot until retry repairs it @sse-ui-transition @T03", async ({
  page,
}) => {
  const row = issue("T03-A", "T03 stale card");
  const app = await mockedApp(page, [row]);
  await page.goto(workspacePath("/?groupBy=none"));
  await expect(
    page.getByText(row.title, { exact: true }).first(),
  ).toBeVisible();
  await app.send("connected", {});
  const probe = await observeTransition(page, {
    root: "#main-content",
    protectedNodes: [`[aria-label="Issue: ${row.title}"]`],
    forbiddenWithinRoot: ['[data-testid="loading-container"]'],
    scope: { workspace: WS, query: "groupBy=none", generation: 1 },
  });
  app.failNextCollection(503);
  const before = app.collectionRequests;
  await app.send("mutation", {
    type: "update",
    issue_id: row.id,
    workspace_id: WS,
    timestamp: new Date().toISOString(),
  });
  await expect
    .poll(() => app.collectionRequests, { timeout: 10_000 })
    .toBeGreaterThan(before);
  await expect(
    page.getByText(row.title, { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("status").filter({ hasText: "Unable to refresh" }),
  ).toBeVisible();
  await expect
    .poll(() => app.collectionRequests, { timeout: 15_000 })
    .toBeGreaterThan(before + 1);
  await expect(
    page.getByRole("status").filter({ hasText: "Unable to refresh" }),
  ).toHaveCount(0);
  await probe.assertSatisfied();
  await probe.dispose();
});

test("T04 A to B to A fences both obsolete responses @sse-ui-transition @T04", async ({
  page,
}) => {
  const a = issue("T04-A", "T04 issue A");
  const b = issue("T04-B", "T04 issue B");
  a.dependencies = [
    {
      id: b.id,
      title: b.title,
      status: b.status,
      priority: b.priority,
      created_at: b.created_at,
      updated_at: b.updated_at,
      dependency_type: "blocks",
    },
  ];
  const app = await mockedApp(page, [a, b]);
  await page.goto(`/ws/${WS}/issues/${a.id}`);
  const detail = page.getByTestId("issue-detail-view");
  await expect(
    detail.getByRole("combobox", { name: "Change issue status" }),
  ).toBeVisible();
  await expect(detail.getByTestId("detail-title")).toHaveText(a.title);
  await app.send("connected", {});
  app.holdDetails();
  a.title = "T04 issue A obsolete pending generation";
  await app.send("mutation", {
    type: "update",
    issue_id: a.id,
    workspace_id: WS,
    timestamp: new Date().toISOString(),
  });
  await expect
    .poll(() => app.pendingDetailIds.filter((id) => id === a.id).length)
    .toBeGreaterThanOrEqual(1);
  await expect
    .poll(() => app.pendingDetailTitles)
    .toContain("T04 issue A obsolete pending generation");
  a.title = "T04 issue A current generation";
  await detail.getByRole("button").filter({ hasText: b.id }).click();
  await expect(page).toHaveURL(`/ws/${WS}/issues/${b.id}`);
  await expect
    .poll(() => app.pendingDetailIds.filter((id) => id === b.id).length)
    .toBeGreaterThan(0);
  await page.goBack();
  await expect(page).toHaveURL(`/ws/${WS}/issues/${a.id}`);
  await expect
    .poll(() => app.pendingDetailTitles)
    .toContain("T04 issue A current generation");

  await app.releaseDetailFor(a.id, "last");
  await expect(detail.getByTestId("detail-title")).toHaveText(a.title);

  await page.evaluate(() => {
    const values: string[] = [];
    const collect = () => {
      const value = document.querySelector(
        '[data-testid="detail-title"]',
      )?.textContent;
      if (value) values.push(value);
    };
    const observer = new MutationObserver(collect);
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    (
      window as unknown as {
        __t04Titles: string[];
        __t04Observer: MutationObserver;
      }
    ).__t04Titles = values;
    (
      window as unknown as {
        __t04Titles: string[];
        __t04Observer: MutationObserver;
      }
    ).__t04Observer = observer;
  });
  while (app.pendingDetailIds.includes(a.id)) await app.releaseDetailFor(a.id);
  while (app.pendingDetailIds.includes(b.id)) await app.releaseDetailFor(b.id);
  await expect(detail.getByTestId("detail-title")).toHaveText(a.title);
  const renderedTitles = await page.evaluate(() => {
    const state = window as unknown as {
      __t04Titles: string[];
      __t04Observer: MutationObserver;
    };
    state.__t04Observer.takeRecords();
    state.__t04Observer.disconnect();
    return state.__t04Titles;
  });
  expect(renderedTitles).not.toContain(
    "T04 issue A obsolete pending generation",
  );
  expect(renderedTitles).not.toContain(b.title);
});

test("T05 authorization loss discards detail through pending retry @sse-ui-transition @T05", async ({
  page,
}) => {
  // Run 401 last because it intentionally retires authentication for the
  // entire browser context; 403/404 remain selection-local and can retry.
  for (const [index, status] of [403, 404, 401].entries()) {
    const casePage = index === 0 ? page : await page.context().newPage();
    const row = issue(`T05-${status}`, `T05 protected detail ${status}`);
    const app = await mockedApp(casePage, [row]);
    await casePage.goto(`/ws/${WS}/issues/${row.id}`);
    const detail = casePage.getByTestId("issue-detail-view");
    await expect(
      detail.getByRole("combobox", { name: "Change issue status" }),
    ).toBeVisible();
    await expect(detail.getByTestId("detail-title")).toHaveText(row.title);
    await app.send("connected", {});
    app.holdDetails();
    await app.send("mutation", {
      type: "update",
      issue_id: row.id,
      workspace_id: WS,
      timestamp: new Date().toISOString(),
    });
    await expect.poll(() => app.pendingDetailCount).toBeGreaterThan(0);
    await app.releaseAllDetails(status);
    await expect(detail.getByTestId("detail-title")).toHaveCount(0);
    if (status === 401) {
      // Global authentication retirement owns the transition after a 401;
      // this selection must not issue or display a same-owner retry.
      await expect(detail.getByTestId("detail-title")).toHaveCount(0);
      continue;
    }
    app.setDetailStatus(200);
    if (status === 404) {
      // A not-found result retires the selected query owner. A document retry
      // creates a new owner, which must still start without republishing the
      // discarded response.
      await casePage.goto(`/ws/${WS}/issues/${row.id}`, {
        waitUntil: "domcontentloaded",
      });
      await expect(casePage).toHaveURL(`/ws/${WS}/issues/${row.id}`);
    } else {
      await app.send("mutation", {
        type: "update",
        issue_id: row.id,
        workspace_id: WS,
        timestamp: new Date().toISOString(),
      });
    }
    await expect.poll(() => app.pendingDetailCount).toBeGreaterThan(0);
    await expect(detail.getByTestId("detail-title")).toHaveCount(0);
    await app.releaseAllDetails();
    await expect(detail.getByTestId("detail-title")).toHaveText(row.title);
    if (casePage !== page) await casePage.close();
  }
});

test("T07 resync fences obsolete reads and applies the recovery snapshot @sse-ui-transition @T07", async ({
  page,
}) => {
  const row = issue("T07-A", "T07 loaded card");
  const app = await mockedApp(page, [row]);
  await page.goto(workspacePath("/?groupBy=none"));
  await expect(
    page.getByText(row.title, { exact: true }).first(),
  ).toBeVisible();
  await app.send("connected", {});
  app.holdCollections();
  row.title = "T07 obsolete pre-fence card";
  const beforeObsolete = app.collectionRequests;
  await app.send("mutation", {
    type: "update",
    issue_id: row.id,
    workspace_id: WS,
    timestamp: new Date().toISOString(),
  });
  await expect
    .poll(() => app.collectionRequests)
    .toBeGreaterThan(beforeObsolete);
  await expect
    .poll(() => app.pendingCollectionTitles.flat())
    .toContain("T07 obsolete pre-fence card");

  row.title = "T07 recovered card";
  row.updated_at = new Date(Date.now() + 1_000).toISOString();
  const beforeRecovery = app.collectionRequests;
  await app.send("resync", { reason: "error" });
  await expect
    .poll(() => app.collectionRequests, { timeout: 10_000 })
    .toBeGreaterThan(beforeRecovery);
  await expect(page.getByText("T07 loaded card", { exact: true })).toHaveCount(
    0,
  );
  await expect
    .poll(
      () =>
        app.pendingCollectionTitles
          .flat()
          .filter((title) => title === "T07 recovered card").length,
    )
    .toBeGreaterThanOrEqual(2);

  // The resync dispatch starts an ordinary refresh, then the registered strict
  // recovery supersedes it. Release the newest response first: that is the
  // certified read which may reopen publication.
  await app.releaseCollectionContaining("T07 recovered card", "last");
  await expect(
    page.getByText("T07 recovered card", { exact: true }).first(),
  ).toBeVisible();
  const recoveredTransition = await observeTransition(page, {
    root: "#main-content",
    forbiddenWithinRoot: [
      '[aria-label="Issue: T07 loaded card"]',
      '[aria-label="Issue: T07 obsolete pre-fence card"]',
    ],
    scope: { workspace: WS, query: "groupBy=none", generation: 2 },
  });

  while (app.pendingCollectionTitles.flat().includes("T07 recovered card")) {
    await app.releaseCollectionContaining("T07 recovered card");
  }
  while (
    app.pendingCollectionTitles.flat().includes("T07 obsolete pre-fence card")
  ) {
    await app.releaseCollectionContaining("T07 obsolete pre-fence card");
  }
  await expect(
    page.getByText("T07 recovered card", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("T07 obsolete pre-fence card", { exact: true }),
  ).toHaveCount(0);
  await recoveredTransition.assertSatisfied();
  await recoveredTransition.dispose();
});
