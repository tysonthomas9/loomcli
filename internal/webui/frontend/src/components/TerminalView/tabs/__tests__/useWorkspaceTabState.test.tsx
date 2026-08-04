/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceTabState hook.
 * Validates that tab state is keyed by stable workspace UUID (not name),
 * so workspace renames do NOT clear tabs.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type React from "react";

import { useWorkspaceTabState } from "../useWorkspaceTabState";
import type { TabState } from "../terminalTabUtils";

// ---------- mocks ----------

let mockContextValue = {
  activeWorkspaceName: "my-workspace" as string | null,
  workspace: { id: "uuid-A" } as { id: string } | null,
};

vi.mock("@/hooks", () => ({
  useWorkspaceContext: () => mockContextValue,
}));

// ---------- helpers ----------

function createArgs(
  overrides: Partial<Parameters<typeof useWorkspaceTabState>[0]> = {},
) {
  return {
    tabs: [] as TabState[],
    activeTabId: "",
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    initializedRef: { current: false } as React.MutableRefObject<boolean>,
    ...overrides,
  };
}

function makeTab(id: string): TabState {
  return {
    id,
    label: `Tab ${id}`,
    sessionName: `session-${id}`,
    connectionState: "disconnected",
    backendName: "claude",
  };
}

// ---------- tests ----------

describe("useWorkspaceTabState", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContextValue = {
      activeWorkspaceName: "my-workspace",
      workspace: { id: "uuid-A" },
    };
  });

  describe("return values", () => {
    it("returns name and id from workspace context", () => {
      mockContextValue = {
        activeWorkspaceName: "production",
        workspace: { id: "uuid-prod" },
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.name).toBe("production");
      expect(result.current.id).toBe("uuid-prod");
    });

    it("returns 'default' for name when activeWorkspaceName is null", () => {
      mockContextValue = {
        activeWorkspaceName: null,
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.name).toBe("default");
      expect(result.current.id).toBe("uuid-A");
    });

    it("returns empty string for id when workspace is null", () => {
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspace: null,
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.id).toBe("");
    });
  });

  describe("workspace rename does NOT clear tabs", () => {
    it("does not call setTabs or setActiveTabId when name changes but UUID stays the same", () => {
      mockContextValue = {
        activeWorkspaceName: "old-name",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs({
        tabs: [makeTab("1")],
        activeTabId: "1",
      });

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Rename workspace — UUID unchanged
      mockContextValue = {
        activeWorkspaceName: "new-name",
        workspace: { id: "uuid-A" },
      };
      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();

      rerender();

      expect(args.setTabs).not.toHaveBeenCalled();
      expect(args.setActiveTabId).not.toHaveBeenCalled();
    });
  });

  describe("workspace switch saves and restores", () => {
    it("saves tabs for old workspace and clears for new workspace", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspace: { id: "uuid-A" },
      };
      const tabA = makeTab("a1");
      const args = createArgs({
        tabs: [tabA],
        activeTabId: "a1",
      });

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Switch to workspace B (never visited)
      mockContextValue = {
        activeWorkspaceName: "ws-B",
        workspace: { id: "uuid-B" },
      };

      rerender();

      // Should clear tabs for the new workspace (no saved state)
      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
      expect(args.initializedRef.current).toBe(false);
    });

    it("restores previously saved tabs when switching back", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspace: { id: "uuid-A" },
      };
      const tabA = makeTab("a1");
      const args = createArgs({
        tabs: [tabA],
        activeTabId: "a1",
      });

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Switch to workspace B
      mockContextValue = {
        activeWorkspaceName: "ws-B",
        workspace: { id: "uuid-B" },
      };
      rerender();

      // Now simulate workspace B having its own tabs
      const tabB = makeTab("b1");
      args.tabs = [tabB];
      args.activeTabId = "b1";
      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();

      // Switch back to workspace A
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspace: { id: "uuid-A" },
      };
      rerender();

      // Should restore workspace A's saved tabs
      expect(args.setTabs).toHaveBeenCalledWith([tabA]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("a1");
    });
  });

  describe("missing workspace data uses __unresolved__", () => {
    it("starts with __unresolved__ when wsData is null, then transitions when resolved", () => {
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspace: null,
      };
      const args = createArgs();

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Initially at __unresolved__, no setTabs called (cacheKey unchanged from initial ref)
      expect(args.setTabs).not.toHaveBeenCalled();

      // Now workspace data arrives
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspace: { id: "uuid-A" },
      };

      rerender();

      // Transition from __unresolved__ to uuid-A should fire the effect
      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
      expect(args.initializedRef.current).toBe(false);
    });

    it("does not save tabs under __unresolved__ when UUID resolves", () => {
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspace: null,
      };
      const args = createArgs({
        tabs: [makeTab("1")],
        activeTabId: "1",
      });

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // UUID resolves
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspace: { id: "uuid-A" },
      };
      rerender();

      // Switch to workspace B — if __unresolved__ was saved, switching back
      // to __unresolved__ would restore stale tabs
      mockContextValue = {
        activeWorkspaceName: "other",
        workspace: null,
      };
      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();
      rerender();

      // Should clear tabs (no saved state under __unresolved__)
      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
    });
  });

  describe("no-op when cacheKey unchanged", () => {
    it("does not fire effect on re-render with same UUID", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs({
        tabs: [makeTab("1")],
        activeTabId: "1",
      });

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();

      // Re-render with identical context
      rerender();
      rerender();
      rerender();

      expect(args.setTabs).not.toHaveBeenCalled();
      expect(args.setActiveTabId).not.toHaveBeenCalled();
    });
  });
});
