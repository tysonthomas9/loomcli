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

import { ApiError } from "@/api/client";
import { ToastProvider } from "@/hooks/useToast";

import { useGitActions } from "@/hooks/useGitActions";

// Mock git API functions
const mockGitPush = vi.fn();
const mockGitPull = vi.fn();
const mockGitSync = vi.fn();
const mockGitCreatePR = vi.fn();
const mockGitReset = vi.fn();
const mockGitUpdateTarget = vi.fn();

vi.mock("@/api/git", () => ({
  gitPush: (...args: unknown[]) => mockGitPush(...args),
  gitPull: (...args: unknown[]) => mockGitPull(...args),
  gitSync: (...args: unknown[]) => mockGitSync(...args),
  gitCreatePR: (...args: unknown[]) => mockGitCreatePR(...args),
  gitReset: (...args: unknown[]) => mockGitReset(...args),
  gitUpdateTarget: (...args: unknown[]) => mockGitUpdateTarget(...args),
}));

// Mock useToast to track showToast calls
const mockShowToast = vi.fn();

vi.mock("@/hooks/useToast", async (importOriginal) => {
  const actual = (await importOriginal()) as typeof import("@/hooks/useToast");
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

      expect(result.current.pushState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(result.current.pullState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(result.current.syncState).toEqual({
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

  describe("push", () => {
    it("shows success toast on successful push", async () => {
      mockGitPush.mockResolvedValue({
        success: true,
        message: "Pushed 3 commits",
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.push("main");
      });

      expect(mockGitPush).toHaveBeenCalledWith("nova", "main");
      expect(result.current.pushState).toEqual({
        isLoading: false,
        error: null,
      });
      expect(mockShowToast).toHaveBeenCalledWith("Pushed 3 commits", {
        type: "success",
      });
    });

    it("uses default message when result.message is empty", async () => {
      mockGitPush.mockResolvedValue({ success: true, message: "" });

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.push();
      });

      expect(mockShowToast).toHaveBeenCalledWith("Push successful", {
        type: "success",
      });
    });

    it("does nothing when agentName is null", async () => {
      const { result } = renderHook(() => useGitActions({ agentName: null }), {
        wrapper,
      });

      await act(async () => {
        await result.current.push();
      });

      expect(mockGitPush).not.toHaveBeenCalled();
    });

    it("calls onStatusChange after successful push", async () => {
      mockGitPush.mockResolvedValue({ success: true, message: "Done" });
      const onStatusChange = vi.fn();

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova", onStatusChange }),
        { wrapper },
      );

      await act(async () => {
        await result.current.push();
      });

      expect(onStatusChange).toHaveBeenCalledTimes(1);
    });

    it("calls onStatusChange after push error", async () => {
      mockGitPush.mockRejectedValue(new Error("network error"));
      const onStatusChange = vi.fn();

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova", onStatusChange }),
        { wrapper },
      );

      await act(async () => {
        await result.current.push();
      });

      expect(onStatusChange).toHaveBeenCalledTimes(1);
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

      expect(mockGitPull).toHaveBeenCalledWith("falcon", "origin/main");
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

  describe("sync", () => {
    it("shows success toast on successful sync", async () => {
      mockGitSync.mockResolvedValue({
        push_result: { success: true },
        pull_result: { success: true },
      });

      const { result } = renderHook(
        () => useGitActions({ agentName: "ember" }),
        { wrapper },
      );

      await act(async () => {
        await result.current.sync();
      });

      expect(mockGitSync).toHaveBeenCalledWith("ember");
      expect(mockShowToast).toHaveBeenCalledWith("Sync successful", {
        type: "success",
      });
    });

    it("calls onStatusChange after sync", async () => {
      mockGitSync.mockResolvedValue({});
      const onStatusChange = vi.fn();

      const { result } = renderHook(
        () => useGitActions({ agentName: "ember", onStatusChange }),
        { wrapper },
      );

      await act(async () => {
        await result.current.sync();
      });

      expect(onStatusChange).toHaveBeenCalledTimes(1);
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

      expect(mockGitCreatePR).toHaveBeenCalledWith("nova", "main");
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

      expect(mockGitReset).toHaveBeenCalledWith("nova", "main", true);
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

      expect(mockGitUpdateTarget).toHaveBeenCalledWith("nova", "develop");
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
        mockGitPush.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.push();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Merge conflicts in 2 files",
          { type: "warning" },
        );
        expect(result.current.pushState.error).toBe(
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
        mockGitSync.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.sync();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Sync resulted in conflicts",
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
        mockGitPush.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.push();
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
        mockGitPush.mockRejectedValue(new Error("network timeout"));

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.push();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Push failed: network timeout",
          { type: "error" },
        );
        expect(result.current.pushState.error).toBe("network timeout");
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
        mockGitSync.mockRejectedValue(error);

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.sync();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Sync failed: Internal Server Error",
          { type: "error" },
        );
      });

      it("handles non-Error thrown values", async () => {
        mockGitPush.mockRejectedValue("some string error");

        const { result } = renderHook(
          () => useGitActions({ agentName: "nova" }),
          { wrapper },
        );

        await act(async () => {
          await result.current.push();
        });

        expect(mockShowToast).toHaveBeenCalledWith(
          "Push failed: some string error",
          { type: "error" },
        );
      });
    });
  });

  describe("anyLoading", () => {
    it("reflects loading state during push", async () => {
      let resolvePromise: (value: unknown) => void;
      const pendingPromise = new Promise((resolve) => {
        resolvePromise = resolve;
      });
      mockGitPush.mockReturnValue(pendingPromise);

      const { result } = renderHook(
        () => useGitActions({ agentName: "nova" }),
        { wrapper },
      );

      // Start push but don't await it
      let pushPromise: Promise<void>;
      act(() => {
        pushPromise = result.current.push();
      });

      // Should be loading now
      expect(result.current.pushState.isLoading).toBe(true);
      expect(result.current.anyLoading).toBe(true);

      // Resolve and await
      await act(async () => {
        resolvePromise!({ success: true, message: "Done" });
        await pushPromise!;
      });

      expect(result.current.pushState.isLoading).toBe(false);
      expect(result.current.anyLoading).toBe(false);
    });
  });
});
