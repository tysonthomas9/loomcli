import { test, expect } from "@playwright/test";
import {
  generateTestId,
  resolveWorkspaceId,
  createTestIssueInWorkspace,
  updateIssueStatusInWorkspace,
  closeTestIssueInWorkspace,
  getWsIssue,
  patchWsIssue,
} from "./helpers";

/**
 * Integration tests for concurrent API operations stability.
 * Validates data integrity under simultaneous mutations, parallel label
 * operations, competing status transitions, and burst SSE delivery.
 *
 * These tests require:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration concurrent-ops
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(
  skipIntegration,
  "Integration tests require RUN_INTEGRATION_TESTS=1",
);

// Run tests serially to avoid data conflicts with shared backend
test.describe.configure({ mode: "serial" });

let workspaceId = "";

test.beforeAll(async () => {
  workspaceId = await resolveWorkspaceId();
});

/**
 * Helper: navigate to '/' and wait for redirect + SSE connected state.
 */
async function navigateAndWaitForConnected(
  page: import("@playwright/test").Page,
) {
  await page.goto("/");
  await page.waitForURL("**/ws/*/**", { timeout: 10_000 });
  await expect(page.locator('[data-state="connected"]')).toBeVisible({
    timeout: 15_000,
  });
}

test.describe("Concurrent issue creation", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    for (const id of testIssueIds) {
      await closeTestIssueInWorkspace(workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("concurrent creates all succeed and return distinct IDs", async () => {
    const titles = Array.from({ length: 5 }, () =>
      `Concurrent Create ${generateTestId()}`,
    );

    const ids = await Promise.all(
      titles.map((t) => createTestIssueInWorkspace(workspaceId, t)),
    );
    testIssueIds.push(...ids);

    // All 5 should return valid string IDs
    for (const id of ids) {
      expect(typeof id).toBe("string");
      expect(id.length).toBeGreaterThan(0);
    }

    // All IDs must be distinct
    expect(new Set(ids).size).toBe(5);

    // Read-back each and verify
    const issues = await Promise.all(
      ids.map((id) => getWsIssue(workspaceId, id)),
    );
    for (let i = 0; i < 5; i++) {
      expect(issues[i].title).toBe(titles[i]);
      expect(issues[i].status).toBe("open");
      expect(issues[i].priority).toBe(2);
    }
  });

  test("concurrent creates all delivered via SSE to browser", async ({
    page,
  }) => {
    await navigateAndWaitForConnected(page);

    const titles = Array.from({ length: 5 }, () =>
      `SSE Concurrent ${generateTestId()}`,
    );

    const ids = await Promise.all(
      titles.map((t) => createTestIssueInWorkspace(workspaceId, t)),
    );
    testIssueIds.push(...ids);

    // Wait for all 5 to appear in the ready column
    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(async () => {
      for (const title of titles) {
        await expect(readyColumn.getByText(title)).toBeVisible();
      }
    }).toPass({ timeout: 20_000, intervals: [500, 1000, 2000, 3000] });
  });
});

test.describe("Concurrent issue mutations", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    for (const id of testIssueIds) {
      await closeTestIssueInWorkspace(workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("concurrent PATCH of different fields preserves all changes", async () => {
    const title = `Diff Fields ${generateTestId()}`;
    const id = await createTestIssueInWorkspace(workspaceId, title);
    testIssueIds.push(id);

    const newTitle = `Updated ${generateTestId()}`;
    const newDesc = `Description ${generateTestId()}`;

    await Promise.all([
      patchWsIssue(workspaceId, id, { title: newTitle }),
      patchWsIssue(workspaceId, id, { description: newDesc }),
      patchWsIssue(workspaceId, id, { priority: 0 }),
      patchWsIssue(workspaceId, id, { assignee: "test-agent" }),
    ]);

    const readBack = await getWsIssue(workspaceId, id);
    expect(readBack.title).toBe(newTitle);
    expect(readBack.description).toBe(newDesc);
    expect(readBack.priority).toBe(0);
    expect(readBack.assignee).toBe("test-agent");
  });

  test("concurrent PATCH of same field resolves without corruption", async () => {
    const title = `Same Field ${generateTestId()}`;
    const id = await createTestIssueInWorkspace(workspaceId, title);
    testIssueIds.push(id);

    const titles = [
      `title-A-${generateTestId()}`,
      `title-B-${generateTestId()}`,
      `title-C-${generateTestId()}`,
      `title-D-${generateTestId()}`,
      `title-E-${generateTestId()}`,
    ];

    await Promise.all(
      titles.map((t) => patchWsIssue(workspaceId, id, { title: t })),
    );

    const readBack = await getWsIssue(workspaceId, id);
    // Final title must be exactly one of the 5 submitted values
    expect(titles).toContain(readBack.title);
  });

  test("concurrent label add operations accumulate", async () => {
    const title = `Label Add ${generateTestId()}`;
    const id = await createTestIssueInWorkspace(workspaceId, title);
    testIssueIds.push(id);

    const labels = ["concur-a", "concur-b", "concur-c", "concur-d", "concur-e"];

    await Promise.all(
      labels.map((l) =>
        patchWsIssue(workspaceId, id, { add_labels: [l] }),
      ),
    );

    const readBack = await getWsIssue(workspaceId, id);
    const issueLabels = readBack.labels as string[];
    for (const label of labels) {
      expect(issueLabels).toContain(label);
    }
  });

  test("concurrent status transitions end in valid state", async () => {
    const title = `Status Race ${generateTestId()}`;
    const id = await createTestIssueInWorkspace(workspaceId, title);
    testIssueIds.push(id);

    const results = await Promise.allSettled([
      patchWsIssue(workspaceId, id, { status: "in_progress" }),
      patchWsIssue(workspaceId, id, { status: "review" }),
      patchWsIssue(workspaceId, id, { status: "in_progress" }),
    ]);

    // At least one must succeed
    const fulfilled = results.filter((r) => r.status === "fulfilled");
    expect(fulfilled.length).toBeGreaterThanOrEqual(1);

    const readBack = await getWsIssue(workspaceId, id);
    expect(["in_progress", "review"]).toContain(readBack.status);
  });
});

test.describe("Cross-issue concurrent operations", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    for (const id of testIssueIds) {
      await closeTestIssueInWorkspace(workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("parallel operations on separate issues don't interfere", async () => {
    const titleA = `Cross A ${generateTestId()}`;
    const titleB = `Cross B ${generateTestId()}`;
    const titleC = `Cross C ${generateTestId()}`;

    const [idA, idB, idC] = await Promise.all([
      createTestIssueInWorkspace(workspaceId, titleA),
      createTestIssueInWorkspace(workspaceId, titleB),
      createTestIssueInWorkspace(workspaceId, titleC),
    ]);
    testIssueIds.push(idA, idB, idC);

    const newTitleC = `Cross C Updated ${generateTestId()}`;

    await Promise.all([
      patchWsIssue(workspaceId, idA, { status: "in_progress" }),
      patchWsIssue(workspaceId, idB, { priority: 0 }),
      patchWsIssue(workspaceId, idC, { title: newTitleC }),
    ]);

    const [issueA, issueB, issueC] = await Promise.all([
      getWsIssue(workspaceId, idA),
      getWsIssue(workspaceId, idB),
      getWsIssue(workspaceId, idC),
    ]);

    // Issue A: status changed, others unchanged
    expect(issueA.status).toBe("in_progress");
    expect(issueA.title).toBe(titleA);
    expect(issueA.priority).toBe(2);

    // Issue B: priority changed, others unchanged
    expect(issueB.priority).toBe(0);
    expect(issueB.status).toBe("open");
    expect(issueB.title).toBe(titleB);

    // Issue C: title changed, others unchanged
    expect(issueC.title).toBe(newTitleC);
    expect(issueC.status).toBe("open");
    expect(issueC.priority).toBe(2);
  });
});

test.describe("SSE delivery under concurrent load", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    for (const id of testIssueIds) {
      await closeTestIssueInWorkspace(workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("burst of rapid creates and status changes reach SSE client", async ({
    page,
  }) => {
    await navigateAndWaitForConnected(page);

    // Create one issue and wait for it in ready column
    const mainTitle = `SSE Burst Main ${generateTestId()}`;
    const mainId = await createTestIssueInWorkspace(workspaceId, mainTitle);
    testIssueIds.push(mainId);

    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(async () => {
      await expect(readyColumn.getByText(mainTitle)).toBeVisible();
    }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });

    // Start 3 concurrent creates
    const burstTitles = Array.from({ length: 3 }, () =>
      `SSE Burst ${generateTestId()}`,
    );
    const burstPromise = Promise.all(
      burstTitles.map((t) => createTestIssueInWorkspace(workspaceId, t)),
    );

    // Fire 5 rapid sequential status toggles on the main issue
    for (let i = 0; i < 5; i++) {
      await updateIssueStatusInWorkspace(
        workspaceId,
        mainId,
        i % 2 === 0 ? "in_progress" : "open",
      );
    }
    // Final status after 5 iterations (0,1,2,3,4): in_progress

    const burstIds = await burstPromise;
    testIssueIds.push(...burstIds);

    // Verify the main issue ended up in in_progress column
    const inProgressColumn = page.locator(
      'section[data-status="in_progress"]',
    );
    await expect(async () => {
      await expect(inProgressColumn.getByText(mainTitle)).toBeVisible();
    }).toPass({ timeout: 15_000, intervals: [500, 1000, 2000] });

    // Verify all 3 burst-created issues appear in ready column
    await expect(async () => {
      for (const title of burstTitles) {
        await expect(readyColumn.getByText(title)).toBeVisible();
      }
    }).toPass({ timeout: 20_000, intervals: [500, 1000, 2000, 3000] });
  });
});
