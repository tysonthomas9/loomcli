/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { createElement, type ReactNode } from "react";

import { DEFAULT_VIEW } from "@/components/ViewSwitcher";

import { useViewState, isValidViewMode } from "../useViewState";

/**
 * Create a RouterProvider wrapper with routes matching the app structure.
 * Initializes at the given path (default: /ws/test-ws/kanban).
 */
function createRouterWrapper(initialPath = "/ws/test-ws/kanban") {
  return function Wrapper({ children }: { children: ReactNode }) {
    const router = createMemoryRouter(
      [
        {
          path: "/ws/:workspaceId",
          children: [
            { index: true, element: children },
            { path: "kanban", element: children },
            { path: "table", element: children },
            { path: "graph", element: children },
            { path: "monitor", element: children },
            { path: "observability", element: children },
            { path: "terminal", element: children },
            { path: "workspace", element: children },
            { path: "settings", element: children },
            { path: "files", element: children },
            { path: "issues/:issueId", element: children },
          ],
        },
      ],
      { initialEntries: [initialPath] },
    );

    return createElement(RouterProvider, { router });
  };
}

describe("useViewState", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns DEFAULT_VIEW when at the workspace root", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      const { view } = result.current;
      expect(view).toBe(DEFAULT_VIEW);
      expect(view).toBe("kanban");
    });

    it("returns table when at /ws/:id/table", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/table"),
      });

      expect(result.current.view).toBe("table");
    });

    it("returns terminal when at /ws/:id/terminal", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/terminal"),
      });

      expect(result.current.view).toBe("terminal");
    });
  });

  describe("setView", () => {
    it("updates view via route navigation (replace semantics)", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.setView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("allows changing view multiple times", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.setView("table");
      });
      expect(result.current.view).toBe("table");

      act(() => {
        result.current.setView("graph");
      });
      expect(result.current.view).toBe("graph");

      act(() => {
        result.current.setView("kanban");
      });
      expect(result.current.view).toBe("kanban");
    });
  });

  describe("navigateToView", () => {
    it("updates view via route navigation (push semantics)", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.navigateToView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("navigating to DEFAULT_VIEW shows kanban", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/table"),
      });

      expect(result.current.view).toBe("table");

      act(() => {
        result.current.navigateToView("kanban");
      });

      expect(result.current.view).toBe("kanban");
    });
  });

  describe("edge cases", () => {
    it("handles setting the same view multiple times", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.setView("table");
      });
      expect(result.current.view).toBe("table");

      act(() => {
        result.current.setView("table");
      });
      expect(result.current.view).toBe("table");
    });

    it("handles rapid view changes", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.setView("table");
        result.current.setView("graph");
        result.current.setView("kanban");
      });

      expect(result.current.view).toBe("kanban");
    });
  });
});

describe("isValidViewMode", () => {
  it("returns true for kanban", () => {
    expect(isValidViewMode("kanban")).toBe(true);
  });

  it("returns true for table", () => {
    expect(isValidViewMode("table")).toBe(true);
  });

  it("returns true for graph", () => {
    expect(isValidViewMode("graph")).toBe(true);
  });

  it("returns true for monitor", () => {
    expect(isValidViewMode("monitor")).toBe(true);
  });

  it("returns true for settings", () => {
    expect(isValidViewMode("settings")).toBe(true);
  });

  it("returns true for files", () => {
    expect(isValidViewMode("files")).toBe(true);
  });

  it("returns true for issue-detail", () => {
    expect(isValidViewMode("issue-detail")).toBe(true);
  });

  it("returns false for invalid string", () => {
    expect(isValidViewMode("invalid")).toBe(false);
  });

  it("returns false for empty string", () => {
    expect(isValidViewMode("")).toBe(false);
  });

  it("returns false for null", () => {
    expect(isValidViewMode(null)).toBe(false);
  });

  it("returns false for uppercase valid view", () => {
    expect(isValidViewMode("KANBAN")).toBe(false);
  });

  it("returns false for similar but invalid strings", () => {
    expect(isValidViewMode("kanban ")).toBe(false);
    expect(isValidViewMode(" table")).toBe(false);
    expect(isValidViewMode("graphs")).toBe(false);
  });
});
