import { expect } from "@playwright/test";
import {
  BASE_URL,
  authHeaders,
  generateTestId,
  type WorkspaceResponse,
} from "./helpers";

/** Product-created workspace with no autonomous agents; existing dogfood agents remain untouched. */
export async function createIsolatedSSEWorkspace(): Promise<string> {
  const project = process.env.LOCAL_MODE_COMPOSE_PROJECT ?? "";
  if (!/^loomcli-pg-browser-[a-zA-Z0-9_-]+$/.test(project)) {
    throw new Error(
      "SSE isolation requires a run-owned loomcli-pg-browser-* project",
    );
  }
  const repo =
    process.env.LOOM_SSE_TEST_SOURCE_REPO || "/workspace/source-repo";
  if (!repo.startsWith("/"))
    throw new Error("SSE source repository must be an absolute container path");
  const name = `sse-${generateTestId()}`;
  // No retries: a dropped response is an uncertain creation, never a synthesized 201.
  const response = await fetch(`${BASE_URL}/api/workspaces`, {
    method: "POST",
    headers: authHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({ name, type: "empty", repos: [repo] }),
    signal: AbortSignal.timeout(60_000),
  });
  expect(response.status, "Actual workspace creation response").toBe(201);
  const body = (await response.json()) as WorkspaceResponse;
  expect(body.success).toBe(true);
  const matches = body.data?.workspaces?.filter(
    (workspace) => workspace.name === name,
  );
  expect(matches).toHaveLength(1);
  const id = (
    body.data as
      | (NonNullable<WorkspaceResponse["data"]> & { id?: string })
      | undefined
  )?.id;
  expect(id).toBe(matches![0]!.id);
  expect(id).toBeTruthy();
  await assertSSEWorkspaceHasNoAgents(id!);
  return id!;
}

export async function assertSSEWorkspaceHasNoAgents(
  workspace: string,
): Promise<void> {
  const response = await fetch(
    `${BASE_URL}/api/workspaces/${encodeURIComponent(workspace)}`,
    {
      headers: authHeaders(),
      signal: AbortSignal.timeout(15_000),
    },
  );
  expect(response.status).toBe(200);
  const body = (await response.json()) as WorkspaceResponse;
  expect(body.success).toBe(true);
  expect(
    body.data?.agents,
    "No autonomous actors may mutate the test tasks",
  ).toEqual([]);
  expect(body.data?.repos).toHaveLength(1);
  expect(body.data?.repos[0]?.name).toBeTruthy();
}
