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

import { ApiError, api, get } from "../client";

import {
  fetchAgents,
  fetchStatus,
  fetchTasks,
  checkLoomHealth,
  type FetchStatusResult,
} from "../agents";

vi.mock("../client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../client")>();
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
  };
});

const mockApiGet = vi.mocked(api.GET);
const mockGet = vi.mocked(get);

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
        status: "working:bd-123",
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
        agent_tasks: {
          nova: {
            id: "bd-123",
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
    expect(result).toHaveProperty("agentTasks");
    expect(result).toHaveProperty("sync");
    expect(result).toHaveProperty("stats");
    expect(result).toHaveProperty("timestamp");

    expect(result.agents).toEqual(agents);
    expect(result.tasks.backlog).toBe(5);
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
        { id: "bd-001", title: "Plan feature", priority: 2, status: "open" },
      ],
      readyToImplement: [
        {
          id: "bd-002",
          title: "Implement feature",
          priority: 1,
          status: "open",
        },
      ],
      inProgress: [
        {
          id: "bd-003",
          title: "In progress task",
          priority: 0,
          status: "in_progress",
        },
      ],
      needsReview: [
        { id: "bd-004", title: "Review code", priority: 1, status: "review" },
      ],
      backlog: [
        { id: "bd-005", title: "Blocked task", priority: 3, status: "blocked" },
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
        id: "bd-100",
        title: "First blocked task",
        priority: 2,
        status: "blocked",
      },
      {
        id: "bd-101",
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
      id: `bd-${200 + i}`,
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
        { id: "bd-010", title: "Plan", priority: 1, status: "open" },
      ],
      readyToImplement: [
        { id: "bd-020", title: "Ready 1", priority: 0, status: "open" },
        { id: "bd-021", title: "Ready 2", priority: 1, status: "open" },
      ],
      inProgress: [
        { id: "bd-030", title: "Working", priority: 0, status: "in_progress" },
      ],
      needsReview: [
        { id: "bd-040", title: "Review", priority: 1, status: "review" },
      ],
      backlog: [
        { id: "bd-050", title: "Blocked", priority: 2, status: "blocked" },
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
        { id: "bd-001", title: "Plan", priority: 2, status: "open" },
      ],
      readyToImplement: [
        { id: "bd-002", title: "Implement", priority: 1, status: "open" },
      ],
      inProgress: [
        {
          id: "bd-003",
          title: "In progress",
          priority: 0,
          status: "in_progress",
        },
      ],
      needsReview: [
        { id: "bd-004", title: "Review", priority: 1, status: "review" },
      ],
      backlog: [
        { id: "bd-005", title: "Blocked", priority: 3, status: "blocked" },
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

    mockApiGet.mockResolvedValueOnce({
      data: {
        needs_planning: null,
        ready_to_implement: null,
        in_progress: null,
        needs_review: null,
        backlog: [
          { id: "bd-100", title: "Blocked", priority: 0, status: "blocked" },
        ],
      },
      error: undefined,
      response: new Response(),
    } as never);

    const tasksResult = await fetchTasks();

    expect(tasksResult.backlog).toHaveLength(1);
    expect(tasksResult.backlog[0].id).toBe("bd-100");
  });
});
