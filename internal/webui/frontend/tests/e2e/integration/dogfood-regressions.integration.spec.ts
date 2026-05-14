import { test, expect, type Page } from "@playwright/test";
import {
  createWsIssue,
  createTestIssueInWorkspace,
  createTestWorkspace,
  deleteTestWorkspace,
  generateTestId,
  getWorkspaceById,
  getWsIssue,
  resolveWorkspaceId,
  closeTestIssueInWorkspace,
  type WorkspaceResponse,
} from "./helpers";

/**
 * Real-stack regressions promoted from dogfood-output reports.
 *
 * These tests intentionally use the FleetDB-backed integration stack instead
 * of mocked Playwright routes so they exercise workspace topology, source_repo
 * defaults, search filtering, and SSE-backed initial state together.
 */

const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(skipIntegration, "Integration tests require RUN_INTEGRATION_TESTS=1");

test.describe.configure({ mode: "serial" });

async function gotoKanban(page: Page, workspaceId: string, query = "") {
  await page.goto(`/ws/${encodeURIComponent(workspaceId)}/kanban${query}`);
  await page.waitForLoadState("domcontentloaded");
}

async function gotoTable(page: Page, workspaceId: string, query = "") {
  await page.goto(`/ws/${encodeURIComponent(workspaceId)}/table${query}`);
  await page.waitForLoadState("domcontentloaded");
}

async function waitForKanbanReady(page: Page) {
  await expect(page.locator('[data-state="connected"]')).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByRole("region", { name: "Open issues" })).toBeVisible({
    timeout: 15_000,
  });
}

async function waitForWorkspaceShellReady(page: Page) {
  await expect(page.locator('[data-state="connected"]')).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByTestId("new-issue-button")).toBeVisible({
    timeout: 15_000,
  });
}

test.describe("Dogfood promoted regressions", () => {
  const createdIssues: Array<{ workspaceId: string; issueId: string }> = [];
  const createdWorkspaces: string[] = [];

  test.afterEach(async () => {
    for (const { workspaceId, issueId } of createdIssues.splice(0)) {
      await closeTestIssueInWorkspace(workspaceId, issueId).catch(() => {});
    }
    for (const workspaceId of createdWorkspaces.splice(0)) {
      await deleteTestWorkspace(workspaceId).catch(() => {});
    }
  });

  test("real-stack page load has no console errors, page errors, or missing static resources @regression", async ({
    page,
  }) => {
    const workspaceId = await resolveWorkspaceId();
    const title = `Dogfood Clean Load ${generateTestId()}`;
    const issueId = await createTestIssueInWorkspace(workspaceId, title);
    createdIssues.push({ workspaceId, issueId });

    const consoleErrors: string[] = [];
    const pageErrors: string[] = [];
    const failedResources: string[] = [];

    page.on("console", (message) => {
      if (message.type() === "error") {
        const location = message.location();
        const source = location.url
          ? ` (${location.url}:${location.lineNumber}:${location.columnNumber})`
          : "";
        consoleErrors.push(`${message.text()}${source}`);
      }
    });
    page.on("pageerror", (error) => {
      pageErrors.push(error.message);
    });
    page.on("response", (response) => {
      const resourceType = response.request().resourceType();
      if (
        response.status() >= 400 &&
        ["document", "script", "stylesheet", "image", "font"].includes(
          resourceType,
        )
      ) {
        failedResources.push(
          `${response.status()} ${resourceType} ${response.url()}`,
        );
      }
    });

    await gotoKanban(page, workspaceId, "?groupBy=none");
    await waitForKanbanReady(page);
    await expect(page.locator("article", { hasText: title })).toBeVisible({
      timeout: 15_000,
    });

    expect.soft(failedResources).toEqual([]);
    expect.soft(pageErrors).toEqual([]);
    expect.soft(consoleErrors).toEqual([]);
  });

  test("real-stack table route loads API-created issues @regression", async ({
    page,
  }) => {
    const workspaceId = await resolveWorkspaceId();
    const title = `Dogfood Table Route ${generateTestId()}`;
    const issueId = await createTestIssueInWorkspace(workspaceId, title);
    createdIssues.push({ workspaceId, issueId });

    await gotoTable(page, workspaceId);

    await expect(page.locator('[data-state="connected"]')).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId("issue-table")).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByTestId(`issue-row-${issueId}`)).toContainText(
      title,
      {
        timeout: 15_000,
      },
    );
  });

  test("real-stack search no-match hides Kanban cards and preserves the query @regression", async ({
    page,
  }) => {
    const workspaceId = await resolveWorkspaceId();
    const testId = generateTestId();
    const firstTitle = `Dogfood Search First ${testId}`;
    const secondTitle = `Dogfood Search Second ${testId}`;

    const firstId = await createTestIssueInWorkspace(workspaceId, firstTitle);
    const secondId = await createTestIssueInWorkspace(workspaceId, secondTitle);
    createdIssues.push(
      { workspaceId, issueId: firstId },
      { workspaceId, issueId: secondId },
    );

    await gotoKanban(page, workspaceId, "?groupBy=none");
    await waitForKanbanReady(page);

    const readyColumn = page.getByRole("region", { name: "Open issues" });
    await expect(
      readyColumn.locator("article", { hasText: firstTitle }),
    ).toBeVisible({
      timeout: 15_000,
    });
    await expect(
      readyColumn.locator("article", { hasText: secondTitle }),
    ).toBeVisible({
      timeout: 15_000,
    });

    const searchInput = page.getByTestId("search-input-field");
    const noMatchQuery = `zzzz-no-match-${testId}`;
    await searchInput.fill(noMatchQuery);

    await expect(searchInput).toHaveValue(noMatchQuery);
    await expect(page.locator("article", { hasText: firstTitle })).toHaveCount(
      0,
    );
    await expect(page.locator("article", { hasText: secondTitle })).toHaveCount(
      0,
    );
  });

  test("real-stack mobile Kanban keeps controls inside the viewport @regression", async ({
    page,
  }) => {
    const workspaceId = await resolveWorkspaceId();
    const title = `Dogfood Mobile Kanban ${generateTestId()}`;
    const issueId = await createTestIssueInWorkspace(workspaceId, title);
    createdIssues.push({ workspaceId, issueId });

    await page.setViewportSize({ width: 390, height: 844 });
    await gotoKanban(page, workspaceId, "?groupBy=none");
    await waitForKanbanReady(page);
    await expect(page.locator("article", { hasText: title })).toBeVisible({
      timeout: 15_000,
    });

    const horizontalOverflowPx = await page.evaluate(() => {
      return (
        Math.max(
          document.body.scrollWidth,
          document.documentElement.scrollWidth,
        ) - window.innerWidth
      );
    });
    expect(horizontalOverflowPx).toBeLessThanOrEqual(2);

    const newIssueButton = page.getByTestId("new-issue-button");
    await expect(newIssueButton).toBeVisible();
    const buttonBox = await newIssueButton.boundingBox();
    expect(buttonBox).toBeTruthy();
    expect(buttonBox!.x).toBeGreaterThanOrEqual(0);
    expect(buttonBox!.x + buttonBox!.width).toBeLessThanOrEqual(391);
    expect(buttonBox!.y + buttonBox!.height).toBeLessThanOrEqual(845);
  });

  test("single-repo UI issue creation records source_repo on the real backend @regression", async ({
    page,
  }) => {
    const workspaceId = await resolveWorkspaceId();
    const workspace = await getWorkspaceById(workspaceId);
    test.skip(
      (workspace.data?.repos?.length ?? 0) !== 1,
      "Dogfood source_repo regression requires a single-repo workspace",
    );

    const repoName =
      workspace.data!.repos[0].source_repo_id || workspace.data!.repos[0].name;
    const title = `Dogfood UI Source Repo ${generateTestId()}`;

    await gotoKanban(page, workspaceId, "?groupBy=none");
    await waitForWorkspaceShellReady(page);

    await page.getByTestId("new-issue-button").click();
    const modal = page.getByRole("dialog", { name: "Create Issue" });
    await expect(modal).toBeVisible();

    const repoSelect = page.getByTestId("create-issue-source-repo");
    await expect(repoSelect).toBeVisible();
    await expect(repoSelect).toBeDisabled();
    await expect(repoSelect).toHaveValue(repoName);

    await page.getByTestId("create-issue-title").fill(title);

    const responsePromise = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        /\/api\/workspaces\/[^/]+\/issues$/.test(
          new URL(response.url()).pathname,
        ),
    );
    await page.getByTestId("create-issue-submit").click();
    const response = await responsePromise;
    expect(response.status()).toBeGreaterThanOrEqual(200);
    expect(response.status()).toBeLessThan(300);

    const body = await response.json();
    const issueId = body?.data?.id;
    expect(issueId).toBeTruthy();
    createdIssues.push({ workspaceId, issueId });

    await expect(modal).not.toBeVisible();
    await expect(page.locator("article", { hasText: title })).toBeVisible({
      timeout: 15_000,
    });

    const issue = await getWsIssue(workspaceId, issueId);
    expect(issue.source_repo ?? issue.repo).toBe(repoName);
  });

  test("issue detail repo label mutation preserves title on the real backend @regression", async ({
    page,
  }) => {
    const workspaceId = await resolveWorkspaceId();
    const workspace = await getWorkspaceById(workspaceId);
    test.skip(
      (workspace.data?.repos?.length ?? 0) < 1,
      "Dogfood repo mutation regression requires at least one repo",
    );

    const repoName =
      workspace.data!.repos[0].source_repo_id || workspace.data!.repos[0].name;
    const title = `Dogfood Repo Detail ${generateTestId()}`;
    const issueId = await createWsIssue(workspaceId, title);
    createdIssues.push({ workspaceId, issueId });

    await gotoKanban(page, workspaceId, "?groupBy=none");
    await waitForKanbanReady(page);

    await page.locator("article", { hasText: title }).click();
    const titleDisplay = page.getByTestId("editable-title-display");
    await expect(titleDisplay).toHaveText(title, { timeout: 15_000 });

    const patchPromise = page.waitForResponse((response) => {
      const pathname = new URL(response.url()).pathname;
      return (
        response.request().method() === "PATCH" &&
        pathname.endsWith(
          `/api/workspaces/${encodeURIComponent(
            workspaceId,
          )}/issues/${encodeURIComponent(issueId)}`,
        )
      );
    });
    await page.getByTestId("add-label-button").click();
    const labelInput = page.getByTestId("label-input");
    await labelInput.fill(`repo:${repoName}`);
    await labelInput.press("Enter");
    const patchResponse = await patchPromise;
    expect(patchResponse.status()).toBeGreaterThanOrEqual(200);
    expect(patchResponse.status()).toBeLessThan(300);

    await expect(titleDisplay).toHaveText(title);
    await expect(titleDisplay).not.toContainText("undefined");
    await expect(page.getByTestId("label-list")).toContainText(
      `repo:${repoName}`,
    );

    const issue = await getWsIssue(workspaceId, issueId);
    expect(issue.title).toBe(title);
  });

  test("empty workspace creation does not inherit repos or phantom agents @regression", async () => {
    const name = `dogfood-empty-${generateTestId()}`;
    const response = await createTestWorkspace(name, { type: "empty" });
    expect(response.status).toBe(201);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);
    const workspaceId =
      body.data?.workspaces?.find((workspace) => workspace.name === name)?.id ??
      "";
    expect(workspaceId).toBeTruthy();
    createdWorkspaces.push(workspaceId);

    const workspace = await getWorkspaceById(workspaceId);
    expect(workspace.success).toBe(true);
    expect(workspace.data?.repos ?? []).toHaveLength(0);
    expect(workspace.data?.agents ?? []).toHaveLength(0);
  });
});
