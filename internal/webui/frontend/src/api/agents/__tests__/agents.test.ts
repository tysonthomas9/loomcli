/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the Loom Agent API client (agents.ts).
 *
 * These tests verify that the API client correctly fetches and passes through
 * task status categories from the loom server API.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import type { LoomTaskLists } from "@/types";

import { ApiError, api, del, get, patch, post } from "@/api/common";

import {
  fetchAgents,
  fetchStatus,
  fetchTasks,
  checkLoomHealth,
  createPromptAgentRecord,
  deleteAgentRecord,
  getAgentLifecycleCommand,
  restartAgent,
  startAgent,
  stopAgent,
  listAgentRuns,
  listAgentRecords,
  setAgentRecordEnabled,
  updateAgentRecord,
  type FetchStatusResult,
} from "../agents";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
    del: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
  };
});

const mockApiGet = vi.mocked(api.GET);
const mockApiPost = vi.mocked(api.POST);
const mockGet = vi.mocked(get);
const mockPost = vi.mocked(post);
const mockPatch = vi.mocked(patch);
const mockDel = vi.mocked(del);

describe("durable agent record lifecycle", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists only prompt/scripted AgentService identities from the unified collection", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: [
        {
          id: "prompt-1",
          name: "Prompt agent",
          kind: "prompt",
          enabled: false,
          behavior: { role_name: "documentation" },
          workspace_key: "TEAM A",
          budget_policy: "conservative",
          last_run_status: "failed",
          consecutive_failures: 2,
          next_fire_at: "2026-07-29T09:00:00Z",
          metadata: { owner: "docs" },
        },
        {
          id: "scripted-1",
          name: "Scripted agent",
          kind: "scripted",
          enabled: true,
          behavior: { driver_id: "review-loop-agent" },
          workspace_key: "TEAM A",
        },
        { id: "lead", name: "lead", kind: "supervised" },
        { id: "legacy", name: "Legacy binding", kind: "binding" },
      ],
      total: 4,
    });

    await expect(listAgentRecords("TEAM A")).resolves.toEqual([
      {
        id: "prompt-1",
        name: "Prompt agent",
        kind: "prompt",
        enabled: false,
        behavior: { role_name: "documentation" },
        workspace_key: "TEAM A",
        budget_policy: "conservative",
        last_run_status: "failed",
        consecutive_failures: 2,
        next_fire_at: "2026-07-29T09:00:00Z",
        metadata: { owner: "docs" },
      },
      {
        id: "scripted-1",
        name: "Scripted agent",
        kind: "scripted",
        enabled: true,
        behavior: { driver_id: "review-loop-agent" },
        workspace_key: "TEAM A",
      },
    ]);
    expect(mockGet).toHaveBeenCalledWith("/api/workspaces/TEAM%20A/agents");
  });

  it("lists encoded agent history with an optional limit", async () => {
    const response = {
      agent_id: "agent/one",
      runs: [],
      sessions: [
        {
          workspace_key: "TEAM A",
          session_id: "session-1",
          agent_id: "agent/one",
          kind: "task" as const,
          status: "completed" as const,
          created_at: "2026-07-25T00:00:00Z",
          updated_at: "2026-07-25T00:01:00Z",
        },
      ],
    };
    mockGet.mockResolvedValueOnce(response);

    await expect(
      listAgentRuns("TEAM A", "agent/one", { limit: 7 }),
    ).resolves.toEqual(response);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/TEAM%20A/agents/agent%2Fone/runs?limit=7",
    );
  });

  it("patches the record id rather than an attached binding id", async () => {
    mockPatch.mockResolvedValueOnce({
      id: "agent/one",
      name: "Renamed agent",
      kind: "prompt",
      enabled: true,
      behavior: { role_name: "reviewer" },
      workspace_key: "TEAM A",
    });

    await updateAgentRecord("TEAM A", "agent/one", {
      name: "Renamed agent",
    });

    expect(mockPatch).toHaveBeenCalledWith(
      "/api/workspaces/TEAM%20A/agents/agent%2Fone",
      { name: "Renamed agent" },
    );
  });

  it("deletes through the record id so the server archives and cleans children", async () => {
    mockDel.mockResolvedValueOnce({
      agent: { id: "agent/one" },
      archived: true,
      bindings_deleted: 1,
      grants_revoked: 2,
    });

    await deleteAgentRecord("TEAM A", "agent/one");

    expect(mockDel).toHaveBeenCalledWith(
      "/api/workspaces/TEAM%20A/agents/agent%2Fone",
    );
  });

  it("sends record mutations without a local operator credential", async () => {
    mockPost.mockResolvedValue({});
    mockPatch.mockResolvedValue({});
    mockDel.mockResolvedValue({});

    await setAgentRecordEnabled("TEAM A", "agent/one", true);
    await updateAgentRecord("TEAM A", "agent/one", { name: "Renamed" });
    await deleteAgentRecord("TEAM A", "agent/one");
    const create = {
      name: "Created",
      kind: "prompt" as const,
      behavior: { role_name: "bug-fix" },
    };
    await createPromptAgentRecord("TEAM A", create);

    expect(mockPost).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/TEAM%20A/agents/agent%2Fone/enable",
      undefined,
    );
    expect(mockPatch).toHaveBeenCalledWith(
      "/api/workspaces/TEAM%20A/agents/agent%2Fone",
      { name: "Renamed" },
    );
    expect(mockDel).toHaveBeenCalledWith(
      "/api/workspaces/TEAM%20A/agents/agent%2Fone",
    );
    expect(mockPost).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/TEAM%20A/agents",
      create,
      { timeout: 120_000 },
    );
  });
});

describe("fetchAgents", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("returns agents array on successful response", async () => {
    const agents = [
      {
        name: "nova",
        branch: "feature-x",
        status: "ready",
        ahead: 0,
        behind: 0,
      },
      {
        name: "ember",
        branch: "main",
        status: "working:loom-123",
        ahead: 1,
        behind: 0,
      },
    ];

    mockApiGet.mockResolvedValueOnce({
      data: { agents },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchAgents();

    expect(result).toEqual(agents);
    expect(result).toHaveLength(2);
    expect(result[0].name).toBe("nova");
  });

  it("passes workspace scope as monitor query parameter", async () => {
    mockGet.mockResolvedValueOnce({
      agents: [
        {
          name: "nova",
          branch: "main",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "Test",
        },
      ],
      timestamp: "2024-01-15T12:30:00Z",
      workspace: { mode: "workspace", name: "Test" },
    });

    const result = await fetchAgents("TEST 2");

    expect(result).toHaveLength(1);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/monitor/agents?workspace=TEST%202",
      { signal: expect.any(AbortSignal) },
    );
    expect(mockApiGet).not.toHaveBeenCalled();
  });

  it("returns empty array when API returns null agents", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { agents: null },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchAgents();

    expect(result).toEqual([]);
  });

  it("throws error on network failure (does not return empty array)", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Network error" },
      response: new Response(null, {
        status: 500,
        statusText: "Network error",
      }),
    } as never);

    await expect(fetchAgents()).rejects.toThrow();
  });

  it("throws ApiError on non-OK HTTP response", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Service Unavailable" },
      response: new Response(null, {
        status: 503,
        statusText: "Service Unavailable",
      }),
    } as never);

    await expect(fetchAgents()).rejects.toThrow(ApiError);
  });

  it("throws ApiError on server error (500)", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Internal Server Error" },
      response: new Response(null, {
        status: 500,
        statusText: "Internal Server Error",
      }),
    } as never);

    await expect(fetchAgents()).rejects.toThrow(ApiError);
  });

  it("passes correct path to api.GET()", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { agents: [] },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchAgents();

    expect(mockApiGet).toHaveBeenCalledWith("/api/monitor/agents", {
      signal: expect.any(AbortSignal),
    });
  });

  it("uses workspace-scoped monitor status when workspace is provided", async () => {
    mockGet.mockResolvedValueOnce({
      agents: null,
      tasks: {},
      agent_tasks: null,
      sync: {},
      stats: {},
      timestamp: "",
    } as never);

    await fetchStatus("test-ws");

    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/test-ws/monitor/status",
      {
        signal: expect.any(AbortSignal),
      },
    );
    expect(mockApiGet).not.toHaveBeenCalled();
  });
});

describe("checkLoomHealth", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("returns true when health endpoint responds ok", async () => {
    mockGet.mockResolvedValueOnce({ status: "ok" });

    const result = await checkLoomHealth();

    expect(result).toBe(true);
    expect(mockGet).toHaveBeenCalledWith(expect.stringContaining("/health"), {
      timeout: 15000,
    });
  });

  it("returns false when get() throws ApiError", async () => {
    mockGet.mockRejectedValueOnce(new ApiError(503, "Service Unavailable"));

    const result = await checkLoomHealth();

    expect(result).toBe(false);
  });

  it("returns false on network error (error is swallowed)", async () => {
    mockGet.mockRejectedValueOnce(new ApiError(0, "Network error"));

    const result = await checkLoomHealth();

    expect(result).toBe(false);
  });

  it("returns false on timeout error", async () => {
    mockGet.mockRejectedValueOnce(new ApiError(0, "Request timeout"));

    const result = await checkLoomHealth();

    expect(result).toBe(false);
  });
});

describe("startAgent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("returns pending when the workspace-scoped start endpoint accepts the command", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: {
        message: "agent start requested",
        pending: true,
        command_id: "agent-lifecycle-start-1",
        status: "queued",
      },
      error: undefined,
      response: new Response(null, { status: 202 }),
    } as never);

    await expect(startAgent("TEST 2", "nova")).resolves.toEqual({
      message: "agent start requested",
      pending: true,
      command_id: "agent-lifecycle-start-1",
      status: "queued",
    });

    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/{name}/start",
      { params: { path: { ws: "TEST 2", name: "nova" } } },
    );
  });

  it("returns settled and can include a requested task id", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: {
        message: "agent started",
        pending: false,
        status: "succeeded",
      },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);

    await expect(
      startAgent("TEST 2", "nova", { taskId: "TEST-1" }),
    ).resolves.toEqual({
      message: "agent started",
      pending: false,
      status: "succeeded",
    });

    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/{name}/start",
      {
        params: { path: { ws: "TEST 2", name: "nova" } },
        body: { payload: { task_id: "TEST-1" } },
      },
    );
  });

  it("throws on start errors", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: undefined,
      error: { error: "not found" },
      response: new Response(null, { status: 404, statusText: "Not Found" }),
    });

    await expect(startAgent("TEST", "missing")).rejects.toThrow(ApiError);
  });

  it("rejects a pending response without an authoritative command id", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: { message: "agent start requested", pending: true },
      error: undefined,
      response: new Response(null, { status: 202 }),
    } as never);

    await expect(startAgent("TEST", "nova")).rejects.toThrow(
      "pending command_id is missing",
    );
  });
});

describe("stopAgent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("reports asynchronous acceptance instead of waiting for lifecycle completion", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: {
        message: "agent stop requested",
        pending: true,
        command_id: "agent-lifecycle-stop-1",
      },
      error: undefined,
      response: new Response(null, { status: 202 }),
    } as never);

    await expect(stopAgent("TEST 2", "nova")).resolves.toEqual({
      message: "agent stop requested",
      pending: true,
      command_id: "agent-lifecycle-stop-1",
    });

    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/{name}/stop",
      { params: { path: { ws: "TEST 2", name: "nova" } } },
    );
  });

  it("reports synchronous force completion and forwards the force flag", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: { message: "agent force-stopped", pending: false },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);

    await expect(stopAgent("TEST 2", "nova", { force: true })).resolves.toEqual(
      {
        message: "agent force-stopped",
        pending: false,
      },
    );

    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/{name}/stop",
      {
        params: { path: { ws: "TEST 2", name: "nova" } },
        body: { force: true },
      },
    );
  });
});

describe("restartAgent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("reports asynchronous acceptance instead of waiting for lifecycle completion", async () => {
    mockApiPost.mockResolvedValueOnce({
      data: {
        message: "agent restart requested",
        pending: true,
        command_id: "agent-lifecycle-restart-1",
        status: "acked",
      },
      error: undefined,
      response: new Response(null, { status: 202 }),
    } as never);

    await expect(restartAgent("TEST 2", "nova")).resolves.toEqual({
      message: "agent restart requested",
      pending: true,
      command_id: "agent-lifecycle-restart-1",
      status: "acked",
    });

    expect(mockApiPost).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/agents/{name}/restart",
      { params: { path: { ws: "TEST 2", name: "nova" } } },
    );
  });
});

describe("getAgentLifecycleCommand", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("fetches and validates the authoritative command by encoded identity", async () => {
    const controller = new AbortController();
    mockGet.mockResolvedValueOnce({
      command_id: "command/1",
      action: "restart",
      status: "running",
      created_at: "2026-07-24T12:00:00Z",
    });

    await expect(
      getAgentLifecycleCommand("TEAM A", "review/one", "command/1", {
        signal: controller.signal,
      }),
    ).resolves.toEqual({
      command_id: "command/1",
      action: "restart",
      status: "running",
      created_at: "2026-07-24T12:00:00Z",
    });
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/TEAM%20A/agents/review%2Fone/lifecycle-commands/command%2F1",
      { signal: controller.signal },
    );
  });

  it("rejects unknown command statuses instead of guessing convergence", async () => {
    mockGet.mockResolvedValueOnce({
      command_id: "command-1",
      action: "restart",
      status: "lost",
    });

    await expect(
      getAgentLifecycleCommand("TEAM", "review", "command-1"),
    ).rejects.toThrow("Invalid agent lifecycle command response");
  });

  it("accepts the yield action exposed by the lifecycle command contract", async () => {
    mockGet.mockResolvedValueOnce({
      command_id: "command-yield-1",
      action: "yield",
      status: "succeeded",
    });

    await expect(
      getAgentLifecycleCommand("TEAM", "review", "command-yield-1"),
    ).resolves.toEqual({
      command_id: "command-yield-1",
      action: "yield",
      status: "succeeded",
    });
  });
});

describe("fetchStatus", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("successfully fetches status from API", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        agents: null,
        tasks: {
          needs_planning: 5,
          ready_to_implement: 3,
          in_progress: 2,
          need_review: 1,
          backlog: 4,
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: "2024-01-15T12:00:00Z",
        },
        stats: {
          open: 15,
          closed: 25,
          total: 40,
          completion: 62.5,
        },
        timestamp: "2024-01-15T12:30:00Z",
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchStatus();

    expect(result.tasks.needs_planning).toBe(5);
    expect(result.tasks.ready_to_implement).toBe(3);
    expect(result.tasks.in_progress).toBe(2);
    expect(result.tasks.need_review).toBe(1);
    expect(result.tasks.backlog).toBe(4);
  });

  it("passes through backlog field directly from API", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        agents: null,
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 10,
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: "2024-01-15T12:00:00Z",
        },
        stats: {
          open: 0,
          closed: 0,
          total: 10,
          completion: 0,
        },
        timestamp: "2024-01-15T12:30:00Z",
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchStatus();

    expect(result.tasks).toHaveProperty("backlog");
    expect(result.tasks.backlog).toBe(10);
  });

  it("returns tasks.backlog as 0 when backlog is 0", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        agents: null,
        tasks: {
          needs_planning: 5,
          ready_to_implement: 3,
          in_progress: 2,
          need_review: 1,
          backlog: 0,
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: "2024-01-15T12:00:00Z",
        },
        stats: {
          open: 11,
          closed: 25,
          total: 36,
          completion: 69.4,
        },
        timestamp: "2024-01-15T12:30:00Z",
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchStatus();

    expect(result.tasks.backlog).toBe(0);
  });

  it("preserves all other task status counts", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        agents: null,
        tasks: {
          needs_planning: 10,
          ready_to_implement: 20,
          in_progress: 15,
          need_review: 8,
          backlog: 5,
        },
        agent_tasks: null,
        sync: {
          db_synced: true,
          db_last_sync: "2024-01-15T12:00:00Z",
        },
        stats: {
          open: 53,
          closed: 100,
          total: 153,
          completion: 65.4,
        },
        timestamp: "2024-01-15T12:30:00Z",
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchStatus();

    expect(result.tasks.needs_planning).toBe(10);
    expect(result.tasks.ready_to_implement).toBe(20);
    expect(result.tasks.in_progress).toBe(15);
    expect(result.tasks.need_review).toBe(8);
    expect(result.tasks.backlog).toBe(5);
  });

  it("returns complete FetchStatusResult with all properties", async () => {
    const agents = [
      { name: "nova", branch: "main", status: "ready", ahead: 0, behind: 0 },
    ];

    mockApiGet.mockResolvedValueOnce({
      data: {
        agents,
        tasks: {
          needs_planning: 1,
          ready_to_implement: 2,
          in_progress: 3,
          need_review: 4,
          backlog: 5,
        },
        needs_planning: [
          { id: "loom-001", title: "Plan", priority: 2, status: "open" },
        ],
        ready_to_implement: [
          { id: "loom-002", title: "Implement", priority: 1, status: "open" },
        ],
        needs_review: [
          { id: "loom-004", title: "Review", priority: 1, status: "review" },
        ],
        in_progress: [
          {
            id: "loom-003",
            title: "In progress",
            priority: 0,
            status: "in_progress",
          },
        ],
        in_progress_list: [
          {
            id: "loom-003",
            title: "In progress",
            priority: 0,
            status: "in_progress",
          },
        ],
        backlog: [
          { id: "loom-005", title: "Blocked", priority: 3, status: "blocked" },
        ],
        closed: [
          { id: "loom-006", title: "Done", priority: 3, status: "closed" },
        ],
        agent_tasks: {
          nova: {
            id: "loom-123",
            title: "Test",
            priority: 1,
            status: "in_progress",
          },
        },
        sync: {
          db_synced: true,
          db_last_sync: "2024-01-15T12:00:00Z",
        },
        stats: {
          open: 15,
          closed: 25,
          total: 40,
          completion: 62.5,
        },
        timestamp: "2024-01-15T12:30:00Z",
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result: FetchStatusResult = await fetchStatus();

    expect(result).toHaveProperty("agents");
    expect(result).toHaveProperty("tasks");
    expect(result).toHaveProperty("taskLists");
    expect(result).toHaveProperty("agentTasks");
    expect(result).toHaveProperty("sync");
    expect(result).toHaveProperty("stats");
    expect(result).toHaveProperty("timestamp");

    expect(result.agents).toEqual(agents);
    expect(result.tasks.backlog).toBe(5);
    expect(result.taskLists.readyToImplement[0].id).toBe("loom-002");
    expect(result.taskLists.done[0].id).toBe("loom-006");
    expect(result.timestamp).toBe("2024-01-15T12:30:00Z");
  });

  it("throws ApiError on non-ok HTTP response", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Internal Server Error" },
      response: new Response(null, {
        status: 500,
        statusText: "Internal Server Error",
      }),
    } as never);

    await expect(fetchStatus()).rejects.toThrow(ApiError);
  });

  it("throws on network failure", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Network error" },
      response: new Response(null, {
        status: 500,
        statusText: "Network error",
      }),
    } as never);

    await expect(fetchStatus()).rejects.toThrow();
  });

  it("passes correct path to api.GET()", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        agents: null,
        tasks: {},
        agent_tasks: null,
        sync: {},
        stats: {},
        timestamp: "",
      },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchStatus();

    expect(mockApiGet).toHaveBeenCalledWith("/api/monitor/status", {
      signal: expect.any(AbortSignal),
    });
  });
});

describe("fetchTasks", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("successfully fetches task lists from API", async () => {
    const taskLists = {
      needsPlanning: [
        { id: "loom-001", title: "Plan feature", priority: 2, status: "open" },
      ],
      readyToImplement: [
        {
          id: "loom-002",
          title: "Implement feature",
          priority: 1,
          status: "open",
        },
      ],
      inProgress: [
        {
          id: "loom-003",
          title: "In progress task",
          priority: 0,
          status: "in_progress",
        },
      ],
      needsReview: [
        { id: "loom-004", title: "Review code", priority: 1, status: "review" },
      ],
      backlog: [
        {
          id: "loom-005",
          title: "Blocked task",
          priority: 3,
          status: "blocked",
        },
      ],
    };

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: taskLists.needsPlanning,
        ready_to_implement: taskLists.readyToImplement,
        in_progress: taskLists.inProgress,
        needs_review: taskLists.needsReview,
        backlog: taskLists.backlog,
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchTasks();

    expect(result.needsPlanning).toEqual(taskLists.needsPlanning);
    expect(result.readyToImplement).toEqual(taskLists.readyToImplement);
    expect(result.inProgress).toEqual(taskLists.inProgress);
    expect(result.needsReview).toEqual(taskLists.needsReview);
    expect(result.backlog).toEqual(taskLists.backlog);
  });

  it("passes through backlog field directly from API", async () => {
    const backlogTasks = [
      {
        id: "loom-100",
        title: "First blocked task",
        priority: 2,
        status: "blocked",
      },
      {
        id: "loom-101",
        title: "Second blocked task",
        priority: 3,
        status: "blocked",
      },
    ];

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: backlogTasks,
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result: LoomTaskLists = await fetchTasks();

    expect(result).toHaveProperty("backlog");
    expect(result.backlog).toEqual(backlogTasks);
  });

  it("returns empty backlog array when API sends null", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: null,
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchTasks();

    expect(result.backlog).toEqual([]);
  });

  it("returns backlog array with multiple tasks", async () => {
    const backlogTasks = Array.from({ length: 5 }, (_, i) => ({
      id: `loom-${200 + i}`,
      title: `Blocked task ${i + 1}`,
      priority: Math.floor(Math.random() * 5),
      status: "blocked" as const,
    }));

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: backlogTasks,
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchTasks();

    expect(result.backlog).toHaveLength(5);
    expect(result.backlog).toEqual(backlogTasks);
  });

  it("preserves all other task lists", async () => {
    const taskLists = {
      needsPlanning: [
        { id: "loom-010", title: "Plan", priority: 1, status: "open" },
      ],
      readyToImplement: [
        { id: "loom-020", title: "Ready 1", priority: 0, status: "open" },
        { id: "loom-021", title: "Ready 2", priority: 1, status: "open" },
      ],
      inProgress: [
        {
          id: "loom-030",
          title: "Working",
          priority: 0,
          status: "in_progress",
        },
      ],
      needsReview: [
        { id: "loom-040", title: "Review", priority: 1, status: "review" },
      ],
      backlog: [
        { id: "loom-050", title: "Blocked", priority: 2, status: "blocked" },
      ],
    };

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: taskLists.needsPlanning,
        ready_to_implement: taskLists.readyToImplement,
        in_progress: taskLists.inProgress,
        needs_review: taskLists.needsReview,
        backlog: taskLists.backlog,
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await fetchTasks();

    expect(result.needsPlanning).toEqual(taskLists.needsPlanning);
    expect(result.readyToImplement).toEqual(taskLists.readyToImplement);
    expect(result.inProgress).toEqual(taskLists.inProgress);
    expect(result.needsReview).toEqual(taskLists.needsReview);
    expect(result.backlog).toEqual(taskLists.backlog);
  });

  it("returns complete LoomTaskLists with all properties", async () => {
    const taskLists = {
      needsPlanning: [
        { id: "loom-001", title: "Plan", priority: 2, status: "open" },
      ],
      readyToImplement: [
        { id: "loom-002", title: "Implement", priority: 1, status: "open" },
      ],
      inProgress: [
        {
          id: "loom-003",
          title: "In progress",
          priority: 0,
          status: "in_progress",
        },
      ],
      needsReview: [
        { id: "loom-004", title: "Review", priority: 1, status: "review" },
      ],
      backlog: [
        { id: "loom-005", title: "Blocked", priority: 3, status: "blocked" },
      ],
    };

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: taskLists.needsPlanning,
        ready_to_implement: taskLists.readyToImplement,
        in_progress: taskLists.inProgress,
        needs_review: taskLists.needsReview,
        backlog: taskLists.backlog,
      },
      error: undefined,
      response: new Response(),
    } as never);

    const result: LoomTaskLists = await fetchTasks();

    expect(result).toHaveProperty("needsPlanning");
    expect(result).toHaveProperty("readyToImplement");
    expect(result).toHaveProperty("inProgress");
    expect(result).toHaveProperty("needsReview");
    expect(result).toHaveProperty("backlog");
  });

  it("throws ApiError on non-ok HTTP response", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Not Found" },
      response: new Response(null, { status: 404, statusText: "Not Found" }),
    } as never);

    await expect(fetchTasks()).rejects.toThrow(ApiError);
  });

  it("throws on network failure", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Network error" },
      response: new Response(null, {
        status: 500,
        statusText: "Network error",
      }),
    } as never);

    await expect(fetchTasks()).rejects.toThrow(ApiError);
  });

  it("passes correct path to api.GET()", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: null,
      },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchTasks();

    expect(mockApiGet).toHaveBeenCalledWith("/api/monitor/tasks", {
      signal: expect.any(AbortSignal),
    });
  });
});

describe("API field consistency", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("both fetchStatus and fetchTasks use consistent backlog field name", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: {
        agents: null,
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          backlog: 7,
        },
        agent_tasks: null,
        needs_planning: null,
        ready_to_implement: null,
        needs_review: null,
        in_progress: null,
        in_progress_list: null,
        backlog: [
          { id: "loom-100", title: "Blocked", priority: 0, status: "blocked" },
        ],
        closed: null,
        sync: { db_synced: true, db_last_sync: "2024-01-15T12:00:00Z" },
        stats: {
          open: 7,
          closed: 0,
          total: 7,
          completion: 0,
          remaining: 7,
          in_progress: 0,
          review: 0,
          blocked: 0,
        },
        timestamp: "2024-01-15T12:30:00Z",
      },
      error: undefined,
      response: new Response(),
    } as never);

    const statusResult = await fetchStatus();

    expect(statusResult.tasks.backlog).toBe(7);
    expect(statusResult.taskLists.backlog).toHaveLength(1);

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: [
          { id: "loom-100", title: "Blocked", priority: 0, status: "blocked" },
        ],
      },
      error: undefined,
      response: new Response(),
    } as never);

    const tasksResult = await fetchTasks();

    expect(tasksResult.backlog).toHaveLength(1);
    expect(tasksResult.backlog[0].id).toBe("loom-100");
  });
});
