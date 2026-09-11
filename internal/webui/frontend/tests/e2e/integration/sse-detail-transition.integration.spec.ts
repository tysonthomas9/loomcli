import {
  expect,
  test,
  type APIResponse,
  type Request,
  type Route,
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

let workspace = "";
let probe: SSEBrowserProbe | undefined;
const issueIds: string[] = [];

test.beforeAll(async () => {
  workspace = await createIsolatedSSEWorkspace();
  await assertSSEWorkspaceHasNoAgents(workspace);
});

test.beforeEach(async ({ page }) => {
  await assertSSEWorkspaceHasNoAgents(workspace);
  probe = await createSSEBrowserProbe(page, workspace);
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
    for (const id of issueIds.splice(0)) {
      await closeTestIssueInWorkspace(workspace, id);
    }
  }
});

function requestSettlement(
  page: import("@playwright/test").Page,
  target: Request,
) {
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

test("real detail refresh preserves an unsaved description draft @sse-ui-transition @T08-DRAFT", async ({
  page,
}) => {
  const title = `SSE T08 draft ${generateTestId()}`;
  const id = await createTestIssueInWorkspace(workspace, title);
  issueIds.push(id);
  probe!.ownIssue(id);
  await page.goto(`/ws/${encodeURIComponent(workspace)}/kanban?groupBy=none`);
  await page.getByText(title, { exact: true }).first().click();
  const panel = page
    .getByTestId("issue-detail-overlay")
    .getByTestId("issue-detail-panel");
  await expect(panel.getByTestId("issue-id")).toContainText(id);
  await panel.getByTestId("description-edit-button").click();
  const textarea = panel.getByTestId("description-textarea");
  const draft = "paired unsaved draft remains here";
  await textarea.fill(draft);
  await textarea.evaluate((node: HTMLTextAreaElement) => {
    node.focus();
    node.setSelectionRange(7, 14);
  });
  const transition = await observeTransition(page, {
    root: '[data-testid="issue-detail-overlay"] [data-testid="issue-detail-panel"]',
    protectedNodes: ['textarea[data-testid="description-textarea"]'],
    forbiddenWithinRoot: ['[data-testid="panel-loading"]'],
    scope: { workspace, selectedIssue: id, editor: "description" },
  });

  let held:
    | {
        route: Route;
        response: APIResponse;
        settled: Promise<"finished" | "failed">;
      }
    | undefined;
  await page.route("**/*", async (route) => {
    const request = route.request();
    if (
      !held &&
      request.method() === "GET" &&
      new URL(request.url()).pathname ===
        `/api/workspaces/${encodeURIComponent(workspace)}/issues/${encodeURIComponent(id)}`
    ) {
      const settled = requestSettlement(page, request);
      held = { route, response: await route.fetch(), settled };
      return;
    }
    await route.fallback();
  });

  const watermark = probe!.watermark();
  const refreshedTitle = `${title} refreshed`;
  const response = await page.request.patch(
    `/api/workspaces/${encodeURIComponent(workspace)}/issues/${encodeURIComponent(id)}`,
    { data: { title: refreshedTitle } },
  );
  expect(response.status()).toBe(200);
  await expect
    .poll(
      () =>
        probe!.frames.filter(
          (frame) =>
            frame.sequence > watermark &&
            frame.event === "mutation" &&
            frame.workspaceId === workspace &&
            frame.issueId === id,
        ).length,
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  await expect.poll(() => !!held, { timeout: 15_000 }).toBe(true);
  await expect(textarea).toHaveValue(draft);
  await expect
    .poll(() =>
      textarea.evaluate((node: HTMLTextAreaElement) => ({
        active: document.activeElement === node,
        start: node.selectionStart,
        end: node.selectionEnd,
      })),
    )
    .toEqual({ active: true, start: 7, end: 14 });
  await transition.assertSatisfied();

  await held!.route.fulfill({ response: held!.response });
  expect(await held!.settled).toBe("finished");
  await expect
    .poll(
      () =>
        probe!.completedReadsAfter(watermark, {
          issueId: id,
          method: "GET",
          status: 200,
          successfulSnapshot: true,
        }).length,
      { timeout: 15_000 },
    )
    .toBeGreaterThan(0);
  await expect(panel.getByRole("heading", { level: 2 })).toHaveText(
    refreshedTitle,
  );
  await expect(textarea).toHaveValue(draft);
  await expect
    .poll(() =>
      textarea.evaluate((node: HTMLTextAreaElement) => ({
        active: document.activeElement === node,
        start: node.selectionStart,
        end: node.selectionEnd,
      })),
    )
    .toEqual({ active: true, start: 7, end: 14 });
  await transition.assertSatisfied();
  await transition.dispose();
});
