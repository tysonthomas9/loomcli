/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceManagementActions.
 *
 * Mocks useWorkspaceContext, useToast, useNavigate, and the workspace API
 * functions so each test exercises the hook's orchestration logic in
 * isolation.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import type { WorkspaceData, WorkspaceSummary } from "@/api/workspace";

vi.mock("@/hooks/api", () => ({
  renameWorkspace: vi.fn().mockResolvedValue({} as WorkspaceData),
  deleteWorkspace: vi.fn().mockResolvedValue(null),
}));

const mockNavigate = vi.fn();
vi.mock("react-router-dom", () => ({
  useNavigate: () => mockNavigate,
}));

const mockShowToast = vi.fn();
vi.mock("../../ui", () => ({
  useToast: () => ({ showToast: mockShowToast }),
}));

const mockRefetch = vi.fn();
const mockSetDefault = vi.fn().mockResolvedValue(undefined);
let workspaceContextValue = {
  workspace: { workspaces: [] as WorkspaceSummary[] } as WorkspaceData,
  workspaceId: "ws-active",
  refetch: mockRefetch,
  defaultWorkspaceName: null as string | null,
  setDefaultWorkspace: mockSetDefault,
};
vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => workspaceContextValue,
}));

import { renameWorkspace, deleteWorkspace } from "@/hooks/api";
import { useWorkspaceManagementActions } from "../useWorkspaceManagementActions";

const mockedRename = vi.mocked(renameWorkspace);
const mockedDelete = vi.mocked(deleteWorkspace);

function ws(overrides: Partial<WorkspaceSummary>): WorkspaceSummary {
  return {
    id: "ws-test",
    name: "test",
    path: "/p",
    active: false,
    repo_count: 1,
    is_default: false,
    ...overrides,
  };
}

function setWorkspaces(
  workspaces: WorkspaceSummary[],
  opts: { activeId?: string; defaultName?: string | null } = {},
) {
  workspaceContextValue = {
    workspace: { workspaces } as WorkspaceData,
    workspaceId: opts.activeId ?? "ws-active",
    refetch: mockRefetch,
    defaultWorkspaceName: opts.defaultName ?? null,
    setDefaultWorkspace: mockSetDefault,
  };
}

describe("useWorkspaceManagementActions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    setWorkspaces([
      ws({ id: "ws-active", name: "active" }),
      ws({ id: "ws-other", name: "other" }),
    ]);
  });

  describe("onRename", () => {
    it("calls renameWorkspace and refetches on success", async () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());

      await act(async () => {
        await result.current.onRename("ws-other", "renamed");
      });

      expect(mockedRename).toHaveBeenCalledWith("ws-other", "renamed");
      expect(mockRefetch).toHaveBeenCalled();
    });

    it("propagates rename errors to the caller", async () => {
      mockedRename.mockRejectedValueOnce(new Error("duplicate"));
      const { result } = renderHook(() => useWorkspaceManagementActions());

      await expect(
        act(async () => {
          await result.current.onRename("ws-other", "dupe");
        }),
      ).rejects.toThrow("duplicate");
    });
  });

  describe("onDelete + confirm flow", () => {
    it("populates pendingDelete on onDelete", () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());

      act(() => {
        result.current.onDelete("ws-other", "other");
      });

      expect(result.current.pendingDelete).toEqual({
        id: "ws-other",
        name: "other",
      });
    });

    it("onCancelDelete clears pendingDelete without calling the API", () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());

      act(() => {
        result.current.onDelete("ws-other", "other");
      });
      act(() => {
        result.current.onCancelDelete();
      });

      expect(result.current.pendingDelete).toBeNull();
      expect(mockedDelete).not.toHaveBeenCalled();
    });

    it("confirm shows a toast and defers the deleteWorkspace API call by 5s", async () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());

      act(() => {
        result.current.onDelete("ws-other", "other");
      });
      act(() => {
        result.current.onConfirmDelete();
      });

      expect(mockShowToast).toHaveBeenCalledWith(
        'Workspace "other" removed',
        expect.objectContaining({
          type: "success",
          duration: 5000,
          onUndo: expect.any(Function),
        }),
      );
      expect(mockedDelete).not.toHaveBeenCalled();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000);
      });

      expect(mockedDelete).toHaveBeenCalledWith("ws-other");
      expect(mockRefetch).toHaveBeenCalled();
    });

    it("Undo cancels the deferred API call and shows a 'restored' toast", async () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());

      act(() => {
        result.current.onDelete("ws-other", "other");
      });
      act(() => {
        result.current.onConfirmDelete();
      });

      const undoCall = mockShowToast.mock.calls[0]?.[1] as
        | { onUndo?: () => void }
        | undefined;
      expect(undoCall?.onUndo).toBeDefined();

      act(() => {
        undoCall?.onUndo?.();
      });

      // Fast-forward past the 5-second window
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });

      expect(mockedDelete).not.toHaveBeenCalled();
      expect(mockShowToast).toHaveBeenCalledWith(
        'Workspace "other" restored',
        expect.objectContaining({ type: "info" }),
      );
    });

    it("deleting the active workspace navigates to the next remaining workspace", async () => {
      setWorkspaces(
        [
          ws({ id: "ws-active", name: "active" }),
          ws({ id: "ws-next", name: "next" }),
        ],
        { activeId: "ws-active" },
      );
      const { result } = renderHook(() => useWorkspaceManagementActions());

      act(() => {
        result.current.onDelete("ws-active", "active");
      });
      act(() => {
        result.current.onConfirmDelete();
      });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000);
      });

      expect(mockedDelete).toHaveBeenCalledWith("ws-active");
      expect(mockNavigate).toHaveBeenCalledWith(
        expect.stringContaining("ws-next"),
        expect.objectContaining({ flushSync: true }),
      );
    });

    it("does not navigate when deleting the only remaining workspace", async () => {
      setWorkspaces([ws({ id: "ws-active", name: "active" })], {
        activeId: "ws-active",
      });
      mockedDelete.mockRejectedValueOnce(new Error("cannot delete last"));
      const { result } = renderHook(() => useWorkspaceManagementActions());

      act(() => {
        result.current.onDelete("ws-active", "active");
      });
      act(() => {
        result.current.onConfirmDelete();
      });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000);
      });

      expect(mockedDelete).toHaveBeenCalledWith("ws-active");
      expect(mockNavigate).not.toHaveBeenCalled();
      expect(mockShowToast).toHaveBeenCalledWith(
        "cannot delete last",
        expect.objectContaining({ type: "error" }),
      );
    });
  });

  describe("default workspace", () => {
    it("onSetDefault wraps setDefaultWorkspace and surfaces errors as toasts", async () => {
      mockSetDefault.mockRejectedValueOnce(new Error("boom"));
      const { result } = renderHook(() => useWorkspaceManagementActions());

      await act(async () => {
        result.current.onSetDefault("foo");
        // Allow the rejected promise to settle
        await Promise.resolve();
      });

      expect(mockSetDefault).toHaveBeenCalledWith("foo");
      expect(mockShowToast).toHaveBeenCalledWith(
        "boom",
        expect.objectContaining({ type: "error" }),
      );
    });

    it("onClearDefault calls setDefaultWorkspace with null", async () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());

      await act(async () => {
        result.current.onClearDefault();
        await Promise.resolve();
      });

      expect(mockSetDefault).toHaveBeenCalledWith(null);
    });

    it("defaultWorkspaceId resolves from defaultWorkspaceName via the workspace list", () => {
      setWorkspaces(
        [
          ws({ id: "ws-active", name: "active" }),
          ws({ id: "ws-pin", name: "pinned" }),
        ],
        { defaultName: "pinned" },
      );

      const { result } = renderHook(() => useWorkspaceManagementActions());

      expect(result.current.defaultWorkspaceId).toBe("ws-pin");
    });

    it("defaultWorkspaceId is undefined when no default is set", () => {
      const { result } = renderHook(() => useWorkspaceManagementActions());
      expect(result.current.defaultWorkspaceId).toBeUndefined();
    });
  });
});
