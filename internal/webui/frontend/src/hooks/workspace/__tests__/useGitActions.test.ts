/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useGitActions hook.
 * Covers loading states, toast calls, error handling for 409/423/503,
 * PR already exists / no commits cases, and onStatusChange callbacks.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { createElement } from "react";

import { ApiError } from "@/api/common";
import { ToastProvider } from "@/hooks/ui";

import { useGitActions } from "@/hooks/workspace";

// Mock git API functions
const mockGitPull = vi.fn();
const mockGitCreatePR = vi.fn();
const mockGitReset = vi.fn();
const mockGitUpdateTarget = vi.fn();

vi.mock("@/api/workspace", () => ({
  gitPull: (...args: unknown[]) => mockGitPull(...args),
  gitCreatePR: (...args: unknown[]) => mockGitCreatePR(...args),
  gitReset: (...args: unknown[]) => mockGitReset(...args),
  gitUpdateTarget: (...args: unknown[]) => mockGitUpdateTarget(...args),
}));

vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
}));

// Mock useToast to track showToast calls
const { mockShowToast } = vi.hoisted(() => ({ mockShowToast: vi.fn() }));

vi.mock("@/hooks/ui/useToast", async () => {
  const actual = await vi.importActual<typeof import("@/hooks/ui/useToast")>(
    "@/hooks/ui/useToast",
  );
  return {
    ...actual,
    useToast: () => ({
      toasts: [],
      showToast: mockShowToast,
      dismissToast: vi.fn(),
      dismissAll: vi.fn(),
    }),
  };
});

/** Wrapper providing ToastProvider context. */
function wrapper({ children }: { children: ReactNode }) {
  return createElement(ToastProvider, null, children);
}

describe("useGitActions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("initial state", () => {
    it("returns all states as not loading with no errors", () => {
      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      expect(result.current.pullState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(result.current.prState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(result.current.resetState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(result.current.targetState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(result.current.anyLoading).toBe(false);
    });
  });

  describe("pull", () => {
    it("shows success toast on successful pull", async () => {
      mockGitPull.mockResolvedValue({
        success: true,
        message: "Pulled 2 commits",
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "falcon" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.pull("origin/main");
      });

      expect(mockGitPull).toHaveBeenCalledWith(
        "test-ws-id",
        "falcon",
        "origin/main",
      );
      expect(mockShowToast).toHaveBeenCalledWith("Pulled 2 commits", {
        type: "success",
      });
    });

    it("uses default message when result.message is empty", async () => {
      mockGitPull.mockResolvedValue({ success: true, message: "" });

      const { result } = renderHook(
        () => useGitActions({ agentName: "falcon" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.pull();
      });

      expect(mockShowToast).toHaveBeenCalledWith("Pull successful", {
        type: "success",
      });
    });
  });

  describe("createPR", () => {
    it("shows success toast with URL when PR is created", async () => {
      mockGitCreatePR.mockResolvedValue({
        url: "https://github.com/repo/pull/42",
        created: true,
        already_exists: false,
        no_commits: false,
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.createPR("main");
      });

      expect(mockGitCreatePR).toHaveBeenCalledWith(
        "test-ws-id",
        "nova",
        "main",
      );
      expect(mockShowToast).toHaveBeenCalledWith(
        "PR created: https://github.com/repo/pull/42",
        { type: "success" },
      );
    });

    it("shows success toast without URL when PR is created but no URL", async () => {
      mockGitCreatePR.mockResolvedValue({
        created: true,
        already_exists: false,
        no_commits: false,
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.createPR();
      });

      expect(mockShowToast).toHaveBeenCalledWith("PR created", {
        type: "success",
      });
    });

    it("shows info toast when PR already exists with URL", async () => {
      mockGitCreatePR.mockResolvedValue({
        url: "https://github.com/repo/pull/10",
        created: false,
        already_exists: true,
        no_commits: false,
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.createPR();
      });

      expect(mockShowToast).toHaveBeenCalledWith(
        "PR already exists: https://github.com/repo/pull/10",
        { type: "info" },
      );
    });

    it("shows info toast when PR already exists without URL", async () => {
      mockGitCreatePR.mockResolvedValue({
        created: false,
        already_exists: true,
        no_commits: false,
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.createPR();
      });

      expect(mockShowToast).toHaveBeenCalledWith("PR already exists", {
        type: "info",
      });
    });

    it("shows info toast when no commits to create PR for", async () => {
      mockGitCreatePR.mockResolvedValue({
        created: false,
        already_exists: false,
        no_commits: true,
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.createPR();
      });

      expect(mockShowToast).toHaveBeenCalledWith(
        "No commits to create PR for",
        { type: "info" },
      );
    });
  });

  describe("reset", () => {
    it("shows success toast on successful reset", async () => {
      mockGitReset.mockResolvedValue({
        success: true,
        message: "Reset to main",
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.reset("main", true);
      });

      expect(mockGitReset).toHaveBeenCalledWith(
        "test-ws-id",
        "nova",
        "main",
        true,
      );
      expect(mockShowToast).toHaveBeenCalledWith("Reset to main", {
        type: "success",
      });
    });

    it("uses default message when result.message is empty", async () => {
      mockGitReset.mockResolvedValue({ success: true, message: "" });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.reset();
      });

      expect(mockShowToast).toHaveBeenCalledWith("Reset successful", {
        type: "success",
      });
    });
  });

  describe("updateTarget", () => {
    it("shows success toast on target update", async () => {
      mockGitUpdateTarget.mockResolvedValue({
        success: true,
        branch: "develop",
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.updateTarget("develop");
      });

      expect(mockGitUpdateTarget).toHaveBeenCalledWith(
        "test-ws-id",
        "nova",
        "develop",
      );
      expect(mockShowToast).toHaveBeenCalledWith(
        "Target branch updated to develop",
        { type: "success" },
      );
    });

    it("does nothing when agentName is null", async () => {
      const { result } = renderHook(() => useGitActions({ agentName: null }), {
        wrapper,
      });

      await act(async () => {
        await result.current.updateTarget("main");
      });

      expect(mockGitUpdateTarget).not.toHaveBeenCalled();
    });
  });

  describe("error handling", () => {
    describe("409 Conflict", () => {
      it("shows warning toast with file count for conflicts", async () => {
        const error = new ApiError(409, "Conflict", {
          conflicted_files: ["src/main.go", "src/util.go"],
        });
        mockGitPull.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Merge conflicts in 2 files",
          { type: "warning" },
        );
        expect(result.current.pullState.error).toBe(
          "Merge conflicts in 2 files",
        );
      });

      it("shows warning toast with singular file for 1 conflict", async () => {
        const error = new ApiError(409, "Conflict", {
          conflicted_files: ["src/main.go"],
        });
        mockGitPull.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Merge conflicts in 1 file",
          { type: "warning" },
        );
      });

      it("shows generic conflict message when no conflicted_files", async () => {
        const error = new ApiError(409, "Conflict", {});
        mockGitPull.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Pull resulted in conflicts",
          { type: "warning" },
        );
      });
    });

    describe("423 Locked", () => {
      it("shows error toast with lock info", async () => {
        const error = new ApiError(423, "Locked", {
          error: "Agent is locked",
          lock_info: {
            agent: "falcon",
            pid: 12345,
            duration: "5m30s",
          },
        });
        mockGitReset.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.reset();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Agent locked by falcon (5m30s)",
          { type: "error" },
        );
        expect(result.current.resetState.error).toBe(
          "Agent locked by falcon (5m30s)",
        );
      });

      it("shows generic lock message when no lock_info", async () => {
        const error = new ApiError(423, "Locked", {});
        mockGitPull.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith("Agent is locked", {
          type: "error",
        });
      });
    });

    describe("503 Service Unavailable", () => {
      it("shows error toast with extracted message", async () => {
        const error = new ApiError(503, "Service Unavailable", {
          error: "gh CLI not installed",
        });
        mockGitCreatePR.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.createPR();
        });

        expect(mockShowToast).toHaveBeenCalledWith("gh CLI not installed", {
          type: "error",
        });
        expect(result.current.prState.error).toBe("gh CLI not installed");
      });
    });

    describe("generic errors", () => {
      it("shows error toast with action name and error message", async () => {
        mockGitPull.mockRejectedValue(new Error("network timeout"));

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Pull failed: network timeout",
          { type: "error" },
        );
        expect(result.current.pullState.error).toBe("network timeout");
      });

      it("handles ApiError with string body", async () => {
        const error = new ApiError(500, "Internal Server Error", "bad server");
        mockGitPull.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith("Pull failed: bad server", {
          type: "error",
        });
      });

      it("handles ApiError with no body", async () => {
        const error = new ApiError(500, "Internal Server Error");
        mockGitPull.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Pull failed: Internal Server Error",
          { type: "error" },
        );
      });

      it("handles non-Error thrown values", async () => {
        mockGitPull.mockRejectedValue("some string error");

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.pull();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Pull failed: some string error",
          { type: "error" },
        );
      });
    });
  });

  describe("anyLoading", () => {
    it("reflects loading state during pull", async () => {
      let resolvePromise: (value: unknown) => void;
      const pendingPromise = new Promise((resolve) => {
        resolvePromise = resolve;
      });
      mockGitPull.mockReturnValue(pendingPromise);

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      // Start pull but don't await it
      let pullPromise: Promise<void>;
      act(() => {
        pullPromise = result.current.pull();
      });

      // Should be loading now
      expect(result.current.pullState.isLoading).toBe(true);
      expect(result.current.anyLoading).toBe(true);

      // Resolve and await
      await act(async () => {
        resolvePromise!({ success: true, message: "Done" });
        await pullPromise!;
      });

      expect(result.current.pullState.isLoading).toBe(false);
      expect(result.current.anyLoading).toBe(false);
    });
  });
});
