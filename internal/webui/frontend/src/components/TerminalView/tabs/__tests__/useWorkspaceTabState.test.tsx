/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceTabState hook.
 * Validates that tab state is keyed by the route-authoritative workspace id
 * (not the name, so renames do NOT clear tabs, and not the polled store's
 * lagging workspace.id), and that a workspace change resets tab state.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type React from "react";

import { useWorkspaceTabState } from "../useWorkspaceTabState";
import type { TabState } from "../terminalTabUtils";

// ---------- mocks ----------

interface MockContext {
  activeWorkspaceName: string | null;
  /** Route-authoritative id, as WorkspaceLayout passes it to WorkspaceProvider. */
  workspaceId: string;
  /** Polled-store workspace record; deliberately stale while refetching. */
  workspace: { id: string } | null;
}

let mockContextValue: MockContext = {
  activeWorkspaceName: "my-workspace",
  workspaceId: "uuid-A",
  workspace: { id: "uuid-A" },
};

vi.mock("@/hooks", () => ({
  useWorkspaceContext: () => mockContextValue,
}));

// ---------- helpers ----------

function createArgs(
  overrides: Partial<Parameters<typeof useWorkspaceTabState>[0]> = {},
) {
  return {
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    initializedRef: { current: false } as React.MutableRefObject<boolean>,
    ...overrides,
  };
}

// ---------- tests ----------

describe("useWorkspaceTabState", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockContextValue = {
      activeWorkspaceName: "my-workspace",
      workspaceId: "uuid-A",
      workspace: { id: "uuid-A" },
    };
  });

  describe("return values", () => {
    it("returns name and id from workspace context", () => {
      mockContextValue = {
        activeWorkspaceName: "production",
        workspaceId: "uuid-prod",
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
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.name).toBe("default");
      expect(result.current.id).toBe("uuid-A");
    });

    it("returns empty string for id when neither source has resolved", () => {
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspaceId: "",
        workspace: null,
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.id).toBe("");
    });
  });

  describe("authoritative workspace id", () => {
    it("prefers the context workspaceId over the lagging store workspace.id", () => {
      // The polled store deliberately serves the previous workspace while
      // refetching, so on a switch the two disagree for a few renders.
      mockContextValue = {
        activeWorkspaceName: "ws-B",
        workspaceId: "uuid-B",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.id).toBe("uuid-B");
    });

    it("falls back to the store workspace.id when the context id is empty", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspaceId: "",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { result } = renderHook(() => useWorkspaceTabState(args));

      expect(result.current.id).toBe("uuid-A");
    });

    it("resets tab state when the context id changes even if the store lags", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Route commits to B; the store has not caught up yet.
      mockContextValue = {
        activeWorkspaceName: "ws-B",
        workspaceId: "uuid-B",
        workspace: { id: "uuid-A" },
      };
      rerender();

      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
      expect(args.initializedRef.current).toBe(false);
    });
  });

  describe("workspace rename does NOT clear tabs", () => {
    it("does not call setTabs or setActiveTabId when name changes but id stays the same", () => {
      mockContextValue = {
        activeWorkspaceName: "old-name",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Rename workspace — id unchanged
      mockContextValue = {
        activeWorkspaceName: "new-name",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();

      rerender();

      expect(args.setTabs).not.toHaveBeenCalled();
      expect(args.setActiveTabId).not.toHaveBeenCalled();
    });
  });

  describe("workspace switch resets tab state", () => {
    it("clears tabs, clears the active id and resets initializedRef", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs({
        initializedRef: { current: true } as React.MutableRefObject<boolean>,
      });

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      mockContextValue = {
        activeWorkspaceName: "ws-B",
        workspaceId: "uuid-B",
        workspace: { id: "uuid-B" },
      };
      rerender();

      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
      expect(args.initializedRef.current).toBe(false);
    });

    it("clears again on switching back — no in-memory restore", () => {
      // Tab sets are NOT cached per workspace: server-side metadata is the
      // persistence layer, and TerminalView remounts on every switch anyway.
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      mockContextValue = {
        activeWorkspaceName: "ws-B",
        workspaceId: "uuid-B",
        workspace: { id: "uuid-B" },
      };
      rerender();

      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();

      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      rerender();

      expect(args.setTabs).toHaveBeenCalledTimes(1);
      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
    });
  });

  describe("missing workspace data uses __unresolved__", () => {
    it("starts with __unresolved__ when no id has resolved, then transitions", () => {
      mockContextValue = {
        activeWorkspaceName: "ws",
        workspaceId: "",
        workspace: null,
      };
      const args = createArgs();

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      // Initially at __unresolved__: cacheKey unchanged from the initial ref.
      expect(args.setTabs).not.toHaveBeenCalled();

      mockContextValue = {
        activeWorkspaceName: "ws",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      rerender();

      expect(args.setTabs).toHaveBeenCalledWith([]);
      expect(args.setActiveTabId).toHaveBeenCalledWith("");
      expect(args.initializedRef.current).toBe(false);
    });
  });

  describe("no-op when the id is unchanged", () => {
    it("does not fire the reset effect on re-render with the same id", () => {
      mockContextValue = {
        activeWorkspaceName: "ws-A",
        workspaceId: "uuid-A",
        workspace: { id: "uuid-A" },
      };
      const args = createArgs();

      const { rerender } = renderHook(() => useWorkspaceTabState(args));

      args.setTabs.mockClear();
      args.setActiveTabId.mockClear();

      rerender();
      rerender();
      rerender();

      expect(args.setTabs).not.toHaveBeenCalled();
      expect(args.setActiveTabId).not.toHaveBeenCalled();
    });
  });
});
