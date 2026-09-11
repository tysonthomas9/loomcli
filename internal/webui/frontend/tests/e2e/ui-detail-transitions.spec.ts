import { expect, test, type Page, type Route } from "@playwright/test";

import { observeTransition } from "../helpers/ui-transition-probe";
import { ok, setupFleetMocks, workspacePath } from "./helpers/fleet";

const WS = "default";
const API = `/api/workspaces/${WS}`;

type DetailIssue = {
  id: string;
  title: string;
  description?: string;
  status: string;
  priority: number;
  issue_type: string;
  created_at: string;
  updated_at: string;
  comments: unknown[];
  dependencies: Array<DetailIssue & { dependency_type: string }>;
  dependents: unknown[];
};

function issue(id: string, title: string): DetailIssue {
  return {
    id,
    title,
    description: `${title} server description`,
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-09-10T00:00:00Z",
    updated_at: "2026-09-10T00:00:00Z",
    comments: [],
    dependencies: [],
    dependents: [],
  };
}

async function detailTransitionApp(page: Page, rows: DetailIssue[]) {
  await setupFleetMocks(page, rows);
  const heldDetails: Array<{ route: Route; snapshot: DetailIssue }> = [];
  const pendingSSE: Route[] = [];
  let holdDetails = false;
  let detailRequests = 0;

  await page.route(`**${API}/issues/*`, async (route) => {
    const request = route.request();
    if (request.method() !== "GET") return route.fallback();
    const id = new URL(request.url()).pathname.split("/").pop();
    const row = rows.find((candidate) => candidate.id === id);
    if (!row) return route.fallback();
    detailRequests += 1;
    const snapshot = structuredClone(row);
    if (holdDetails) {
      heldDetails.push({ route, snapshot });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(ok(snapshot)),
    });
  });

  await page.route("**/api/workspaces/*/events**", async (route) => {
    if (route.request().url().includes("/events/token")) {
      await route.fulfill({ status: 404, body: "open mode" });
      return;
    }
    pendingSSE.push(route);
  });

  return {
    get detailRequests() {
      return detailRequests;
    },
    get heldDetailCount() {
      return heldDetails.length;
    },
    holdDetails(value = true) {
      holdDetails = value;
    },
    async releaseDetail(position = 0, status = 200) {
      const entry = heldDetails.splice(position, 1)[0];
      if (!entry) throw new Error("No held detail request");
      await entry.route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(
          status === 200
            ? ok(entry.snapshot)
            : { success: false, error: `detail status ${status}` },
        ),
      });
    },
    async releaseAllDetails(status = 200) {
      while (heldDetails.length > 0) await this.releaseDetail(0, status);
    },
    async sendMutation(row: DetailIssue) {
      await expect.poll(() => pendingSSE.length).toBeGreaterThan(0);
      const route = pendingSSE.shift()!;
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: [
          `id: detail-${Date.now()}`,
          "event: mutation",
          `data: ${JSON.stringify({
            type: "update",
            issue_id: row.id,
            workspace_id: WS,
            timestamp: row.updated_at,
          })}`,
          "",
          "",
        ].join("\n"),
      });
    },
  };
}

async function restoreAgentTask(page: Page, taskId: string) {
  await page.addInitScript((selectedTaskId) => {
    localStorage.setItem(
      "loom:default:agent-work-panel-view:lead-1",
      JSON.stringify({
        statusFilter: "all",
        leadFilter: "all",
        taskSearch: "",
        expandedEpics: {},
        selectedTaskId,
      }),
    );
  }, taskId);
}

async function openDetailPanel(page: Page, row: DetailIssue) {
  await page.goto(workspacePath("/?groupBy=none"));
  await page.getByText(row.title, { exact: true }).first().click();
  const panel = page
    .getByTestId("issue-detail-overlay")
    .getByTestId("issue-detail-panel");
  await expect(panel.getByTestId("issue-id")).toContainText(row.id);
  return panel;
}

test("restored Agents task waits for eligible full detail @sse-ui-detail-transition @sse-ui-transition @T09-RESTORE", async ({
  page,
}) => {
  const a = issue("T09-A", "T09 task A");
  const b = issue("T09-B", "T09 task B");
  a.dependencies = [{ ...b, dependency_type: "blocks" }];
  const app = await detailTransitionApp(page, [a, b]);
  app.holdDetails();
  await restoreAgentTask(page, a.id);

  await page.goto("/ws/default/agents/lead-1");
  await expect.poll(() => app.heldDetailCount).toBeGreaterThan(0);
  const inlinePanel = page
    .getByTestId("agents-page")
    .getByTestId("issue-detail-panel");
  await expect(inlinePanel.getByTestId("panel-loading")).toBeVisible();
  await expect(inlinePanel.getByTestId("issue-id")).toHaveCount(0);

  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(inlinePanel.getByTestId("issue-id")).toContainText(a.id);
});

test("Agents dependency selection retires the previous detail until B loads @sse-ui-detail-transition @sse-ui-transition @T09-SWITCH", async ({
  page,
}) => {
  const a = issue("T09-A", "T09 task A");
  const b = issue("T09-B", "T09 task B");
  a.dependencies = [{ ...b, dependency_type: "blocks" }];
  const app = await detailTransitionApp(page, [a, b]);
  await restoreAgentTask(page, a.id);

  await page.goto("/ws/default/agents/lead-1");
  const inlinePanel = page
    .getByTestId("agents-page")
    .getByTestId("issue-detail-panel");
  await expect(inlinePanel.getByTestId("issue-id")).toContainText(a.id);
  app.holdDetails();
  await inlinePanel.getByRole("button", { name: new RegExp(b.title) }).click();
  await expect.poll(() => app.heldDetailCount).toBeGreaterThan(0);
  await expect(inlinePanel.getByTestId("panel-loading")).toBeVisible();
  await expect(inlinePanel.getByTestId("issue-id")).toHaveCount(0);

  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(inlinePanel.getByTestId("issue-id")).toContainText(b.id);
});

test("Agents same-task refresh preserves the loaded inline detail @sse-ui-detail-transition @sse-ui-transition @T09-REFRESH", async ({
  page,
}) => {
  const a = issue("T09-A", "T09 task A");
  const app = await detailTransitionApp(page, [a]);
  await restoreAgentTask(page, a.id);

  await page.goto("/ws/default/agents/lead-1");
  const inlinePanel = page
    .getByTestId("agents-page")
    .getByTestId("issue-detail-panel");
  await expect(inlinePanel.getByTestId("issue-id")).toContainText(a.id);
  const status = inlinePanel.getByRole("combobox", {
    name: "Change issue status",
  });
  await expect(status).toBeVisible();
  const probe = await observeTransition(page, {
    root: '[data-testid="agents-page"] [data-testid="issue-detail-panel"]',
    protectedNodes: ['select[aria-label="Change issue status"]'],
    forbiddenWithinRoot: ['[data-testid="panel-loading"]'],
    scope: { workspace: WS, selectedIssue: a.id, generation: 1 },
  });
  app.holdDetails();
  const refreshedTitle = "T09 task A refreshed";
  a.title = refreshedTitle;
  a.updated_at = "2026-09-10T00:00:01Z";
  const before = app.detailRequests;
  await app.sendMutation(a);
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await probe.assertSatisfied();

  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(inlinePanel).toHaveAttribute("data-loading", "false");
  await expect(inlinePanel.getByRole("heading", { level: 2 })).toHaveText(
    refreshedTitle,
  );
  await expect(status).toBeVisible();
  await probe.assertSatisfied();
  await probe.dispose();
});

test("Agents inaccessible detail cannot resurface from its collection row during retry @sse-ui-detail-transition @sse-ui-transition @T09-AUTH", async ({
  page,
}) => {
  const a = issue("T09-AUTH-A", "T09 inaccessible task");
  const app = await detailTransitionApp(page, [a]);
  await restoreAgentTask(page, a.id);
  await page.goto("/ws/default/agents/lead-1");
  const inlinePanel = page
    .getByTestId("agents-page")
    .getByTestId("issue-detail-panel");
  await expect(inlinePanel.getByTestId("issue-id")).toContainText(a.id);

  app.holdDetails();
  a.updated_at = "2026-09-10T00:00:01Z";
  let before = app.detailRequests;
  await app.sendMutation(a);
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await app.releaseAllDetails(403);
  await expect(inlinePanel.getByTestId("panel-error")).toBeVisible();
  await expect(inlinePanel.getByTestId("issue-id")).toHaveCount(0);

  a.updated_at = "2026-09-10T00:00:02Z";
  before = app.detailRequests;
  await app.sendMutation(a);
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await expect(inlinePanel.getByTestId("panel-loading")).toBeVisible();
  await expect(inlinePanel.getByTestId("issue-id")).toHaveCount(0);
  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(inlinePanel.getByTestId("issue-id")).toContainText(a.id);
});

test("unsaved description draft keeps value caret focus and active tab through refresh @sse-ui-detail-transition @sse-ui-transition @T08-DRAFT", async ({
  page,
}) => {
  const a = issue("T08-A", "T08 editable task");
  const app = await detailTransitionApp(page, [a]);
  const panel = await openDetailPanel(page, a);
  await panel.getByTestId("description-edit-button").click();
  const textarea = panel.getByTestId("description-textarea");
  const draft = "local unsaved draft remains here";
  await textarea.fill(draft);
  await textarea.evaluate((node: HTMLTextAreaElement) => {
    node.focus();
    node.setSelectionRange(6, 13);
  });
  const probe = await observeTransition(page, {
    root: '[data-testid="issue-detail-overlay"] [data-testid="issue-detail-panel"]',
    protectedNodes: ['textarea[data-testid="description-textarea"]'],
    forbiddenWithinRoot: ['[data-testid="panel-loading"]'],
    scope: { workspace: WS, selectedIssue: a.id, editor: "description" },
  });

  app.holdDetails();
  a.description = "new server description that must not replace the draft";
  a.updated_at = "2026-09-10T00:00:02Z";
  const before = app.detailRequests;
  await app.sendMutation(a);
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await expect(textarea).toHaveValue(draft);
  await expect(panel.getByRole("tab", { name: "Details" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect
    .poll(() =>
      textarea.evaluate((node: HTMLTextAreaElement) => ({
        active: document.activeElement === node,
        start: node.selectionStart,
        end: node.selectionEnd,
      })),
    )
    .toEqual({ active: true, start: 6, end: 13 });
  await probe.assertSatisfied();

  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(panel).toHaveAttribute("data-loading", "false");
  await expect(textarea).toHaveValue(draft);
  await expect
    .poll(() =>
      textarea.evaluate((node: HTMLTextAreaElement) => ({
        active: document.activeElement === node,
        start: node.selectionStart,
        end: node.selectionEnd,
      })),
    )
    .toEqual({ active: true, start: 6, end: 13 });
  await probe.assertSatisfied();
  await probe.dispose();
});

test("active Runs tab stays selected through same-task refresh @sse-ui-detail-transition @sse-ui-transition @T08-TAB", async ({
  page,
}) => {
  const a = issue("T08-TAB-A", "T08 tab task");
  const app = await detailTransitionApp(page, [a]);
  const panel = await openDetailPanel(page, a);
  const runsTab = panel.getByRole("tab", { name: "Runs" });
  await runsTab.click();
  await expect(runsTab).toHaveAttribute("aria-selected", "true");

  app.holdDetails();
  a.updated_at = "2026-09-10T00:00:03Z";
  const before = app.detailRequests;
  await app.sendMutation(a);
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await expect(runsTab).toHaveAttribute("aria-selected", "true");
  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(panel).toHaveAttribute("data-loading", "false");
  await expect(runsTab).toHaveAttribute("aria-selected", "true");
});

test("description draft from A is retired when selecting B @sse-ui-detail-transition @sse-ui-transition @T08-SWITCH", async ({
  page,
}) => {
  const a = issue("T08-SWITCH-A", "T08 draft task A");
  const b = issue("T08-SWITCH-B", "T08 draft task B");
  a.dependencies = [{ ...b, dependency_type: "blocks" }];
  const app = await detailTransitionApp(page, [a, b]);
  const panel = await openDetailPanel(page, a);
  await panel.getByTestId("description-edit-button").click();
  await panel.getByTestId("description-textarea").fill("draft only for A");
  app.holdDetails();
  await panel.getByRole("button", { name: new RegExp(b.title) }).click();
  await expect.poll(() => app.heldDetailCount).toBeGreaterThan(0);
  await expect(panel.getByTestId("panel-loading")).toBeVisible();
  await expect(panel.getByTestId("description-textarea")).toHaveCount(0);
  await expect(panel).not.toContainText("draft only for A");
  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(panel.getByTestId("issue-id")).toContainText(b.id);
  await expect(panel).not.toContainText("draft only for A");
});

test("standalone same-issue refresh keeps its loaded controls @sse-ui-detail-transition @sse-ui-transition @T10-REFRESH", async ({
  page,
}) => {
  const a = issue("T10-A", "T10 standalone task");
  const app = await detailTransitionApp(page, [a]);
  await page.goto(`/ws/${WS}/issues/${a.id}`);
  const view = page.getByTestId("issue-detail-view");
  await expect(view.getByTestId("detail-issue-id")).toContainText(a.id);
  const probe = await observeTransition(page, {
    root: '[data-testid="issue-detail-view"]',
    protectedNodes: ['select[aria-label="Change issue status"]'],
    forbiddenWithinRoot: ['[data-testid="detail-loading"]'],
    scope: { workspace: WS, selectedIssue: a.id, route: "standalone" },
  });
  app.holdDetails();
  const refreshedTitle = "T10 standalone task refreshed";
  a.title = refreshedTitle;
  a.updated_at = "2026-09-10T00:00:04Z";
  const before = app.detailRequests;
  await app.sendMutation(a);
  await expect.poll(() => app.detailRequests).toBeGreaterThan(before);
  await probe.assertSatisfied();
  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(view.getByTestId("detail-title")).toHaveText(refreshedTitle);
  await probe.assertSatisfied();
  await probe.dispose();
});

test("standalone A to B selection hides A until B detail loads @sse-ui-detail-transition @sse-ui-transition @T10-SWITCH", async ({
  page,
}) => {
  const a = issue("T10-A", "T10 standalone task A");
  const b = issue("T10-B", "T10 standalone task B");
  a.dependencies = [{ ...b, dependency_type: "blocks" }];
  const app = await detailTransitionApp(page, [a, b]);
  await page.goto(`/ws/${WS}/issues/${a.id}`);
  const view = page.getByTestId("issue-detail-view");
  await expect(view.getByTestId("detail-issue-id")).toContainText(a.id);
  app.holdDetails();
  await view.getByRole("button", { name: new RegExp(b.title) }).click();
  await expect(page).toHaveURL(new RegExp(`/issues/${b.id}$`));
  await expect.poll(() => app.heldDetailCount).toBeGreaterThan(0);
  await expect(view.getByTestId("detail-loading")).toBeVisible();
  await expect(view.getByTestId("detail-issue-id")).toHaveCount(0);
  await expect(view).not.toContainText(a.title);
  app.holdDetails(false);
  await app.releaseAllDetails();
  await expect(view.getByTestId("detail-issue-id")).toContainText(b.id);
});
