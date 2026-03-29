/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useGitStatus hook.
 * Covers polling lifecycle, cleanup on unmount, and error handling.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchGitStatus } from "@/api/git";
import type { GitStatus } from "@/api/git";

import { useGitStatus } from "../useGitStatus";

vi.mock("@/api/git", () => ({
  fetchGitStatus: vi.fn(),
}));

const mockFetchGitStatus = vi.mocked(fetchGitStatus);

function createMockGitStatus(overrides?: Partial<GitStatus>): GitStatus {
  return {
    branch: "feature-x",
    target_branch: "main",
    is_clean: true,
    ahead: 0,
    behind: 0,
    changed_files: [],
    conflicted_files: [],
    has_conflicts: false,
    stash_count: 0,
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useGitStatus", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockFetchGitStatus.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns null status, no loading, no error when disabled", () => {
      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: false,
        }),
      );

      expect(result.current.status).toBeNull();
      expect(result.current.loading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("returns null status when agentName is null", () => {
      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: null,
          enabled: true,
        }),
      );

      expect(result.current.status).toBeNull();
      expect(result.current.loading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe("fetching", () => {
    it("fetches immediately when enabled with an agent name", async () => {
      const gitStatus = createMockGitStatus({ ahead: 3 });
      mockFetchGitStatus.mockResolvedValueOnce(gitStatus);

      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(mockFetchGitStatus).toHaveBeenCalledWith("test-ws-id", "ember");
      expect(result.current.status).toEqual(gitStatus);
      expect(result.current.loading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("does not fetch when disabled", async () => {
      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: false,
        }),
      );

      await flushPromises();

      expect(mockFetchGitStatus).not.toHaveBeenCalled();
      expect(result.current.status).toBeNull();
    });

    it("does not fetch when agentName is null", async () => {
      renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: null,
          enabled: true,
        }),
      );

      await flushPromises();

      expect(mockFetchGitStatus).not.toHaveBeenCalled();
    });
  });

  describe("polling", () => {
    it("polls at 5-second intervals", async () => {
      const gitStatus1 = createMockGitStatus({ ahead: 1 });
      const gitStatus2 = createMockGitStatus({ ahead: 2 });

      mockFetchGitStatus.mockResolvedValueOnce(gitStatus1);

      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      // Initial fetch
      await flushPromises();
      expect(result.current.status?.ahead).toBe(1);
      expect(mockFetchGitStatus).toHaveBeenCalledTimes(1);

      // Advance to trigger poll
      mockFetchGitStatus.mockResolvedValueOnce(gitStatus2);

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockFetchGitStatus).toHaveBeenCalledTimes(2);
      expect(result.current.status?.ahead).toBe(2);
    });

    it("does not poll before 5 seconds", async () => {
      mockFetchGitStatus.mockResolvedValueOnce(createMockGitStatus());

      renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();
      expect(mockFetchGitStatus).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(4999);
      });
      await flushPromises();

      expect(mockFetchGitStatus).toHaveBeenCalledTimes(1);
    });
  });

  describe("error handling", () => {
    it("sets error on fetch failure", async () => {
      mockFetchGitStatus.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Network error");
      expect(result.current.status).toBeNull();
    });

    it("wraps non-Error thrown values", async () => {
      mockFetchGitStatus.mockRejectedValueOnce("string error");

      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });

    it("clears error on successful subsequent fetch", async () => {
      // First fetch fails
      mockFetchGitStatus.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();
      expect(result.current.error).not.toBeNull();

      // Poll interval triggers successful fetch
      mockFetchGitStatus.mockResolvedValueOnce(createMockGitStatus());

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.status).not.toBeNull();
    });
  });

  describe("agent change", () => {
    it("resets state when agent name changes", async () => {
      const gitStatus = createMockGitStatus({ ahead: 5 });
      mockFetchGitStatus.mockResolvedValueOnce(gitStatus);

      const { result, rerender } = renderHook(
        ({ agentName }: { agentName: string }) =>
          useGitStatus({ workspaceId: "test-ws-id", agentName, enabled: true }),
        { initialProps: { agentName: "ember" } },
      );

      await flushPromises();
      expect(result.current.status?.ahead).toBe(5);

      // Change agent — state should reset
      mockFetchGitStatus.mockResolvedValueOnce(
        createMockGitStatus({ ahead: 0, branch: "other-branch" }),
      );

      rerender({ agentName: "nova" });
      await flushPromises();

      expect(mockFetchGitStatus).toHaveBeenLastCalledWith("test-ws-id", "nova");
    });
  });

  describe("cleanup on unmount", () => {
    it("clears polling interval on unmount", async () => {
      const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");

      mockFetchGitStatus.mockResolvedValueOnce(createMockGitStatus());

      const { unmount } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();

      clearIntervalSpy.mockClear();

      unmount();

      expect(clearIntervalSpy).toHaveBeenCalled();

      clearIntervalSpy.mockRestore();
    });

    it("does not update state after unmount", async () => {
      // Use a manually resolvable promise to control timing
      let resolvePromise!: (value: GitStatus) => void;
      mockFetchGitStatus.mockImplementationOnce(
        () =>
          new Promise<GitStatus>((resolve) => {
            resolvePromise = resolve;
          }),
      );

      const { result, unmount } = renderHook(() =>
        useGitStatus({
          workspaceId: "test-ws-id",
          agentName: "ember",
          enabled: true,
        }),
      );

      // Unmount before fetch resolves
      unmount();

      // Resolve after unmount — should not throw or update state
      await act(async () => {
        resolvePromise(createMockGitStatus({ ahead: 99 }));
      });

      // Status should remain null (never updated after unmount)
      expect(result.current.status).toBeNull();
    });
  });
});
