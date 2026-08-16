import { test, expect } from "@playwright/test";
import {
  generateTestId,
  listWorkspaces,
  createWsIssue,
  getWsIssue,
  moveWsIssue,
  mutateWsIssue,
  patchWsIssue,
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

    const sourceBeforeMove = await getWsIssue(sourceWs.id, issueId);
    const intent = {
      expectedSourceRevision: sourceBeforeMove.updated_at,
      requestId: crypto.randomUUID(),
    };
    const moveResp = await moveWsIssue(
      sourceWs.id,
      issueId,
      targetWs.name,
      intent,
    );
    expect(moveResp.status).toBe(200);
    expect(moveResp.body.success).toBe(true);

    const targetId = moveResp.body.data?.target_id;
    expect(targetId).toBeTruthy();
    if (targetId) trackedIssues.push([targetWs.id, targetId]);

    // Verify target issue
    const targetIssue = await getWsIssue(targetWs.id, targetId!);
    expect(targetIssue.title).toBe(title);
    expect(targetIssue.moved_from).toEqual({
      workspace: sourceBeforeMove.workspace ?? sourceWs.id,
      issue_id: issueId,
    });
    expect(targetIssue.priority).toBe(2);
    expect(targetIssue.status).toBe("open");

    // Verify source issue is closed with durable typed lineage.
    const sourceIssue = await getWsIssue(sourceWs.id, issueId);
    expect(sourceIssue.status).toBe("closed");
    expect(sourceIssue.moved_to).toEqual({
      workspace: targetIssue.workspace ?? targetWs.id,
      issue_id: targetId,
    });

    // Lost-response replay converges on the original target.
    const replay = await moveWsIssue(
      sourceWs.id,
      issueId,
      targetWs.name,
      intent,
    );
    expect(replay.status).toBe(200);
    expect(replay.body.data).toEqual({
      source_id: issueId,
      target_id: targetId,
      replayed: true,
    });

    // The source is durable read-only history through every public escape
    // hatch, not only through disabled controls in the UI.
    for (const mutation of [
      () =>
        mutateWsIssue(sourceWs.id, issueId, "PATCH", "", {
          title: "forbidden",
        }),
      () =>
        mutateWsIssue(sourceWs.id, issueId, "POST", "/comments", {
          text: "forbidden",
        }),
      () => mutateWsIssue(sourceWs.id, issueId, "POST", "/reopen", {}),
      () => mutateWsIssue(sourceWs.id, issueId, "DELETE"),
    ]) {
      const rejected = await mutation();
      expect(rejected.status).toBe(409);
      expect(rejected.body.error).toContain("moved");
    }
  });

  test("stale source revision conflicts without creating a target", async () => {
    const issueId = await createWsIssue(
      sourceWs.id,
      `Revision Test ${generateTestId()}`,
    );
    trackedIssues.push([sourceWs.id, issueId]);
    const before = await getWsIssue(sourceWs.id, issueId);
    await patchWsIssue(sourceWs.id, issueId, {
      title: `Changed ${generateTestId()}`,
    });

    const response = await moveWsIssue(sourceWs.id, issueId, targetWs.name, {
      expectedSourceRevision: before.updated_at,
      requestId: crypto.randomUUID(),
    });
    expect(response.status).toBe(409);
    expect(response.body.success).toBe(false);
    expect(response.body.data).toBeUndefined();

    const source = await getWsIssue(sourceWs.id, issueId);
    expect(source.status).toBe("open");
    expect(source.moved_to).toBeUndefined();
  });

  test("move preserves all issue fields", async () => {
    const title = `Fields Test ${generateTestId()}`;
    const description = "Original description for field preservation test";
    const issueId = await createWsIssue(sourceWs.id, title, {
      priority: 1,
      description,
      labels: ["test-label-a", "test-label-b"],
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
    expect(targetIssue.description).toBe(description);
    expect(targetIssue.priority).toBe(1);
    expect(targetIssue.labels).toEqual(
      expect.arrayContaining(["test-label-a", "test-label-b"]),
    );
    expect(targetIssue.assignee ?? "").toBe("");
    expect(targetIssue.owner).toBe("test-owner");
  });

  test("move rejects an assigned source without partial target creation", async () => {
    const title = `Agent Warning Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title, {
      assignee: "some-agent",
    });
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, targetWs.name);
    expect(moveResp.status).toBe(409);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.data).toBeUndefined();
  });

  test("move to same workspace returns 400", async () => {
    const title = `Same WS Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title);
    trackedIssues.push([sourceWs.id, issueId]);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, sourceWs.name);
    expect(moveResp.status).toBe(400);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.error).toContain(
      "cannot move issue to the same workspace",
    );
  });

  test("move closed issue returns 400", async () => {
    const title = `Closed Move Test ${generateTestId()}`;
    const issueId = await createWsIssue(sourceWs.id, title);
    trackedIssues.push([sourceWs.id, issueId]);

    await closeTestIssueInWorkspace(sourceWs.id, issueId);

    const moveResp = await moveWsIssue(sourceWs.id, issueId, targetWs.name);
    expect(moveResp.status).toBe(409);
    expect(moveResp.body.success).toBe(false);
    expect(moveResp.body.error).toContain("cannot be moved");
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
