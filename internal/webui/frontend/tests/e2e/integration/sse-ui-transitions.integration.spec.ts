import {
  expect,
  test,
  type Page,
  type Request,
  type Route,
  type APIResponse,
} from "@playwright/test";

import { observeTransition } from "../../helpers/ui-transition-probe";
import {
  closeTestIssueInWorkspace,
  createTestIssueInWorkspace,
  generateTestId,
} from "./helpers";
import {
  createSSEBrowserProbe,
  type SSEBrowserProbe,
} from "./sse-browser-probe";
import {
  assertSSEWorkspaceHasNoAgents,
  createIsolatedSSEWorkspace,
} from "./sse-workspace";

test.skip(
  !process.env.RUN_INTEGRATION_TESTS,
  "Requires running paired services",
);
test.describe.configure({ mode: "serial" });

let workspaceA = "";
let workspaceB = "";
let probe: SSEBrowserProbe | undefined;
const issues: Array<{ workspace: string; id: string }> = [];

test.beforeAll(async () => {
  workspaceA = await createIsolatedSSEWorkspace();
  workspaceB = await createIsolatedSSEWorkspace();
  await assertSSEWorkspaceHasNoAgents(workspaceA);
  await assertSSEWorkspaceHasNoAgents(workspaceB);
});

test.beforeEach(async ({ page }) => {
  await assertSSEWorkspaceHasNoAgents(workspaceA);
  await assertSSEWorkspaceHasNoAgents(workspaceB);
  probe = await createSSEBrowserProbe(page, workspaceA);
});

test.afterEach(async ({ page }, info) => {
  try {
    probe?.assertHealthy();
  } finally {
    if (probe) {
      await info.attach("actual-fetch-sse", {
        body: JSON.stringify(probe.snapshot(), null, 2),
        contentType: "application/json",
      });
      await info.attach("ui", {
        body: await page.screenshot(),
        contentType: "image/png",
      });
      await probe.dispose();
      probe = undefined;
    }
    for (const item of issues.splice(0)) {
      await closeTestIssueInWorkspace(item.workspace, item.id);
    }
  }
});

async function createIssue(workspace: string, title: string) {
  const id = await createTestIssueInWorkspace(workspace, title);
  issues.push({ workspace, id });
  if (workspace === workspaceA) probe!.ownIssue(id);
  return id;
}

async function gotoBoard(page: Page, workspace: string) {
  await page.goto(`/ws/${encodeURIComponent(workspace)}/kanban?groupBy=none`);
  await expect(
    page.getByRole("region", { name: "Open issues", exact: true }),
  ).toBeVisible();
  if (workspace === workspaceA) {
    await expect
      .poll(() => {
        probe!.assertHealthy();
        return probe!.frames.some((frame) => frame.event === "connected");
      })
      .toBe(true);
  }
}

function successfulReadsAfter(
  watermark: number,
  match: {
    path?: string;
    issueId?: string;
    status?: number;
    method?: string;
  },
) {
  probe!.assertHealthy();
  return probe!
    .completedReadsAfter(watermark, match)
    .filter((completion) => completion.successfulSnapshot);
}

function issueMutationsAfter(watermark: number, issueId: string) {
  probe!.assertHealthy();
  return probe!.frames.filter(
    (frame) =>
      frame.sequence > watermark &&
      frame.event === "mutation" &&
      frame.workspaceId === workspaceA &&
      frame.issueId === issueId,
  );
}

async function switchWorkspace(page: Page, workspace: string) {
  await page.getByRole("button", { name: /Active workspace:/ }).click();
  const dialog = page.getByRole("dialog", { name: "Switch workspace" });
  await expect(dialog).toBeVisible();
  await dialog
    .getByRole("searchbox", { name: "Search workspaces" })
    .fill(workspace);
  await dialog
    .locator("[data-workspace-item]")
    .filter({ hasText: workspace })
    .click();
  await expect(page).toHaveURL(
    new RegExp(`/ws/${encodeURIComponent(workspace)}/(?:home)?(?:[?#]|$)`),
  );
  await page
    .getByRole("navigation", { name: "Primary" })
    .getByRole("button", { name: "Workspaces", exact: true })
    .click();
  await expect(page).toHaveURL(
    new RegExp(`/ws/${encodeURIComponent(workspace)}/kanban(?:[?#]|$)`),
  );
}

function requestSettlement(page: Page, target: Request) {
  return new Promise<"finished" | "failed">((resolve) => {
    const cleanup = () => {
      page.off("requestfinished", finished);
      page.off("requestfailed", failed);
    };
    const finished = (request: Request) => {
      if (request !== target) return;
      cleanup();
      resolve("finished");
    };
    const failed = (request: Request) => {
      if (request !== target) return;
      cleanup();
      resolve("failed");
    };
    page.on("requestfinished", finished);
    page.on("requestfailed", failed);
  });
}

test("T01 real close keeps the loaded panel through causal detail refreshes @sse-ui-transition @T01", async ({
  page,
}) => {
  const title = `SSE T01 ${generateTestId()}`;
  const id = await createIssue(workspaceA, title);
  await gotoBoard(page, workspaceA);
  await page.getByText(title, { exact: true }).first().click();
  const panel = page.getByTestId("issue-detail-panel");
  const status = panel.getByRole("combobox", { name: "Change issue status" });
  await expect(status).toBeVisible();

  const transition = await observeTransition(page, {
    root: '[data-testid="issue-detail-panel"]',
    protectedNodes: ['select[aria-label="Change issue status"]'],
    forbiddenWithinRoot: ['[data-testid="panel-loading"]'],
    scope: { workspace: workspaceA, selectedIssue: id, generation: 1 },
  });
  const watermark = probe!.watermark();
  await status.selectOption("closed");

  await expect
    .poll(() => issueMutationsAfter(watermark, id).length)
    .toBeGreaterThan(0);
  await expect
    .poll(
      () =>
        successfulReadsAfter(watermark, {
          issueId: id,
          status: 200,
        }).length,
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  let collectionCompletion = 0;
  await expect
    .poll(
      () => {
        const reads = successfulReadsAfter(watermark, {
          path: `/api/workspaces/${encodeURIComponent(workspaceA)}/issues`,
          method: "GET",
          status: 200,
        });
        collectionCompletion = Math.max(
          collectionCompletion,
          ...reads.map((completion) => completion.finishedSequence),
        );
        return reads.length;
      },
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  await expect
    .poll(
      () =>
        successfulReadsAfter(collectionCompletion, {
          issueId: id,
          method: "GET",
          status: 200,
        }).length,
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  await expect(status).toHaveValue("closed");
  await transition.assertSatisfied();
  await transition.dispose();
  issues.splice(
    issues.findIndex((item) => item.id === id),
    1,
  );
});

test("T02 real populated collection remains mounted through refresh @sse-ui-transition @T02", async ({
  page,
}) => {
  const title = `SSE T02 ${generateTestId()}`;
  const id = await createIssue(workspaceA, title);
  await gotoBoard(page, workspaceA);
  const card = page.locator(`[aria-label="Issue: ${title}"]`);
  await expect(card).toBeVisible();
  const transition = await observeTransition(page, {
    root: "#main-content",
    protectedNodes: [`[aria-label="Issue: ${title}"]`],
    forbiddenWithinRoot: ['[data-testid="loading-container"]'],
    scope: { workspace: workspaceA, query: "groupBy=none", generation: 1 },
  });
  const watermark = probe!.watermark();
  const response = await page.request.patch(
    `/api/workspaces/${encodeURIComponent(workspaceA)}/issues/${encodeURIComponent(id)}`,
    { data: { priority: 1 } },
  );
  expect(response.status(), "Actual priority update response").toBe(200);

  await expect
    .poll(() => issueMutationsAfter(watermark, id).length)
    .toBeGreaterThan(0);
  await expect(card).toHaveAttribute("data-priority", "1");
  await expect
    .poll(
      () =>
        successfulReadsAfter(watermark, {
          path: `/api/workspaces/${encodeURIComponent(workspaceA)}/issues`,
          status: 200,
        }).length,
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  await transition.assertSatisfied();
  await transition.dispose();
});

test("T03 real failed collection refresh retains the snapshot until retry @sse-ui-transition @T03", async ({
  page,
}) => {
  const title = `SSE T03 ${generateTestId()}`;
  const id = await createIssue(workspaceA, title);
  await gotoBoard(page, workspaceA);
  const card = page.locator(`[aria-label="Issue: ${title}"]`);
  await expect(card).toBeVisible();
  const transition = await observeTransition(page, {
    root: "#main-content",
    protectedNodes: [`[aria-label="Issue: ${title}"]`],
    forbiddenWithinRoot: ['[data-testid="loading-container"]'],
    scope: { workspace: workspaceA, query: "groupBy=none", generation: 1 },
  });
  let collectionMode: "fail" | "hold" | "pass" = "fail";
  let failures = 0;
  let heldRetry:
    | {
        route: Route;
        response: APIResponse;
        settled: Promise<"finished" | "failed">;
      }
    | undefined;
  await page.route("**/*", async (route) => {
    const request = route.request();
    if (
      request.method() === "GET" &&
      new URL(request.url()).pathname ===
        `/api/workspaces/${encodeURIComponent(workspaceA)}/issues`
    ) {
      if (collectionMode === "fail") {
        failures++;
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            success: false,
            error: "injected collection failure",
          }),
        });
        return;
      }
      if (collectionMode === "hold") {
        const settled = requestSettlement(page, request);
        heldRetry = {
          route,
          response: await route.fetch(),
          settled,
        };
        collectionMode = "pass";
        return;
      }
      await route.fallback();
      return;
    }
    await route.fallback();
  });
  const watermark = probe!.watermark();
  const response = await page.request.patch(
    `/api/workspaces/${encodeURIComponent(workspaceA)}/issues/${encodeURIComponent(id)}`,
    { data: { priority: 1 } },
  );
  expect(response.status(), "Actual priority update response").toBe(200);

  await expect.poll(() => failures).toBeGreaterThan(0);
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute("data-priority", "2");
  await expect(
    page.getByRole("status").filter({ hasText: "Unable to refresh" }),
  ).toBeVisible();
  collectionMode = "hold";
  await expect.poll(() => !!heldRetry, { timeout: 15_000 }).toBe(true);
  await expect(card).toBeVisible();
  await expect(
    page.getByRole("status").filter({ hasText: "Unable to refresh" }),
  ).toBeVisible();
  await heldRetry!.route.fulfill({ response: heldRetry!.response });
  expect(await heldRetry!.settled).toBe("finished");
  await expect
    .poll(
      () =>
        successfulReadsAfter(watermark, {
          path: `/api/workspaces/${encodeURIComponent(workspaceA)}/issues`,
          status: 200,
        }).length,
      { timeout: 20_000 },
    )
    .toBeGreaterThan(0);
  await expect(
    page.getByRole("status").filter({ hasText: "Unable to refresh" }),
  ).toHaveCount(0);
  await expect(card).toHaveAttribute("data-priority", "1");
  await transition.assertSatisfied();
  await transition.dispose();
});

test("T04 real workspace A to B to A ignores the reversed B read @sse-ui-transition @T04", async ({
  page,
}) => {
  const titleA = `SSE T04 A ${generateTestId()}`;
  const titleB = `SSE T04 B ${generateTestId()}`;
  await createIssue(workspaceA, titleA);
  await createIssue(workspaceB, titleB);
  await gotoBoard(page, workspaceA);
  await expect(page.getByText(titleA, { exact: true }).first()).toBeVisible();

  const held: Array<{
    workspace: string;
    route: Route;
    response: APIResponse;
    settled: Promise<"finished" | "failed">;
  }> = [];
  let holdA = true;
  let holdB = true;
  await page.route("**/*", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    const matched = [workspaceA, workspaceB].find(
      (workspace) =>
        pathname === `/api/workspaces/${encodeURIComponent(workspace)}/issues`,
    );
    if (
      !matched ||
      request.method() !== "GET" ||
      (matched === workspaceA && !holdA) ||
      (matched === workspaceB && !holdB)
    ) {
      await route.fallback();
      return;
    }
    const settled = requestSettlement(page, request);
    held.push({
      workspace: matched,
      route,
      response: await route.fetch(),
      settled,
    });
  });

  const switchWatermark = probe!.watermark();
  await switchWorkspace(page, workspaceB);
  await expect
    .poll(() => held.filter((item) => item.workspace === workspaceB).length)
    .toBeGreaterThan(0);
  await switchWorkspace(page, workspaceA);
  await expect
    .poll(() => held.filter((item) => item.workspace === workspaceA).length)
    .toBeGreaterThan(0);

  holdA = false;
  for (const currentA of held.filter((item) => item.workspace === workspaceA)) {
    const outcome = await currentA.route
      .fulfill({ response: currentA.response })
      .then(() => "fulfilled" as const)
      .catch(() => "route-already-canceled" as const);
    const settlement = await currentA.settled;
    expect(
      outcome === "fulfilled" || settlement === "failed",
      "A current-scope route must either deliver completely or already be canceled",
    ).toBe(true);
    held.splice(held.indexOf(currentA), 1);
  }
  await expect
    .poll(
      () =>
        successfulReadsAfter(switchWatermark, {
          path: `/api/workspaces/${encodeURIComponent(workspaceA)}/issues`,
          method: "GET",
          status: 200,
        }).length,
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  await expect(page.getByText(titleA, { exact: true }).first()).toBeVisible();

  const currentTransition = await observeTransition(page, {
    root: "#main-content",
    forbiddenWithinRoot: [`[aria-label="Issue: ${titleB}"]`],
    scope: { workspace: workspaceA, query: "groupBy=none", generation: 2 },
  });

  holdB = false;
  for (const stale of held.filter((item) => item.workspace === workspaceB)) {
    const outcome = await stale.route
      .fulfill({ response: stale.response })
      .then(() => "fulfilled" as const)
      .catch(() => "route-already-canceled" as const);
    const settlement = await stale.settled;
    expect(
      outcome === "fulfilled" || settlement === "failed",
      "An obsolete route must either deliver completely or already be canceled",
    ).toBe(true);
    held.splice(held.indexOf(stale), 1);
  }
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  await expect(page.getByText(titleA, { exact: true }).first()).toBeVisible();
  await currentTransition.assertSatisfied();
  await currentTransition.dispose();
  expect(page.url()).toContain(`/ws/${encodeURIComponent(workspaceA)}/`);

  for (const pending of held)
    await pending.route.abort("failed").catch(() => {});
});
