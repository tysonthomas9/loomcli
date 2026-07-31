// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { listAgentRuns, type AgentRunsResponse } from "@/api/agents";

import { useAgentHistory } from "../useAgentHistory";

vi.mock("@/api/agents", () => ({
  listAgentRuns: vi.fn(),
}));

const mockListAgentRuns = vi.mocked(listAgentRuns);

function historyResponse(
  agentId: string,
  status: AgentRunsResponse["sessions"][number]["status"] = "completed",
): AgentRunsResponse {
  return {
    agent_id: agentId,
    runs: [],
    sessions: [
      {
        workspace_key: "WS",
        session_id: `session-${agentId}`,
        agent_id: agentId,
        kind: "task",
        task_id: "TASK-1",
        status,
        created_at: "2026-07-23T00:00:00Z",
        updated_at: "2026-07-23T00:00:01Z",
      },
    ],
  };
}

function emptyResponse(agentId: string): AgentRunsResponse {
  return { agent_id: agentId, runs: [], sessions: [] };
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
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads the selected agent's unified history", async () => {
    mockListAgentRuns.mockResolvedValue(historyResponse("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));

    await waitFor(() => expect(result.current.sessions).toHaveLength(1));
    expect(mockListAgentRuns).toHaveBeenCalledWith("WS", "coder", {
      limit: 25,
    });
    expect(result.current.sessions[0]?.session_id).toBe("session-coder");
    expect(result.current.error).toBeNull();
    expect(result.current.isLoading).toBe(false);
  });

  it("discards a late response after the selected agent changes", async () => {
    const first = deferred<AgentRunsResponse>();
    const second = deferred<AgentRunsResponse>();
    mockListAgentRuns
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const { result, rerender } = renderHook(
      ({ agentId }) => useAgentHistory("WS", agentId),
      { initialProps: { agentId: "planner" as string | null } },
    );

    rerender({ agentId: "coder" });
    expect(result.current.sessions).toEqual([]);
    await act(async () => second.resolve(historyResponse("coder")));
    await waitFor(() =>
      expect(result.current.sessions[0]?.agent_id).toBe("coder"),
    );

    await act(async () => first.resolve(historyResponse("planner")));
    expect(result.current.sessions[0]?.agent_id).toBe("coder");
  });

  it("serializes polling and refetches for the same agent", async () => {
    vi.useFakeTimers();
    const slow = deferred<AgentRunsResponse>();
    mockListAgentRuns
      .mockReturnValueOnce(slow.promise)
      .mockResolvedValueOnce(historyResponse("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(mockListAgentRuns).toHaveBeenCalledTimes(1);

    act(() => {
      result.current.refetch();
      result.current.refetch();
    });
    await act(async () => {
      vi.advanceTimersByTime(9_000);
    });
    expect(mockListAgentRuns).toHaveBeenCalledTimes(1);

    await act(async () => {
      slow.resolve(emptyResponse("coder"));
      await Promise.resolve();
    });
    await act(async () => {
      vi.advanceTimersByTime(3_000);
      await Promise.resolve();
    });

    expect(mockListAgentRuns).toHaveBeenCalledTimes(2);
    expect(result.current.sessions[0]?.session_id).toBe("session-coder");
  });

  it("polls empty history so a newly started session becomes visible", async () => {
    vi.useFakeTimers();
    mockListAgentRuns
      .mockResolvedValueOnce(emptyResponse("coder"))
      .mockResolvedValueOnce(historyResponse("coder", "running"));

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

  it("recovers from a transient history failure on the next poll", async () => {
    vi.useFakeTimers();
    mockListAgentRuns
      .mockRejectedValueOnce(new Error("FleetDB temporarily unavailable"))
      .mockResolvedValueOnce(historyResponse("coder"));

    const { result } = renderHook(() => useAgentHistory("WS", "coder"));
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.error?.message).toBe(
      "FleetDB temporarily unavailable",
    );
    expect(result.current.sessions).toEqual([]);

    await act(async () => {
      vi.advanceTimersByTime(3_000);
      await Promise.resolve();
    });

    expect(mockListAgentRuns).toHaveBeenCalledTimes(2);
    expect(result.current.sessions[0]?.session_id).toBe("session-coder");
    expect(result.current.error).toBeNull();
    expect(result.current.isLoading).toBe(false);
  });

  it("does not load or poll history while its pane is hidden", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useAgentHistory("WS", "coder", false));
    await act(async () => {
      vi.advanceTimersByTime(9_000);
      await Promise.resolve();
    });

    expect(mockListAgentRuns).not.toHaveBeenCalled();
    expect(result.current.sessions).toEqual([]);
    expect(result.current.isLoading).toBe(false);
  });
});
