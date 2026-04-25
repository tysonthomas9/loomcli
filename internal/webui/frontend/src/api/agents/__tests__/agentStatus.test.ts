/**
 * Unit tests for the workspace-scoped agent status API client.
 *
 * Verifies fetchWorkspaceAgentStatus:
 *   - short-circuits on empty wsID without firing a request,
 *   - unwraps the {success, data} envelope on the happy path,
 *   - throws ApiError on transport / non-OK responses (e.g. 503),
 *   - throws ApiError when the envelope reports success: false,
 *   - threads an AbortSignal through to api.GET.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import { ApiError, api } from "@/api/common";

import { fetchWorkspaceAgentStatus } from "../agentStatus";
import type { WorkspaceAgentStatusResponse } from "@/types";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);

/** Build a minimal-but-valid WorkspaceAgentStatusResponse for happy-path tests. */
function buildResponse(): WorkspaceAgentStatusResponse {
  return {
    agents: [
      {
        worktree: "nova",
        worktree_path: "/home/user/repo/.worktrees/nova",
        path: "/home/user/repo/.worktrees/nova",
        workspace: "ws-1",
        cross_repo: false,
        pid: 4242,
        status: "working:bd-100",
        supervisor_status: "running",
        restart_count: 0,
        branch: "feature/nova",
        ahead: 1,
        behind: 0,
        changes: 2,
        yield_requested: false,
      },
    ],
    ipc_socket_active: true,
    daemon_pid: 999,
    daemon_started_at: "2024-08-01T00:00:00Z",
    workspace_name: "ws-1",
    timestamp: "2024-08-01T12:00:00Z",
  };
}

describe("fetchWorkspaceAgentStatus", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("returns the empty default response for empty wsID without firing the API", async () => {
    const result = await fetchWorkspaceAgentStatus("");

    expect(mockApiGet).not.toHaveBeenCalled();
    expect(result).toEqual({
      agents: [],
      ipc_socket_active: false,
      daemon_pid: 0,
      daemon_started_at: "",
      workspace_name: "",
      timestamp: "",
    });
  });

  it("happy path: returns inner data when envelope.success is true", async () => {
    const expected = buildResponse();

    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: expected },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchWorkspaceAgentStatus("ws-1");

    expect(result).toEqual(expected);
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/status",
      expect.objectContaining({
        params: { path: { ws: "ws-1" } },
      }),
    );
  });

  it("throws ApiError with status 503 when api.GET returns a 503 error", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "daemon_unavailable" },
      response: new Response(null, {
        status: 503,
        statusText: "Service Unavailable",
      }),
    } as never);

    const err = await fetchWorkspaceAgentStatus("ws-1").catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 503 });
  });

  it("throws ApiError when envelope.success is false", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: false },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(fetchWorkspaceAgentStatus("ws-1")).rejects.toThrow(ApiError);
  });

  it("passes the AbortSignal through to api.GET", async () => {
    const controller = new AbortController();
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: buildResponse() },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchWorkspaceAgentStatus("ws-1", { signal: controller.signal });

    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/status",
      expect.objectContaining({ signal: controller.signal }),
    );
  });
});
