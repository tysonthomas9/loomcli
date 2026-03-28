import { test, expect } from "@playwright/test";
import {
  generateTestId,
  listWorkspaces,
  createWsIssue,
  getWsIssue,
  moveWsIssue,
  deleteWsIssue,
  closeTestIssueInWorkspace,
} from "./helpers";

/**
 * Integration tests for cross-workspace issue move lifecycle.
 *
 * These tests require:
 * - A running loom serve instance with at least 2 configured workspaces
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration cross-workspace-move
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(skipIntegration, "Integration tests require RUN_INTEGRATION_TESTS=1");

// Run tests serially to avoid data conflicts with shared backend state
test.describe.configure({ mode: "serial" });

test.describe("Cross-Workspace Issue Move Integration", () => {
  let sourceWs: { id: string; name: string };
  let targetWs: { id: string; name: string };

  // Track issues for cleanup: [wsId, issueId]
  const trackedIssues: Array<[string, string]> = [];

  test.beforeAll(async () => {
    const resp = await listWorkspaces();
    // Filter to workspaces with active pool connections
    const eligible = resp.workspaces.filter((ws) => ws.pool != null);
    test.skip(
      eligible.length < 2,
      "Requires at least 2 configured workspaces with active pools",
    );
    sourceWs = { id: eligible[0].id, name: eligible[0].name };
    targetWs = { id: eligible[1].id, name: eligible[1].name };
  });

  test.afterEach(async () => {
    // Clean up in reverse order (target first, then source)
    for (const [wsId, issueId] of [...trackedIssues].reverse()) {
      await deleteWsIssue(wsId, issueId);
    }
    trackedIssues.length = 0;
  });

  test("move issue to another workspace creates issue in target and closes source", async () => {
    const title = `Move Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title, { priority: 2 });
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, targetWs.name);
    expect(moveResp.status).toBe(200);
    expect(moveResp.body.success).toBe(true);

    const targetId = moveResp.body.data?.target_id;
    expect(targetId).toBeTruthy();
    if (targetId) trackedIssues.push([targetWs.id, targetId]);

    // Verify target issue
    const targetIssue = await getWsIssue(targetWs.id, targetId!);
    expect(targetIssue.title).toBe(title);
    expect(targetIssue.description).toContain(`(Moved from ${issueId})`);
    expect(targetIssue.priority).toBe(2);
    expect(targetIssue.status).toBe("open");

    // Verify source issue is closed with move comment
    const sourceIssue = await getWsIssue(sourceWs.id, issueId);
    expect(sourceIssue.status).toBe("closed");
    const comments = sourceIssue.comments as Array<{ text?: string }>;
    expect(comments).toBeInstanceOf(Array);
    const hasMoveComment = comments.some(
      (c) => c.text && c.text.includes(`Moved to ${targetId}`),
    );
    expect(hasMoveComment).toBe(true);
  });

  test("move preserves all issue fields", async () => {
    const title = `Fields Test ${generateTestId()}`;
    const description = "Original description for field preservation test";
    const issueId = await createWsIssue(sourceWs.id, title, {
      priority: 1,
      description,
      labels: ["test-label-a", "test-label-b"],
      assignee: "test-agent",
      owner: "test-owner",
    });
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, targetWs.name);
    expect(moveResp.status).toBe(200);
    expect(moveResp.body.success).toBe(true);

    const targetId = moveResp.body.data?.target_id;
    expect(targetId).toBeTruthy();
    if (targetId) trackedIssues.push([targetWs.id, targetId]);

    const targetIssue = await getWsIssue(targetWs.id, targetId!);
    expect(targetIssue.title).toBe(title);
    expect(targetIssue.description).toContain(description);
    expect(targetIssue.description).toContain(`(Moved from ${issueId})`);
    expect(targetIssue.priority).toBe(1);
    expect(targetIssue.labels).toEqual(
      expect.arrayContaining(["test-label-a", "test-label-b"]),
    );
    expect(targetIssue.assignee).toBe("test-agent");
    expect(targetIssue.owner).toBe("test-owner");
  });

  test("move returns warning for assigned agent", async () => {
    const title = `Agent Warning Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title, {
      assignee: "some-agent",
    });
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, targetWs.name);
    expect(moveResp.status).toBe(200);
    expect(moveResp.body.success).toBe(true);

    const targetId = moveResp.body.data?.target_id;
    expect(targetId).toBeTruthy();
    if (targetId) trackedIssues.push([targetWs.id, targetId]);

    expect(moveResp.body.data!.warnings).toBeInstanceOf(Array);
    const hasAgentWarning = moveResp.body.data!.warnings!.some((w) =>
      /agent.*assigned/i.test(w),
    );
    expect(hasAgentWarning).toBe(true);
  });

  test("move to same workspace returns 400", async () => {
    const title = `Same WS Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title);
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, sourceWs.name);
    expect(moveResp.status).toBe(400);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.error).toContain("cannot move issue to the same workspace");
  });

  test("move closed issue returns 400", async () => {
    const title = `Closed Move Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title);
    trackedIssues.push([sourceWs.id, issueId]);

    await closeTestIssueInWorkspace(sourceWs.id, issueId);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, targetWs.name);
    expect(moveResp.status).toBe(400);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.error).toContain("cannot move a closed issue");
  });

  test("move to non-existent workspace returns 400", async () => {
    const title = `Bad Target Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title);
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(
      sourceWs.id,
      issueId,
      "nonexistent-workspace-xyz",
    );
    expect(moveResp.status).toBe(400);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.error).toContain("not found");
  });

  test("move non-existent issue returns 404", async () => {
    const moveResp = await moveWsIssue(
      sourceWs.id,
      "nonexistent-issue-xyz",
      targetWs.name,
    );
    expect(moveResp.status).toBe(404);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.error).toContain("not found");
  });
});
