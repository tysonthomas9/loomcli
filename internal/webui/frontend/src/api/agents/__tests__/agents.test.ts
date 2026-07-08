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

import { ApiError, api, get, post } from "@/api/common";

import {
  fetchStatus,
  checkLoomHealth,
  startAgent,
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
    post: vi.fn(),
  };
});

const mockApiGet = vi.mocked(api.GET);
const mockGet = vi.mocked(get);
const mockPost = vi.mocked(post);

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

  it("posts to the workspace-scoped start endpoint", async () => {
    mockPost.mockResolvedValueOnce({ message: "agent started" });

    await startAgent("TEST 2", "nova");

    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/TEST%202/agents/nova/start",
      undefined,
    );
  });

  it("can include a requested task id", async () => {
    mockPost.mockResolvedValueOnce({ message: "agent started" });

    await startAgent("TEST 2", "nova", { taskId: "TEST-1" });

    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/TEST%202/agents/nova/start",
      { payload: { task_id: "TEST-1" } },
    );
  });

  it("throws on start errors", async () => {
    mockPost.mockRejectedValueOnce(new ApiError(404, "Not Found"));

    await expect(startAgent("TEST", "missing")).rejects.toThrow(ApiError);
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

describe("API field consistency", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("fetchStatus uses consistent backlog field name", async () => {
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
  });
});
