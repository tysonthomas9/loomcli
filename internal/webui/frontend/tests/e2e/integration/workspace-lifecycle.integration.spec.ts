import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  authHeaders,
  generateTestId,
  getActiveWorkspace,
  getWorkspaceById,
  createTestWorkspace,
  deleteTestWorkspace,
  setDefaultWorkspace,
  clearDefaultWorkspace,
  renameWorkspace,
  reorderWorkspaces,
  updateWorkspaceBackend,
  type WorkspaceResponse,
} from "./helpers";

/**
 * Integration tests for workspace lifecycle CRUD operations against real backend.
 *
 * These tests require:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration workspace-lifecycle
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(skipIntegration, "Integration tests require RUN_INTEGRATION_TESTS=1");

// Run tests serially because workspace mutations share local FleetDB-backed state.
test.describe.configure({ mode: "serial" });


test.describe("Workspace Lifecycle Integration", () => {
  const testId = generateTestId();
  let workspaceName = `ws-${testId}`;
  let workspaceId = "";
  let originalDefault = "";
  let repoPath = "";

  // Discover a valid repo path from the running server's active workspace
  test.beforeAll(async () => {
    const active = await getActiveWorkspace();
    if (active.data?.repos?.length) {
      repoPath = active.data.repos[0].path;
    }
    if (active.data?.default_workspace) {
      originalDefault = active.data.default_workspace;
    }
  });

  // Cleanup: delete test workspace and restore original default.
  // Workspace deletion may stop a daemon (30+ seconds), so extend the hook timeout.
  // Use a 45s internal timeout so the afterAll hook doesn't hang indefinitely.
  test.afterAll(async ({}, testInfo) => {
    testInfo.setTimeout(120_000);
    if (workspaceId) {
      await Promise.race([
        deleteTestWorkspace(workspaceId),
        new Promise(r => setTimeout(r, 45_000)),
      ]).catch(() => {});
    }
    // Restore original default if we changed it
    if (originalDefault) {
      await setDefaultWorkspace(originalDefault).catch(() => {});
    } else {
      await clearDefaultWorkspace().catch(() => {});
    }
  });

  test("create workspace via API and verify in workspace list", async () => {
    // Workspace creation involves git init + config writes and can be slow
    test.setTimeout(120_000);
    test.skip(
      !repoPath,
      "No repo path available from active workspace — cannot create test workspace",
    );

    const response = await createTestWorkspace(workspaceName, {
      type: "empty",
      repos: [repoPath],
    });
    expect(response.status).toBe(201);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // Find the created workspace in the returned data to get its UUID
    const created = body.data?.workspaces?.find(
      (ws) => ws.name === workspaceName,
    );
    expect(created).toBeTruthy();
    workspaceId = created!.id;
    expect(workspaceId).toBeTruthy();
  });

  test("read workspace topology returns repos", async () => {
    test.skip(!workspaceId, "Workspace was not created");

    const body = await getWorkspaceById(workspaceId);
    expect(body.success).toBe(true);
    expect(body.data).toBeTruthy();
    expect(body.data!.name).toBe(workspaceName);
    expect(body.data!.repos).toBeInstanceOf(Array);
    expect(body.data!.repos.length).toBeGreaterThan(0);

    // Verify repo structure
    const repo = body.data!.repos[0];
    expect(repo.name).toBeTruthy();
    expect(repo.path).toBeTruthy();
  });

  test("set created workspace as default", async () => {
    test.skip(!workspaceId, "Workspace was not created");

    const response = await setDefaultWorkspace(workspaceName);
    expect(response.status).toBe(200);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // Verify default is set
    const active = await getActiveWorkspace();
    expect(active.data?.default_workspace).toBe(workspaceName);
  });

  test("rename workspace and verify new name persists", async () => {
    test.setTimeout(120_000);
    test.skip(!workspaceId, "Workspace was not created");

    const newName = `ws-renamed-${testId}`;
    const response = await renameWorkspace(workspaceId, newName);
    expect(response.status).toBe(200);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // Verify the workspace appears under the new name
    const wsData = await getWorkspaceById(workspaceId);
    expect(wsData.data?.name).toBe(newName);

    // Verify default_workspace was updated since we set it as default
    const active = await getActiveWorkspace();
    expect(active.data?.default_workspace).toBe(newName);

    workspaceName = newName;
  });

  test("reorder workspaces and verify order persists", async () => {
    test.skip(!workspaceId, "Workspace was not created");

    // Get current workspaces to build an order list
    const active = await getActiveWorkspace();
    const allNames = active.data?.workspaces?.map((ws) => ws.name) ?? [];
    expect(allNames).toContain(workspaceName);

    // Put our test workspace first
    const reordered = [
      workspaceName,
      ...allNames.filter((n) => n !== workspaceName),
    ];
    const response = await reorderWorkspaces(reordered);
    expect(response.status).toBe(200);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // Verify order persisted
    const refreshed = await getActiveWorkspace();
    const order = refreshed.data?.workspace_order ?? [];
    expect(order.length).toBeGreaterThan(0);
    expect(order[0]).toBe(workspaceName);
  });

  test("update workspace backend config", async () => {
    test.skip(!workspaceId, "Workspace was not created");

    const response = await updateWorkspaceBackend(workspaceId, "codex");
    expect(response.status).toBe(200);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // Verify via GET on the workspace
    const wsData = await getWorkspaceById(workspaceId);
    expect(wsData.success).toBe(true);
  });

  test("clear default workspace", async () => {
    test.skip(!workspaceId, "Workspace was not created");

    const response = await clearDefaultWorkspace();
    expect(response.status).toBe(200);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // After clearing, the default should no longer be our test workspace.
    // The server may fall back to the first workspace or return empty.
    const active = await getActiveWorkspace();
    expect(active.data?.default_workspace).not.toBe(workspaceName);
  });

  test("delete workspace removes from config", async () => {
    test.skip(!workspaceId, "Workspace was not created");

    const response = await fetch(
      `${BASE_URL}/api/workspaces/${encodeURIComponent(workspaceId)}`,
      { method: "DELETE", headers: authHeaders() },
    );
    expect(response.status).toBe(200);

    const body: WorkspaceResponse = await response.json();
    expect(body.success).toBe(true);

    // Verify workspace no longer in list
    const active = await getActiveWorkspace();
    const found = active.data?.workspaces?.find((ws) => ws.id === workspaceId);
    expect(found).toBeFalsy();

    // Mark as deleted so afterAll cleanup is a no-op
    workspaceId = "";
  });

  test("rename to existing workspace name returns 409", async () => {
    test.setTimeout(240_000);
    test.skip(!repoPath, "No repo path available");

    // Create two workspaces
    const nameA = `ws-conflict-a-${testId}`;
    const nameB = `ws-conflict-b-${testId}`;
    let idA = "";
    let idB = "";

    try {
      const respA = await createTestWorkspace(nameA, {
        type: "empty",
        repos: [repoPath],
      });
      expect(respA.status).toBe(201);
      const bodyA: WorkspaceResponse = await respA.json();
      idA = bodyA.data?.workspaces?.find((ws) => ws.name === nameA)?.id ?? "";
      expect(idA).toBeTruthy();

      const respB = await createTestWorkspace(nameB, {
        type: "empty",
        repos: [repoPath],
      });
      expect(respB.status).toBe(201);
      const bodyB: WorkspaceResponse = await respB.json();
      idB = bodyB.data?.workspaces?.find((ws) => ws.name === nameB)?.id ?? "";
      expect(idB).toBeTruthy();

      // Try to rename A to B's name — should conflict
      const renameResp = await renameWorkspace(idA, nameB);
      expect(renameResp.status).toBe(409);

      const renameBody = await renameResp.json();
      expect(renameBody.error).toBeTruthy();
    } finally {
      if (idA) await deleteTestWorkspace(idA).catch(() => {});
      if (idB) await deleteTestWorkspace(idB).catch(() => {});
    }
  });
});

// --- Validation / error tests (independent, not serial) ---

test.describe("Workspace Validation", () => {
  test("create workspace with invalid name returns 400", async () => {
    const response = await createTestWorkspace("invalid name with spaces", {
      type: "empty",
      repos: ["/tmp"],
    });
    expect(response.status).toBe(400);

    const body = await response.json();
    expect(body.error).toBeTruthy();
  });

  test("create workspace with missing type returns 400", async () => {
    const response = await createTestWorkspace(`ws-notype-${generateTestId()}`, {
      type: "",
      repos: ["/tmp"],
    });
    expect(response.status).toBe(400);

    const body = await response.json();
    expect(body.error).toContain("type");
  });

  test("delete non-existent workspace returns 404", async () => {
    const fakeId = `nonexistent-${generateTestId()}`;
    const response = await fetch(
      `${BASE_URL}/api/workspaces/${encodeURIComponent(fakeId)}`,
      { method: "DELETE", headers: authHeaders() },
    );
    expect(response.status).toBe(404);
  });
});
