/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";

import { DEFAULT_VIEW } from "@/components/ViewSwitcher";
import type { ViewMode as _ViewMode } from "@/components/ViewSwitcher";
import { RouterWrapper } from "@/test-utils/router-wrapper";

import { useViewState, isValidViewMode } from "../useViewState";

/**
 * Mock window.location for parseViewFromUrl tests (legacy helper).
 */
describe("useViewState", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns DEFAULT_VIEW when no URL param exists", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      const { view } = result.current;
      expect(view).toBe(DEFAULT_VIEW);
      expect(view).toBe("kanban");
    });
  });

  describe("setView", () => {
    it("updates state when setView is called", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current.setView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("allows changing view multiple times", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
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
    it("updates view state locally", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current.navigateToView("issue-detail", { issueId: "abc-123" });
      });

      expect(result.current.view).toBe("issue-detail");
    });

    it("navigates to a non-detail view", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      // First navigate to detail
      act(() => {
        result.current.navigateToView("issue-detail", { issueId: "issue-99" });
      });

      // Now navigate to table
      act(() => {
        result.current.navigateToView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("navigating to DEFAULT_VIEW clears view from state", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current.navigateToView("table");
      });
      expect(result.current.view).toBe("table");

      act(() => {
        result.current.navigateToView("kanban");
      });
      expect(result.current.view).toBe("kanban");
    });
  });

  describe("setter reference stability", () => {
    it("setView function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      const setView1 = result.current.setView;

      rerender();

      const setView2 = result.current.setView;

      expect(setView1).toBe(setView2);
    });

    it("setView remains callable when view changes", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current.setView("table");
      });
      expect(result.current.view).toBe("table");

      // Setter still works after state change
      act(() => {
        result.current.setView("graph");
      });
      expect(result.current.view).toBe("graph");
    });

    it("navigateToView reference is stable across re-renders", () => {
      const { result, rerender } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
      });

      const nav1 = result.current.navigateToView;

      rerender();

      const nav2 = result.current.navigateToView;

      expect(nav1).toBe(nav2);
    });
  });

  describe("edge cases", () => {
    it("handles setting the same view multiple times", () => {
      const { result } = renderHook(() => useViewState(), {
        wrapper: RouterWrapper,
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
        wrapper: RouterWrapper,
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
