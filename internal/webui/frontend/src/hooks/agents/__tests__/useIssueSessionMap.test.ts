/**
 * @vitest-environment jsdom
 */
import { renderHook, waitFor, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useIssueSessionMap } from "../useIssueSessionMap";
import * as terminalApi from "@/api/terminal";

vi.mock("@/api/terminal", () => ({
  listSessionsByIssue: vi.fn(),
}));

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useEventSubscription: vi.fn() };
});

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws" }),
  };
});

describe("useIssueSessionMap", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("starts with an empty map", () => {
    vi.mocked(terminalApi.listSessionsByIssue).mockReturnValue(
      new Promise(() => {}),
    );

    const { result } = renderHook(() => useIssueSessionMap());

    expect(result.current.issueSessionMap).toEqual({});
  });

  it("fetches session map on mount", async () => {
    const mockData: Record<string, string[]> = {
      "PROJ-1": ["s1", "s2"],
      "PROJ-2": ["s3"],
    };
    vi.mocked(terminalApi.listSessionsByIssue).mockResolvedValue(mockData);

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(result.current.issueSessionMap).toEqual(mockData);
    });

    expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
  });

  it("hasActiveSession returns true for issues with sessions", async () => {
    vi.mocked(terminalApi.listSessionsByIssue).mockResolvedValue({
      "PROJ-1": ["s1"],
    });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(result.current.hasActiveSession("PROJ-1")).toBe(true);
    });
  });

  it("hasActiveSession returns false for issues without sessions", async () => {
    vi.mocked(terminalApi.listSessionsByIssue).mockResolvedValue({
      "PROJ-1": ["s1"],
    });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(result.current.issueSessionMap).toEqual({ "PROJ-1": ["s1"] });
    });

    expect(result.current.hasActiveSession("PROJ-2")).toBe(false);
  });

  it("hasActiveSession returns false for empty session array", async () => {
    vi.mocked(terminalApi.listSessionsByIssue).mockResolvedValue({
      "PROJ-1": [],
    });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(result.current.issueSessionMap).toEqual({ "PROJ-1": [] });
    });

    expect(result.current.hasActiveSession("PROJ-1")).toBe(false);
  });

  it("silently handles fetch errors", async () => {
    vi.mocked(terminalApi.listSessionsByIssue).mockRejectedValue(
      new Error("network error"),
    );

    const { result } = renderHook(() => useIssueSessionMap());

    // Wait for the rejected promise to settle
    await waitFor(() => {
      expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
    });

    // Should stay as empty map on error
    expect(result.current.issueSessionMap).toEqual({});
  });

  it("refetch fetches updated data", async () => {
    vi.mocked(terminalApi.listSessionsByIssue)
      .mockResolvedValueOnce({ "PROJ-1": ["s1"] })
      .mockResolvedValueOnce({ "PROJ-1": ["s1", "s2"] });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(result.current.issueSessionMap).toEqual({ "PROJ-1": ["s1"] });
    });

    await act(async () => {
      await result.current.refetch();
    });

    await waitFor(() => {
      expect(result.current.issueSessionMap).toEqual({
        "PROJ-1": ["s1", "s2"],
      });
    });

    expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(2);
  });

  it("handleMutation triggers debounced refetch for terminal_session_change", async () => {
    vi.mocked(terminalApi.listSessionsByIssue)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ "PROJ-1": ["s1"] });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
    });

    act(() => {
      result.current.handleMutation({
        type: "terminal_session_change",
        issue_id: "",
        timestamp: new Date().toISOString(),
      });
    });

    // Wait for debounce (200ms) + refetch to complete
    await waitFor(
      () => {
        expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(2);
      },
      { timeout: 2000 },
    );
  }, 10000);

  it("handleMutation triggers debounced refetch for terminal_metadata", async () => {
    vi.mocked(terminalApi.listSessionsByIssue)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ "PROJ-1": ["s1"] });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
    });

    act(() => {
      result.current.handleMutation({
        type: "terminal_metadata",
        issue_id: "",
        timestamp: new Date().toISOString(),
      });
    });

    await waitFor(
      () => {
        expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(2);
      },
      { timeout: 2000 },
    );
  }, 10000);

  it("handleMutation ignores unrelated mutation types", async () => {
    vi.mocked(terminalApi.listSessionsByIssue).mockResolvedValue({});

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
    });

    act(() => {
      result.current.handleMutation({
        type: "update",
        issue_id: "PROJ-1",
        timestamp: new Date().toISOString(),
      });
    });

    // Wait past the debounce window and verify no additional call was made
    await new Promise((r) => setTimeout(r, 400));

    expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
  });

  it("handleMutation debounces multiple rapid calls", async () => {
    vi.mocked(terminalApi.listSessionsByIssue)
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ "PROJ-1": ["s1"] });

    const { result } = renderHook(() => useIssueSessionMap());

    await waitFor(() => {
      expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(1);
    });

    // Fire multiple mutations rapidly
    act(() => {
      result.current.handleMutation({
        type: "terminal_session_change",
        issue_id: "",
        timestamp: new Date().toISOString(),
      });
      result.current.handleMutation({
        type: "terminal_session_change",
        issue_id: "",
        timestamp: new Date().toISOString(),
      });
      result.current.handleMutation({
        type: "terminal_session_change",
        issue_id: "",
        timestamp: new Date().toISOString(),
      });
    });

    // Wait for debounce + refetch: should only result in 1 additional call
    await waitFor(
      () => {
        expect(terminalApi.listSessionsByIssue).toHaveBeenCalledTimes(2);
      },
      { timeout: 2000 },
    );
  }, 10000);
});
