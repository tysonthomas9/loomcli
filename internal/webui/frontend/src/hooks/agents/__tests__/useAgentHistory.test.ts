// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  listAgentActivity,
  listAgentRuns,
  type AgentActivityResponse,
  type AgentRunsResponse,
} from "@/api/agents";

import { useAgentHistory } from "../useAgentHistory";

vi.mock("@/api/agents", () => ({
  listAgentActivity: vi.fn(),
  listAgentRuns: vi.fn(),
}));

const mockListAgentRuns = vi.mocked(listAgentRuns);
const mockListAgentActivity = vi.mocked(listAgentActivity);

function activityResponse(
  agentId: string,
  status = "completed",
): AgentActivityResponse {
  return {
    agent_id: agentId,
    count: 1,
    activity: [
      {
        workspace_key: "WS",
        agent_id: agentId,
        kind: "agent_session",
        source_id: `session-${agentId}`,
        task_id: "TASK-1",
        status,
        started_at: "2026-07-23T00:00:00Z",
        finished_at: "2026-07-23T00:00:01Z",
      },
    ],
  };
}

function emptyResponse(agentId: string): AgentRunsResponse {
  return { agent_id: agentId, runs: [], sessions: [] };
}

function emptyActivity(agentId: string): AgentActivityResponse {
  return { agent_id: agentId, activity: [], count: 0 };
}

function historySession(agentId: string) {
  return {
    workspace_key: "WS",
    session_id: `canonical-${agentId}`,
    agent_id: agentId,
    kind: "task" as const,
    task_id: "TASK-0",
    status: "completed" as const,
    created_at: "2026-07-22T00:00:00Z",
    updated_at: "2026-07-22T00:00:01Z",
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
} {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("useAgentHistory", () => {
  beforeEach(() => {
    mockListAgentRuns.mockReset();
    mockListAgentActivity.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads the selected agent's unified history", async () => {
    mockListAgentRuns.mockResolvedValue(emptyResponse("coder"));
    mockListAgentActivity.mockResolvedValue(activityResponse("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));

    await waitFor(() => expect(result.current.sessions).toHaveLength(1));
    expect(mockListAgentRuns).toHaveBeenCalledWith("WS", "coder", {
      limit: 25,
    });
    expect(mockListAgentActivity).toHaveBeenCalledWith("WS", "coder", {
      limit: 25,
    });
    expect(result.current.sessions[0]?.session_id).toBe("session-coder");
    expect(result.current.error).toBeNull();
    expect(result.current.isLoading).toBe(false);
  });

  it("discards a late response after the selected agent changes", async () => {
    const first = deferred<AgentActivityResponse>();
    const second = deferred<AgentActivityResponse>();
    mockListAgentRuns.mockResolvedValue(emptyResponse("agent"));
    mockListAgentActivity
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const { result, rerender } = renderHook(
      ({ agentId }) => useAgentHistory("WS", agentId),
      { initialProps: { agentId: "planner" as string | null } },
    );

    rerender({ agentId: "coder" });
    expect(result.current.sessions).toEqual([]);
    await act(async () => second.resolve(activityResponse("coder")));
    await waitFor(() =>
      expect(result.current.sessions[0]?.agent_id).toBe("coder"),
    );

    await act(async () => first.resolve(activityResponse("planner")));
    expect(result.current.sessions[0]?.agent_id).toBe("coder");
  });

  it("serializes polling and refetches for the same agent", async () => {
    vi.useFakeTimers();
    const slow = deferred<AgentActivityResponse>();
    mockListAgentRuns.mockResolvedValue(emptyResponse("coder"));
    mockListAgentActivity
      .mockReturnValueOnce(slow.promise)
      .mockResolvedValueOnce(activityResponse("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockListAgentRuns).toHaveBeenCalledTimes(1);
    expect(mockListAgentActivity).toHaveBeenCalledTimes(1);

    act(() => {
      result.current.refetch();
      result.current.refetch();
    });
    await act(async () => {
      vi.advanceTimersByTime(9_000);
    });
    expect(mockListAgentRuns).toHaveBeenCalledTimes(1);
    expect(mockListAgentActivity).toHaveBeenCalledTimes(1);

    await act(async () => {
      slow.resolve(emptyActivity("coder"));
      await Promise.resolve();
    });
    await act(async () => {
      vi.advanceTimersByTime(3_000);
      await Promise.resolve();
    });

    expect(mockListAgentRuns).toHaveBeenCalledTimes(2);
    expect(mockListAgentActivity).toHaveBeenCalledTimes(2);
    expect(result.current.sessions[0]?.session_id).toBe("session-coder");
  });

  it("polls empty history so a newly started session becomes visible", async () => {
    vi.useFakeTimers();
    mockListAgentRuns.mockResolvedValue(emptyResponse("coder"));
    mockListAgentActivity
      .mockResolvedValueOnce(emptyActivity("coder"))
      .mockResolvedValueOnce(activityResponse("coder", "running"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.sessions).toEqual([]);

    await act(async () => {
      vi.advanceTimersByTime(3_000);
      await Promise.resolve();
    });
    expect(mockListAgentRuns).toHaveBeenCalledTimes(2);
    expect(result.current.sessions[0]?.status).toBe("running");
  });

  it("keeps established history usable when activity is unavailable", async () => {
    mockListAgentRuns.mockResolvedValue({
      agent_id: "coder",
      runs: [],
      sessions: [historySession("coder")],
    });
    mockListAgentActivity.mockRejectedValue(new Error("route unavailable"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.sessions).toEqual([historySession("coder")]);
    expect(result.current.error).toBeNull();
  });

  it("keeps established history usable with a legacy activity wire shape", async () => {
    mockListAgentRuns.mockResolvedValue({
      agent_id: "coder",
      runs: [],
      sessions: [historySession("coder")],
    });
    mockListAgentActivity.mockResolvedValue(
      [] as unknown as AgentActivityResponse,
    );

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.sessions).toEqual([historySession("coder")]);
    expect(result.current.error).toBeNull();
  });

  it("recovers from a transient activity failure on the next poll", async () => {
    vi.useFakeTimers();
    mockListAgentRuns.mockResolvedValue(emptyResponse("coder"));
    mockListAgentActivity
      .mockRejectedValueOnce(new Error("FleetDB temporarily unavailable"))
      .mockResolvedValueOnce(activityResponse("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.error).toBeNull();
    expect(result.current.sessions).toEqual([]);

    await act(async () => {
      vi.advanceTimersByTime(3_000);
      await Promise.resolve();
    });

    expect(mockListAgentRuns).toHaveBeenCalledTimes(2);
    expect(mockListAgentActivity).toHaveBeenCalledTimes(2);
    expect(result.current.sessions[0]?.session_id).toBe("session-coder");
    expect(result.current.error).toBeNull();
    expect(result.current.isLoading).toBe(false);
  });

  it("surfaces a runs failure when activity has no session history", async () => {
    mockListAgentRuns.mockRejectedValue(new Error("runs unavailable"));
    mockListAgentActivity.mockResolvedValue(emptyActivity("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.sessions).toEqual([]);
    expect(result.current.error?.message).toBe("runs unavailable");
  });

  it("does not load or poll history while its pane is hidden", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useAgentHistory("WS", "coder", false));
    await act(async () => {
      vi.advanceTimersByTime(9_000);
      await Promise.resolve();
    });

    expect(mockListAgentRuns).not.toHaveBeenCalled();
    expect(mockListAgentActivity).not.toHaveBeenCalled();
    expect(result.current.sessions).toEqual([]);
    expect(result.current.isLoading).toBe(false);
  });
});
