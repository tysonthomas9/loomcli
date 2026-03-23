/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  useWorkspaceState,
  type WorkspaceUIState,
  type WorkspaceStateCallbacks,
} from "../useWorkspaceState";

// Mock ViewSwitcher module for ViewMode type
vi.mock("@/components/ViewSwitcher", () => ({
  DEFAULT_VIEW: "kanban",
}));

describe("useWorkspaceState", () => {
  let stateRef: { current: WorkspaceUIState | null };
  let callbacks: WorkspaceStateCallbacks;

  beforeEach(() => {
    stateRef = {
      current: {
        view: "kanban",
        filters: {},
        searchValue: "",
        selectedIssueId: null,
      },
    };

    callbacks = {
      setView: vi.fn(),
      setFilters: vi.fn(),
      clearAll: vi.fn(),
      setSearchValue: vi.fn(),
      setSelectedIssueId: vi.fn(),
    };

    // Reset URL
    window.history.replaceState(null, "", "/");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initialization", () => {
    it("returns expected shape", () => {
      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      expect(result.current).toHaveProperty("currentWorkspaceId");
      expect(result.current).toHaveProperty("switchWorkspace");
      expect(result.current).toHaveProperty("captureSnapshot");
    });

    it("initializes workspace ID from URL", () => {
      window.history.replaceState(null, "", "/?workspace=test-ws");

      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      expect(result.current.currentWorkspaceId).toBe("test-ws");
    });

    it("initializes to null when no workspace in URL", () => {
      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      expect(result.current.currentWorkspaceId).toBeNull();
    });
  });

  describe("captureSnapshot", () => {
    it("captures current state from stateRef", () => {
      stateRef.current = {
        view: "table",
        filters: { status: "open" },
        searchValue: "test query",
        selectedIssueId: "issue-1",
      };

      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      const snapshot = result.current.captureSnapshot();

      expect(snapshot.view).toBe("table");
      expect(snapshot.filters).toEqual({ status: "open" });
      expect(snapshot.searchValue).toBe("test query");
      expect(snapshot.selectedIssueId).toBe("issue-1");
      expect(snapshot.scrollTop).toBe(0);
    });

    it("returns default snapshot when stateRef.current is null (pre-mount race)", () => {
      stateRef.current = null;

      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      const snapshot = result.current.captureSnapshot();

      expect(snapshot.view).toBe("kanban");
      expect(snapshot.filters).toEqual({});
      expect(snapshot.searchValue).toBe("");
      expect(snapshot.selectedIssueId).toBeNull();
      expect(snapshot.scrollTop).toBe(0);
    });
  });

  describe("switchWorkspace", () => {
    it("updates currentWorkspaceId", () => {
      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      act(() => {
        result.current.switchWorkspace("ws-2");
      });

      expect(result.current.currentWorkspaceId).toBe("ws-2");
    });

    it("applies defaults for first visit to workspace", () => {
      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      act(() => {
        result.current.switchWorkspace("ws-new");
      });

      expect(callbacks.setView).toHaveBeenCalledWith("kanban");
      expect(callbacks.clearAll).toHaveBeenCalled();
      expect(callbacks.setSearchValue).toHaveBeenCalledWith("");
    });

    it("restores saved snapshot when returning to workspace", () => {
      stateRef.current = {
        view: "table",
        filters: { priority: 1 },
        searchValue: "search",
        selectedIssueId: "issue-5",
      };

      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      // Switch away (saves current state for null workspace)
      // First set a workspace via URL so prevId is non-null
      window.history.replaceState(null, "", "/?workspace=ws-1");

      // Re-render to pick up URL
      const { result: result2 } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      // Switch from ws-1 to ws-2 (captures ws-1 state)
      act(() => {
        result2.current.switchWorkspace("ws-2");
      });

      // Switch back to ws-1 (should restore snapshot)
      act(() => {
        result2.current.switchWorkspace("ws-1");
      });

      expect(callbacks.setView).toHaveBeenCalledWith("table");
      expect(callbacks.setFilters).toHaveBeenCalledWith({ priority: 1 });
      expect(callbacks.setSearchValue).toHaveBeenCalledWith("search");
      expect(callbacks.setSelectedIssueId).toHaveBeenCalledWith("issue-5");
    });
  });

  describe("pre-mount stateRef race", () => {
    it("switchWorkspace does not crash when stateRef.current is null", () => {
      window.history.replaceState(null, "", "/?workspace=ws-A");
      stateRef.current = null;

      const { result } = renderHook(() =>
        useWorkspaceState({ stateRef, callbacks }),
      );

      // Should not throw — captureSnapshot returns default when stateRef is null
      expect(() => {
        act(() => {
          result.current.switchWorkspace("ws-B");
        });
      }).not.toThrow();

      expect(result.current.currentWorkspaceId).toBe("ws-B");
      // Defaults should be applied for ws-B
      expect(callbacks.setView).toHaveBeenCalledWith("kanban");
      expect(callbacks.clearAll).toHaveBeenCalled();
      expect(callbacks.setSearchValue).toHaveBeenCalledWith("");
    });
  });
});
