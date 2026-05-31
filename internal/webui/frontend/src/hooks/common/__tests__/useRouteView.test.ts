/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { createElement, type ReactNode } from "react";

import { DEFAULT_VIEW } from "@/types";

import { useRouteView } from "../useRouteView";

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
            { path: "workflows", element: children },
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

describe("useRouteView", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial view derivation", () => {
    it("returns kanban for /ws/:id/kanban", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      expect(result.current.view).toBe("kanban");
    });

    it("returns table for /ws/:id/table", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/table"),
      });

      expect(result.current.view).toBe("table");
    });

    it("returns graph for /ws/:id/graph", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/graph"),
      });

      expect(result.current.view).toBe("graph");
    });

    it("returns monitor for /ws/:id/monitor", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/monitor"),
      });

      expect(result.current.view).toBe("monitor");
    });

    it("returns workflows for /ws/:id/workflows", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/workflows"),
      });

      expect(result.current.view).toBe("workflows");
    });

    it("returns observability for /ws/:id/observability", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/observability"),
      });

      expect(result.current.view).toBe("observability");
    });

    it("returns terminal for /ws/:id/terminal", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/terminal"),
      });

      expect(result.current.view).toBe("terminal");
    });

    it("returns workspace for /ws/:id/workspace", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/workspace"),
      });

      expect(result.current.view).toBe("workspace");
    });

    it("returns settings for /ws/:id/settings", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/settings"),
      });

      expect(result.current.view).toBe("settings");
    });

    it("returns files for /ws/:id/files", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/files"),
      });

      expect(result.current.view).toBe("files");
    });
  });

  describe("issue-detail derivation", () => {
    it("returns issue-detail for /ws/:id/issues/:issueId", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/issues/T-5"),
      });

      expect(result.current.view).toBe("issue-detail");
    });

    it("returns issue-detail for any issueId value", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/issues/PROJ-123"),
      });

      expect(result.current.view).toBe("issue-detail");
    });
  });

  describe("default view for unknown/bare paths", () => {
    it("returns DEFAULT_VIEW for bare workspace path /ws/:id", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws"),
      });

      expect(result.current.view).toBe(DEFAULT_VIEW);
      expect(result.current.view).toBe("kanban");
    });
  });

  describe("setView (replace semantics)", () => {
    it("changes the view", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.setView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("allows changing view multiple times", () => {
      const { result } = renderHook(() => useRouteView(), {
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

    it("handles setting the same view repeatedly", () => {
      const { result } = renderHook(() => useRouteView(), {
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
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.setView("table");
        result.current.setView("graph");
        result.current.setView("terminal");
      });

      expect(result.current.view).toBe("terminal");
    });
  });

  describe("navigateToView (push semantics)", () => {
    it("changes the view", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.navigateToView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("navigating to DEFAULT_VIEW shows kanban", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/table"),
      });

      expect(result.current.view).toBe("table");

      act(() => {
        result.current.navigateToView("kanban");
      });

      expect(result.current.view).toBe("kanban");
    });

    it("navigating through multiple views works", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban"),
      });

      act(() => {
        result.current.navigateToView("graph");
      });
      expect(result.current.view).toBe("graph");

      act(() => {
        result.current.navigateToView("settings");
      });
      expect(result.current.view).toBe("settings");
    });
  });

  describe("search params preservation", () => {
    it("preserves search params when using setView", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper(
          "/ws/test-ws/kanban?repo=my-repo&filter=open",
        ),
      });

      expect(result.current.view).toBe("kanban");

      act(() => {
        result.current.setView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("preserves search params when using navigateToView", () => {
      const { result } = renderHook(() => useRouteView(), {
        wrapper: createRouterWrapper("/ws/test-ws/kanban?repo=my-repo"),
      });

      expect(result.current.view).toBe("kanban");

      act(() => {
        result.current.navigateToView("graph");
      });

      expect(result.current.view).toBe("graph");
    });
  });
});
