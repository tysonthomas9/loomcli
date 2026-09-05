/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useGitStatus hook.
 * Covers polling lifecycle, cleanup on unmount, and error handling.
 */

import { createElement, type ReactNode } from "react";
import {
  QueryRecoveryContext,
  QueryRecoveryCoordinator,
} from "@/hooks/common/queryRecovery";

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchGitStatus } from "@/api/workspace";
import type { GitStatus } from "@/api/workspace";

import { useGitStatus } from "../useGitStatus";

vi.mock("@/api/workspace", () => ({
  fetchGitStatus: vi.fn(),
}));

const scope = vi.hoisted(() => ({ workspaceId: "test-ws-id" }));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: scope.workspaceId }),
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
    scope.workspaceId = "test-ws-id";
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns null status, no loading, no error when disabled", () => {
      const { result } = renderHook(() =>
        useGitStatus({
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
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(mockFetchGitStatus).toHaveBeenCalledWith("test-ws-id", "ember", {
        signal: expect.any(AbortSignal),
      });
      expect(result.current.status).toEqual(gitStatus);
      expect(result.current.loading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("does not fetch when disabled", async () => {
      const { result } = renderHook(() =>
        useGitStatus({
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
          useGitStatus({ agentName, enabled: true }),
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

      expect(mockFetchGitStatus).toHaveBeenLastCalledWith(
        "test-ws-id",
        "nova",
        { signal: expect.any(AbortSignal) },
      );
    });
  });

  describe("cleanup on unmount", () => {
    it("clears polling interval on unmount", async () => {
      const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");

      mockFetchGitStatus.mockResolvedValueOnce(createMockGitStatus());

      const { unmount } = renderHook(() =>
        useGitStatus({
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
          agentName: "ember",
          enabled: true,
        }),
      );

      await flushPromises();
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

function deferredStatus() {
  let resolve!: (value: GitStatus) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<GitStatus>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
describe("git status recovery", () => {
  beforeEach(() => {
    mockFetchGitStatus.mockReset();
    scope.workspaceId = "A";
  });
  it("starts post-fence work, joins ordinary refresh, and rejects recovery failure", async () => {
    const coordinator = new QueryRecoveryCoordinator("A");
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(
        QueryRecoveryContext.Provider,
        { value: coordinator },
        children,
      );
    const old = deferredStatus(),
      fresh = deferredStatus();
    mockFetchGitStatus
      .mockReturnValueOnce(old.promise)
      .mockReturnValueOnce(fresh.promise);
    const hook = renderHook(
      () => useGitStatus({ agentName: "agent", enabled: true }),
      { wrapper },
    );
    await flushPromises();
    let recovery!: Promise<void>;
    act(() => {
      recovery = coordinator.refresh();
    });
    const failed = expect(recovery).rejects.toThrow("unavailable");
    await flushPromises();
    act(() => {
      void hook.result.current.refetch();
    });
    expect(mockFetchGitStatus).toHaveBeenCalledTimes(2);
    await act(async () => {
      old.resolve(createMockGitStatus({ ahead: 99 }));
    });
    expect(hook.result.current.status).toBeNull();
    await act(async () => {
      fresh.reject(new Error("unavailable"));
      await failed;
    });
    expect(hook.result.current.error?.message).toBe("unavailable");
    mockFetchGitStatus.mockResolvedValueOnce(createMockGitStatus({ ahead: 2 }));
    await act(async () => coordinator.refresh());
    expect(hook.result.current.status?.ahead).toBe(2);
    hook.unmount();
  });
  it.each(["workspace", "agent"])(
    "fences %s A to B to A responses",
    async (kind) => {
      const old = deferredStatus();
      mockFetchGitStatus
        .mockReturnValueOnce(old.promise)
        .mockResolvedValue(createMockGitStatus({ ahead: 2 }));
      const hook = renderHook(
        ({ agent }) => useGitStatus({ agentName: agent, enabled: true }),
        { initialProps: { agent: "A" } },
      );
      await flushPromises();
      if (kind === "workspace") scope.workspaceId = "B";
      hook.rerender({ agent: kind === "agent" ? "B" : "A" });
      await flushPromises();
      scope.workspaceId = "A";
      hook.rerender({ agent: "A" });
      await flushPromises();
      await act(async () => old.resolve(createMockGitStatus({ ahead: 99 })));
      expect(hook.result.current.status?.ahead).toBe(2);
      expect(mockFetchGitStatus).toHaveBeenCalledTimes(3);
      hook.unmount();
    },
  );
  it("disabling cancels active work and manual refresh cannot fetch", async () => {
    const old = deferredStatus();
    mockFetchGitStatus.mockReturnValueOnce(old.promise);
    const hook = renderHook(
      ({ enabled }) => useGitStatus({ agentName: "agent", enabled }),
      { initialProps: { enabled: true } },
    );
    await flushPromises();
    hook.rerender({ enabled: false });
    await act(async () => {
      await hook.result.current.refetch();
      old.resolve(createMockGitStatus({ ahead: 99 }));
    });
    expect(hook.result.current.status).toBeNull();
    expect(hook.result.current.loading).toBe(false);
    expect(mockFetchGitStatus).toHaveBeenCalledTimes(1);
    hook.unmount();
  });
});
