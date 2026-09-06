import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { test, expect, type Page, type TestInfo } from "@playwright/test";
import {
  FRONTEND_BASE_URL,
  generateTestId,
  resolveWorkspaceId,
  createTestIssueInWorkspace,
  updateIssueStatusInWorkspace,
  closeTestIssueInWorkspace,
} from "./helpers";
import {
  createSSEBrowserProbe,
  type SSEBrowserProbe,
} from "./sse-browser-probe";

const run = promisify(execFile);
test.skip(
  !process.env.RUN_INTEGRATION_TESTS,
  "Requires running paired services",
);
test.describe.configure({ mode: "serial" });
let workspace = "";
const issueIds: string[] = [];

test.beforeAll(async () => {
  workspace = await resolveWorkspaceId();
});
test.afterEach(async () => {
  for (const id of issueIds.splice(0))
    await closeTestIssueInWorkspace(workspace, id);
});

async function createIssue(title: string) {
  const id = await createTestIssueInWorkspace(workspace, title);
  issueIds.push(id);
  return id;
}

async function observe(page: Page, owned: string[] = []) {
  const probe = await createSSEBrowserProbe(page, workspace);
  owned.forEach((id) => probe.ownIssue(id));
  let navigations = 0;
  page.on("request", (request) => {
    if (
      request.isNavigationRequest() &&
      request.resourceType() === "document" &&
      request.frame() === page.mainFrame()
    )
      navigations++;
  });
  await page.goto(`/ws/${encodeURIComponent(workspace)}/kanban?groupBy=none`);
  await expect
    .poll(() => {
      probe.assertHealthy();
      return probe.frames.some((frame) => frame.event === "connected");
    })
    .toBe(true);
  await expect
    .poll(() => {
      probe.assertHealthy();
      return probe.completions.length;
    })
    .toBeGreaterThan(0);
  probe.assertHealthy();
  return { page, probe, navigations: () => navigations };
}

type Client = Awaited<ReturnType<typeof observe>>;
function mutations(probe: SSEBrowserProbe, id: string) {
  probe.assertHealthy();
  const found = probe.frames.filter(
    (frame) => frame.event === "mutation" && frame.issueId === id,
  );
  for (const frame of found) {
    expect(frame.workspaceId).toBe(workspace);
    expect(frame.id).toMatch(/^c2\./);
    expect(frame.action).toMatch(
      /^issue\.(create|update|claim|release|assign)$/,
    );
  }
  return found;
}
async function visible(client: Client, column: string, title: string) {
  const card = client.page
    .getByRole("region", { name: column, exact: true })
    .getByText(title, { exact: true });
  await expect(card).toBeVisible();
  await expect(card).toHaveCount(1);
  expect(
    client.navigations(),
    "Recovery must never navigate/reload the page",
  ).toBe(1);
  client.probe.assertHealthy();
}
async function attach(info: TestInfo, clients: Client[]) {
  for (const [index, client] of clients.entries()) {
    await info.attach(`client-${index + 1}-actual-fetch`, {
      body: JSON.stringify(client.probe.snapshot(), null, 2),
      contentType: "application/json",
    });
    await info.attach(`client-${index + 1}-board`, {
      body: await client.page.screenshot(),
      contentType: "image/png",
    });
    await client.probe.dispose();
  }
}

// Fault injection must target an explicitly selected, run-owned local-mode proxy.
// No browser response interception, offline emulation or navigation fallback.
async function proxyAction(action: "stop" | "start") {
  const project = process.env.LOCAL_MODE_COMPOSE_PROJECT;
  const container = process.env.LOOM_SSE_TEST_PROXY_CONTAINER;
  if (
    !project?.startsWith("loomcli-pg-browser-") ||
    container !== `${project}-ui-local-1`
  ) {
    throw new Error(
      "Select an isolated loomcli-pg-browser-* project and its exact ui-local-1 container",
    );
  }
  const { stdout } = await run("podman", [
    "inspect",
    "--format",
    '{{ index .Config.Labels "com.docker.compose.project" }}',
    container,
  ]);
  expect(
    stdout.trim(),
    "Proxy ownership label must match the selected project",
  ).toBe(project);
  await run("podman", [action, container]);
}

test("create and status frames reach two independent clients once @regression", async ({
  browser,
}, info) => {
  const contexts = await Promise.all([
    browser.newContext({ baseURL: FRONTEND_BASE_URL }),
    browser.newContext({ baseURL: FRONTEND_BASE_URL }),
  ]);
  const clients: Client[] = [];
  try {
    for (const context of contexts)
      clients.push(await observe(await context.newPage()));
    const title = `SSE two clients ${generateTestId()}`;
    const id = await createIssue(title);
    clients.forEach(({ probe }) => probe.ownIssue(id));
    for (const client of clients) {
      await visible(client, "Open issues", title);
      await expect.poll(() => mutations(client.probe, id).length).toBe(1);
    }
    const completed = clients.map(({ probe }) => probe.completions.length);
    await updateIssueStatusInWorkspace(workspace, id, "in_progress");
    for (const [index, client] of clients.entries()) {
      await visible(client, "In Progress issues", title);
      await expect.poll(() => mutations(client.probe, id).length).toBe(2);
      await expect
        .poll(() => client.probe.completions.length, { timeout: 10_000 })
        .toBeGreaterThan(completed[index]);
      expect(mutations(client.probe, id)).toHaveLength(2);
    }
    const ids = clients.map(({ probe }) =>
      mutations(probe, id).map((frame) => frame.id),
    );
    expect(ids[0]).toEqual(ids[1]);
    expect(new Set(ids[0]).size).toBe(2);
    for (const client of clients)
      expect(mutations(client.probe, id).map((frame) => frame.action)).toEqual([
        "issue.create",
        "issue.claim",
      ]);
  } finally {
    try {
      await attach(info, clients);
    } finally {
      await Promise.all(contexts.map((context) => context.close()));
    }
  }
});

test("real proxy disconnect resumes both accepted cursors without replay gaps or duplicates @regression", async ({
  browser,
}, info) => {
  test.skip(
    !process.env.LOOM_SSE_TEST_PROXY_CONTAINER,
    "Requires an explicitly owned local-mode proxy for real socket interruption",
  );
  test.setTimeout(90_000);
  const title = `SSE real reconnect ${generateTestId()}`;
  const id = await createIssue(title);
  const contexts = await Promise.all([
    browser.newContext({ baseURL: FRONTEND_BASE_URL }),
    browser.newContext({ baseURL: FRONTEND_BASE_URL }),
  ]);
  const clients: Client[] = [];
  let proxyStopped = false;
  try {
    for (const context of contexts)
      clients.push(await observe(await context.newPage(), [id]));
    let completed = clients.map(({ probe }) => probe.completions.length);
    await updateIssueStatusInWorkspace(workspace, id, "in_progress");
    for (const [index, client] of clients.entries()) {
      await visible(client, "In Progress issues", title);
      await expect.poll(() => mutations(client.probe, id).length).toBe(1);
      await expect
        .poll(() => client.probe.completions.length, { timeout: 10_000 })
        .toBeGreaterThan(completed[index]);
    }
    const cursors = clients.map(
      ({ probe }) => probe.frames.filter((frame) => frame.id).at(-1)!.id,
    );
    for (const cursor of cursors) expect(cursor).toMatch(/^c2\./);
    const requestCounts = clients.map(({ probe }) => probe.requests.length);
    const activeStreams = clients.map(
      ({ probe }) =>
        probe.requests.filter((request) => request.streamAttached).at(-1)!
          .requestId,
    );
    const failureCounts = clients.map(({ probe }) => probe.failures.length);
    // Mark before stop so finally restores the proxy even if stop observation fails.
    proxyStopped = true;
    await proxyAction("stop");
    for (const [index, client] of clients.entries()) {
      await expect
        .poll(
          () => {
            client.probe.assertHealthy();
            return client.probe.failures
              .slice(failureCounts[index])
              .some((failure) => failure.requestId === activeStreams[index]);
          },
          { timeout: 15_000 },
        )
        .toBe(true);
    }
    await updateIssueStatusInWorkspace(workspace, id, "review");
    for (const client of clients)
      expect(mutations(client.probe, id)).toHaveLength(1);
    await proxyAction("start");
    proxyStopped = false;
    for (const [index, client] of clients.entries()) {
      await expect
        .poll(
          () =>
            client.probe.requests
              .slice(requestCounts[index])
              .some(
                (request) =>
                  request.status === 200 &&
                  request.lastEventId === cursors[index] &&
                  request.since === null,
              ),
          { timeout: 30_000 },
        )
        .toBe(true);
      await expect.poll(() => mutations(client.probe, id).length).toBe(2);
      await visible(client, "Review issues", title);
    }
    completed = clients.map(({ probe }) => probe.completions.length);
    await updateIssueStatusInWorkspace(workspace, id, "open");
    for (const [index, client] of clients.entries()) {
      await visible(client, "Open issues", title);
      await expect.poll(() => mutations(client.probe, id).length).toBe(4);
      await expect
        .poll(() => client.probe.completions.length, { timeout: 10_000 })
        .toBeGreaterThan(completed[index]);
      const frames = mutations(client.probe, id);
      expect(frames).toHaveLength(4);
      expect(frames.map((frame) => frame.action)).toEqual([
        "issue.claim",
        "issue.update",
        "issue.update",
        "issue.assign",
      ]);
      expect(
        frames.map(
          (frame) =>
            (frame.data as { new_status?: string } | undefined)?.new_status,
        ),
      ).toEqual(["in_progress", "review", "open", undefined]);
      expect(new Set(frames.map((frame) => frame.id)).size).toBe(4);
      expect(client.navigations()).toBe(1);
      client.probe.assertHealthy();
    }
    expect(mutations(clients[0].probe, id).map((frame) => frame.id)).toEqual(
      mutations(clients[1].probe, id).map((frame) => frame.id),
    );
  } finally {
    try {
      if (proxyStopped) await proxyAction("start");
    } finally {
      try {
        await attach(info, clients);
      } finally {
        await Promise.all(contexts.map((context) => context.close()));
      }
    }
  }
});
