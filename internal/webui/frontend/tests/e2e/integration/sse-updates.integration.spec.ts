import { test, expect, type Page } from "@playwright/test";
import {
  generateTestId,
  createTestIssue,
  updateIssueStatus,
  closeTestIssue,
  resolveWorkspaceId,
} from "./helpers";
import {
  createSSEBrowserProbe,
  type SSEBrowserProbe,
} from "./sse-browser-probe";

// Real product fetch-SSE only. No page routes, response fixtures or extra stream.
test.skip(
  !process.env.RUN_INTEGRATION_TESTS,
  "Requires running paired services",
);
test.describe.configure({ mode: "serial" });

let probe: SSEBrowserProbe;
let workspace: string;
const issueIds: string[] = [];
let navigations = 0;

test.beforeEach(async ({ page }) => {
  workspace = await resolveWorkspaceId();
  probe = await createSSEBrowserProbe(page, workspace);
  navigations = 0;
  page.on("request", (request) => {
    if (
      request.isNavigationRequest() &&
      request.resourceType() === "document" &&
      request.frame() === page.mainFrame()
    )
      navigations++;
  });
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
      await info.attach("board", {
        body: await page.screenshot(),
        contentType: "image/png",
      });
      await probe.dispose();
    }
    for (const id of issueIds.splice(0)) await closeTestIssue(id);
  }
});

async function createIssue(title: string): Promise<string> {
  const id = await createTestIssue(title);
  issueIds.push(id);
  probe.ownIssue(id);
  return id;
}

async function gotoKanban(page: Page) {
  await page.goto(`/ws/${encodeURIComponent(workspace)}/kanban?groupBy=none`);
  await expect
    .poll(() => {
      probe.assertHealthy();
      return probe.frames.some((frame) => frame.event === "connected");
    })
    .toBe(true);
  probe.assertHealthy();
  await expect(
    page.getByRole("region", { name: "Open issues", exact: true }),
  ).toBeVisible();
  await expect
    .poll(() => {
      probe.assertHealthy();
      return probe.completions.length;
    })
    .toBeGreaterThan(0);
  expect(navigations).toBe(1);
}

function mutations(id: string) {
  probe.assertHealthy();
  const found = probe.frames.filter(
    (frame) => frame.event === "mutation" && frame.issueId === id,
  );
  for (const frame of found) {
    expect(frame.workspaceId).toBe(workspace);
    expect(frame.id).toMatch(/^c2\./);
    expect(frame.action).toMatch(
      /^issue\.(create|update|claim|release|close)$/,
    );
  }
  return found;
}

async function expectProjectionRefreshAfter(completed: number) {
  // Production debounces refresh for 1s, with a 5s maximum under a busy stream.
  await expect
    .poll(
      () => {
        probe.assertHealthy();
        return probe.completions.length;
      },
      { timeout: 10_000 },
    )
    .toBeGreaterThan(completed);
  probe.assertHealthy();
  expect(navigations).toBe(1);
}

test("fetch SSE establishes with a connected frame @smoke", async ({
  page,
}) => {
  await gotoKanban(page);
  expect(
    probe.requests.some(
      (request) =>
        request.path.endsWith("/events") &&
        request.status === 200 &&
        request.type === "Fetch" &&
        request.streamAttached,
    ),
  ).toBe(true);
  // Never swallow a failed alert assertion as the former test did.
  await expect(
    page.getByRole("alert").filter({ hasText: /error|failed/i }),
  ).toHaveCount(0);
});

test("API-created issue renders before projection refresh then converges @smoke", async ({
  page,
}) => {
  await createIssue(`SSE seed ${generateTestId()}`);
  await gotoKanban(page);
  const completed = probe.completions.length;
  const responses = probe.responses.length;
  const title = `SSE create ${generateTestId()}`;
  const id = await createIssue(title);
  await expect(
    page
      .getByRole("region", { name: "Open issues", exact: true })
      .getByText(title, { exact: true }),
  ).toBeVisible();
  expect(
    probe.responses.length,
    "SSE must render before a collection response can repair the view",
  ).toBe(responses);
  await expect.poll(() => mutations(id).length).toBe(1);
  await expectProjectionRefreshAfter(completed);
  expect(mutations(id)).toHaveLength(1);
});

test("status moves before projection refresh then converges @smoke", async ({
  page,
}) => {
  const title = `SSE status ${generateTestId()}`;
  const id = await createIssue(title);
  await gotoKanban(page);
  const open = page.getByRole("region", { name: "Open issues", exact: true });
  const active = page.getByRole("region", {
    name: "In Progress issues",
    exact: true,
  });
  await expect(open.getByText(title, { exact: true })).toBeVisible();
  const completed = probe.completions.length;
  const responses = probe.responses.length;
  await updateIssueStatus(id, "in_progress");
  await expect(active.getByText(title, { exact: true })).toBeVisible();
  await expect(open.getByText(title, { exact: true })).toHaveCount(0);
  expect(
    probe.responses.length,
    "Status application cannot be rescued by projection refetch",
  ).toBe(responses);
  await expect.poll(() => mutations(id).length).toBe(1);
  await expectProjectionRefreshAfter(completed);
  await expect(active.getByText(title, { exact: true })).toHaveCount(1);
  expect(mutations(id).map((frame) => frame.action)).toEqual(["issue.claim"]);
  const beforeRelease = probe.responses.length;
  const completedBeforeRelease = probe.completions.length;
  await updateIssueStatus(id, "open");
  await expect(open.getByText(title, { exact: true })).toBeVisible();
  expect(probe.responses.length).toBe(beforeRelease);
  await expect.poll(() => mutations(id).length).toBe(2);
  await expectProjectionRefreshAfter(completedBeforeRelease);
  expect(mutations(id).map((frame) => frame.action)).toEqual([
    "issue.claim",
    "issue.release",
  ]);
});

test("rapid creates each reach the browser exactly once in the observed interval @regression", async ({
  page,
}) => {
  await gotoKanban(page);
  const completed = probe.completions.length;
  const created: Array<{ id: string; title: string }> = [];
  for (let i = 0; i < 3; i++) {
    const title = `SSE rapid ${generateTestId()}`;
    created.push({ id: await createIssue(title), title });
  }
  for (const { id, title } of created) {
    await expect(
      page
        .getByRole("region", { name: "Open issues", exact: true })
        .getByText(title, { exact: true }),
    ).toHaveCount(1);
    await expect.poll(() => mutations(id).length).toBe(1);
  }
  await expectProjectionRefreshAfter(completed);
  const ids = created.map(({ id }) => mutations(id)[0].id);
  expect(new Set(ids).size).toBe(3);
  for (const { id } of created) expect(mutations(id)).toHaveLength(1);
});

test("close leaves Open through the observed stream without reload @regression", async ({
  page,
}) => {
  const title = `SSE close ${generateTestId()}`;
  const id = await createIssue(title);
  await gotoKanban(page);
  const open = page
    .getByRole("region", { name: "Open issues", exact: true })
    .getByText(title, { exact: true });
  await expect(open).toBeVisible();
  const completed = probe.completions.length;
  await closeTestIssue(id);
  issueIds.splice(issueIds.indexOf(id), 1);
  await expect(open).toHaveCount(0);
  await expect.poll(() => mutations(id).length).toBe(1);
  await expectProjectionRefreshAfter(completed);
  expect(mutations(id)).toHaveLength(1);
});
