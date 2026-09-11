import {
  expect,
  test,
  type APIResponse,
  type Page,
  type Request,
  type Route,
} from "@playwright/test";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

import { observeTransition } from "../../helpers/ui-transition-probe";
import {
  closeTestIssueInWorkspace,
  createTestIssueInWorkspace,
  generateTestId,
  getWorkspaceById,
  patchWsIssue,
  BASE_URL,
  authHeaders,
} from "./helpers";
import {
  createSSEBrowserProbe,
  type SSEBrowserProbe,
} from "./sse-browser-probe";
import { createIsolatedSSEWorkspace } from "./sse-workspace";

const run = promisify(execFile);

test.skip(
  !process.env.RUN_INTEGRATION_TESTS,
  "Requires running paired services",
);
test.describe.configure({ mode: "serial" });

let workspace = "";
let repo = "";
let secondaryRepoPath = "";
let probe: SSEBrowserProbe | undefined;
const issues: string[] = [];

async function ownedLoomRuntime() {
  const project = process.env.LOCAL_MODE_COMPOSE_PROJECT ?? "";
  const engine = process.env.LOOM_SSE_TEST_CONTAINER_ENGINE ?? "";
  const container = process.env.LOOM_SSE_TEST_LOOM_CONTAINER ?? "";
  expect(project).toMatch(
    /^loomcli-(?:pg-browser|sse-ui-(?:redis|postgres))-[a-zA-Z0-9_-]+$/,
  );
  expect(["podman", "docker"]).toContain(engine);
  expect(container).toMatch(/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/);
  const { stdout } = await run(engine, [
    "inspect",
    "--format",
    '{{ index .Config.Labels "com.docker.compose.project" }}',
    container,
  ]);
  expect(
    stdout.trim(),
    "Loom container ownership label must match the selected project",
  ).toBe(project);
  return { engine, container };
}

test.beforeAll(async () => {
  workspace = await createIsolatedSSEWorkspace();
  const initial = await getWorkspaceById(workspace);
  expect(initial.data?.agents).toEqual([]);
  repo = initial.data?.repos[0]?.name ?? "";
  expect(repo, "Isolated workspace source repo").toBeTruthy();

  // A one-repo workspace treats its sole repo as "all selected", so it cannot
  // exercise the client-side repo filter that consumes Issue.repo. Add a
  // second, run-owned clone inside the already-isolated Loom container.
  const { engine, container } = await ownedLoomRuntime();
  const sourceRepo =
    process.env.LOOM_SSE_TEST_SOURCE_REPO || "/workspace/source-repo";
  expect(sourceRepo).toMatch(/^\/workspace\/[a-zA-Z0-9_.-]+$/);
  secondaryRepoPath = `/workspace/t11-secondary-${generateTestId()}`;
  await run(engine, [
    "exec",
    container,
    "git",
    "clone",
    "--quiet",
    sourceRepo,
    secondaryRepoPath,
  ]);
  const attached = await fetch(
    `${BASE_URL}/api/workspaces/${encodeURIComponent(workspace)}/repos`,
    {
      method: "POST",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ repos: [secondaryRepoPath] }),
      signal: AbortSignal.timeout(60_000),
    },
  );
  expect(attached.status, "Attach run-owned secondary repo").toBe(201);
  const body = await attached.json();
  expect(body.success).toBe(true);
  expect(body.data?.agents).toEqual([]);
  expect(body.data?.repos).toHaveLength(2);
});

test.beforeEach(async ({ page }) => {
  const response = await getWorkspaceById(workspace);
  expect(response.data?.agents).toEqual([]);
  expect(response.data?.repos).toHaveLength(2);
  probe = await createSSEBrowserProbe(page, workspace);
});

test.afterAll(async () => {
  if (!secondaryRepoPath) return;
  const { engine, container } = await ownedLoomRuntime();
  if (
    !/^\/workspace\/t11-secondary-test-[a-zA-Z0-9-]+$/.test(secondaryRepoPath)
  )
    throw new Error(
      "Refusing to remove a secondary repo outside run ownership",
    );
  await run(engine, ["exec", container, "rm", "-rf", "--", secondaryRepoPath]);
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
    for (const id of issues.splice(0)) {
      await closeTestIssueInWorkspace(workspace, id);
    }
  }
});

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

function successfulGraphReadsAfter(watermark: number) {
  probe!.assertHealthy();
  return probe!
    .completedReadsAfter(watermark, {
      path: `/api/workspaces/${encodeURIComponent(workspace)}/issues/graph`,
      method: "GET",
      status: 200,
    })
    .filter((completion) => completion.successfulSnapshot);
}

test("Filtered Graph retains its node until the causal authoritative refresh renders @sse-ui-transition @T11-GRAPH", async ({
  page,
}) => {
  const title = `SSE T11 Graph ${generateTestId()}`;
  const nextTitle = `${title} refreshed`;
  const id = await createTestIssueInWorkspace(workspace, title);
  issues.push(id);
  probe!.ownIssue(id);
  probe!.enrollRead(
    `/api/workspaces/${encodeURIComponent(workspace)}/issues/graph`,
  );

  await page.goto(
    `/ws/${encodeURIComponent(workspace)}/graph?repoFilter=${encodeURIComponent(repo)}`,
  );
  const node = page.getByRole("article", { name: `Issue: ${title}` });
  await expect(node).toBeVisible();
  await expect
    .poll(() => {
      probe!.assertHealthy();
      return probe!.frames.some((frame) => frame.event === "connected");
    })
    .toBe(true);

  let held:
    | {
        route: Route;
        response: APIResponse;
        settled: Promise<"finished" | "failed">;
      }
    | undefined;
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (
      !held &&
      request.method() === "GET" &&
      url.pathname ===
        `/api/workspaces/${encodeURIComponent(workspace)}/issues/graph` &&
      url.searchParams.get("source_repos") === repo
    ) {
      const settled = requestSettlement(page, request);
      held = {
        route,
        response: await route.fetch(),
        settled,
      };
      return;
    }
    await route.fallback();
  });

  const transition = await observeTransition(page, {
    root: '[data-testid="graph-view"]',
    protectedNodes: ['article[data-priority="2"]'],
    scope: { workspace, repository: repo, mode: "graph", generation: 1 },
  });
  const watermark = probe!.watermark();
  await patchWsIssue(workspace, id, { title: nextTitle });

  await expect
    .poll(() => {
      probe!.assertHealthy();
      return probe!.frames.some(
        (frame) =>
          frame.sequence > watermark &&
          frame.event === "mutation" &&
          frame.issueId === id,
      );
    })
    .toBe(true);
  await expect.poll(() => held !== undefined).toBe(true);
  await expect(node).toBeVisible();

  await held!.route.fulfill({ response: held!.response });
  expect(await held!.settled).toBe("finished");
  await expect
    .poll(() => successfulGraphReadsAfter(watermark).length)
    .toBeGreaterThan(0);
  await expect(
    page.getByRole("article", { name: `Issue: ${nextTitle}` }),
  ).toBeVisible();
  await transition.assertSatisfied();
  await transition.dispose();
});
