/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { DEFAULT_VIEW } from "@/components/ViewSwitcher";
import type { ViewMode as _ViewMode } from "@/components/ViewSwitcher";

import {
  useViewState,
  parseViewFromUrl,
  isValidViewMode,
} from "../useViewState";

/**
 * Mock window.location for URL sync tests.
 */
function mockWindowLocation(search = ""): void {
  Object.defineProperty(window, "location", {
    value: {
      pathname: "/app",
      search,
      href: `http://localhost:3000/app${search}`,
    },
    writable: true,
    configurable: true,
  });
}

/**
 * Mock window.history for URL sync tests.
 */
function mockWindowHistory(): {
  replaceState: ReturnType<typeof vi.fn>;
  pushState: ReturnType<typeof vi.fn>;
} {
  const replaceState = vi.fn();
  const pushState = vi.fn();
  Object.defineProperty(window, "history", {
    value: {
      replaceState,
      pushState,
    },
    writable: true,
    configurable: true,
  });
  return { replaceState, pushState };
}

describe("useViewState", () => {
  beforeEach(() => {
    mockWindowLocation();
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns DEFAULT_VIEW when no URL param exists", () => {
      mockWindowLocation("");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe(DEFAULT_VIEW);
      expect(view).toBe("kanban");
    });

    it("returns DEFAULT_VIEW when syncUrl is false", () => {
      mockWindowLocation("?view=table");
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      const { view } = result.current;
      expect(view).toBe(DEFAULT_VIEW);
    });
  });

  describe("URL parsing", () => {
    it('parses valid view from URL (?view=table returns "table")', () => {
      mockWindowLocation("?view=table");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("table");
    });

    it("parses kanban view from URL", () => {
      mockWindowLocation("?view=kanban");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("kanban");
    });

    it("parses graph view from URL", () => {
      mockWindowLocation("?view=graph");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("graph");
    });

    it("parses monitor view from URL", () => {
      mockWindowLocation("?view=monitor");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("monitor");
    });

    it("parses settings view from URL", () => {
      mockWindowLocation("?view=settings");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("settings");
    });

    it("parses files view from URL", () => {
      mockWindowLocation("?view=files");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("files");
    });

    it("parses issue-detail view from URL", () => {
      mockWindowLocation("?view=issue-detail");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("issue-detail");
    });

    it('defaults to kanban for invalid view (?view=invalid returns "kanban")', () => {
      mockWindowLocation("?view=invalid");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("kanban");
    });

    it('defaults to kanban for empty view param (?view= returns "kanban")', () => {
      mockWindowLocation("?view=");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("kanban");
    });

    it("handles case-sensitive view values (uppercase returns default)", () => {
      mockWindowLocation("?view=TABLE");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("kanban");
    });

    it("ignores other URL params and parses view correctly", () => {
      mockWindowLocation("?priority=2&view=graph&type=bug");
      const { result } = renderHook(() => useViewState());

      const { view } = result.current;
      expect(view).toBe("graph");
    });
  });

  describe("setView", () => {
    let historyMock: {
      replaceState: ReturnType<typeof vi.fn>;
      pushState: ReturnType<typeof vi.fn>;
    };

    beforeEach(() => {
      mockWindowLocation("");
      historyMock = mockWindowHistory();
    });

    it("updates state when setView is called", () => {
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current.setView("table");
      });

      expect(result.current.view).toBe("table");
    });

    it("calls pushState when view changes (creates history entry)", () => {
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current.setView("graph");
      });

      expect(historyMock.pushState).toHaveBeenCalledWith(
        null,
        "",
        "/app?view=graph",
      );
    });

    it("removes view param from URL when setting to DEFAULT_VIEW", () => {
      mockWindowLocation("?view=table");
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current.setView("kanban");
      });

      // Last call should be to pathname only (no query string)
      const lastCall = historyMock.pushState.mock.calls.at(-1);
      expect(lastCall?.[2]).toBe("/app");
    });

    it("does not call pushState or replaceState when syncUrl is false", () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      act(() => {
        result.current.setView("table");
      });

      // State should update
      expect(result.current.view).toBe("table");

      // Neither replaceState nor pushState should be called for view changes
      const replaceCalls = historyMock.replaceState.mock.calls;
      const pushCalls = historyMock.pushState.mock.calls;
      const viewReplaceCall = replaceCalls.find((call) => call[2]?.includes("view=table"));
      const viewPushCall = pushCalls.find((call) => call[2]?.includes("view=table"));
      expect(viewReplaceCall).toBeUndefined();
      expect(viewPushCall).toBeUndefined();
    });

    it("allows changing view multiple times", () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

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

    it("clears urlIssueId when setView switches away from issue-detail", () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      // First navigate to issue-detail with an issue
      act(() => {
        result.current.navigateToView("issue-detail", { issueId: "issue-42" });
      });
      expect(result.current.urlIssueId).toBe("issue-42");

      // setView to kanban should clear urlIssueId
      act(() => {
        result.current.setView("kanban");
      });
      expect(result.current.view).toBe("kanban");
      expect(result.current.urlIssueId).toBeNull();
    });

    it("preserves urlIssueId when setView sets issue-detail", () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      // Navigate to issue-detail with an issue
      act(() => {
        result.current.navigateToView("issue-detail", { issueId: "issue-42" });
      });
      expect(result.current.urlIssueId).toBe("issue-42");

      // setView to issue-detail should not clear urlIssueId
      act(() => {
        result.current.setView("issue-detail");
      });
      expect(result.current.view).toBe("issue-detail");
      expect(result.current.urlIssueId).toBe("issue-42");
    });
  });

  describe("preserving other URL params", () => {
    let historyMock: {
      replaceState: ReturnType<typeof vi.fn>;
      pushState: ReturnType<typeof vi.fn>;
    };

    beforeEach(() => {
      historyMock = mockWindowHistory();
    });

    it("preserves other URL params when updating view", () => {
      mockWindowLocation("?priority=2&type=bug");
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current.setView("table");
      });

      const lastCall = historyMock.pushState.mock.calls.at(
        -1,
      )?.[2] as string;
      expect(lastCall).toContain("priority=2");
      expect(lastCall).toContain("type=bug");
      expect(lastCall).toContain("view=table");
    });

    it("preserves other URL params when setting to DEFAULT_VIEW", () => {
      mockWindowLocation("?priority=2&view=table&type=bug");
      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current.setView("kanban");
      });

      const lastCall = historyMock.pushState.mock.calls.at(
        -1,
      )?.[2] as string;
      expect(lastCall).toContain("priority=2");
      expect(lastCall).toContain("type=bug");
      expect(lastCall).not.toContain("view=");
    });

    it("preserves pathname when updating URL", () => {
      mockWindowLocation("");
      Object.defineProperty(window.location, "pathname", {
        value: "/board",
        configurable: true,
      });

      const { result } = renderHook(() => useViewState());

      act(() => {
        result.current.setView("graph");
      });

      expect(historyMock.pushState).toHaveBeenCalledWith(
        null,
        "",
        "/board?view=graph",
      );
    });
  });

  describe("popstate handling", () => {
    beforeEach(() => {
      mockWindowLocation("");
      mockWindowHistory();
    });

    it("updates state on browser back/forward navigation", () => {
      mockWindowLocation("?view=table");
      const { result } = renderHook(() => useViewState());

      expect(result.current.view).toBe("table");

      // Simulate browser navigation (change URL and fire popstate)
      act(() => {
        mockWindowLocation("?view=graph");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current.view).toBe("graph");
    });

    it("returns to DEFAULT_VIEW when navigating to URL without view param", () => {
      mockWindowLocation("?view=table");
      const { result } = renderHook(() => useViewState());

      expect(result.current.view).toBe("table");

      // Simulate browser navigation to URL without view param
      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current.view).toBe("kanban");
    });

    it("cleans up popstate listener on unmount", () => {
      const removeEventListenerSpy = vi.spyOn(window, "removeEventListener");

      const { unmount } = renderHook(() => useViewState());

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "popstate",
        expect.any(Function),
      );
    });

    it("does not add popstate listener when syncUrl is false", () => {
      const addEventListenerSpy = vi.spyOn(window, "addEventListener");

      renderHook(() => useViewState({ syncUrl: false }));

      const popstateCall = addEventListenerSpy.mock.calls.find(
        (call) => call[0] === "popstate",
      );
      expect(popstateCall).toBeUndefined();
    });
  });

  describe("popstate with event.state", () => {
    beforeEach(() => {
      mockWindowLocation("");
      mockWindowHistory();
    });

    it("calls onPopState callback with event.state on popstate", () => {
      const onPopState = vi.fn();
      mockWindowLocation("?view=table");
      renderHook(() => useViewState({ onPopState }));

      act(() => {
        mockWindowLocation("?view=kanban");
        window.dispatchEvent(
          new PopStateEvent("popstate", {
            state: { previousView: "table" },
          }),
        );
      });

      expect(onPopState).toHaveBeenCalledWith({ previousView: "table" });
    });

    it("calls onPopState with null when event.state is null", () => {
      const onPopState = vi.fn();
      mockWindowLocation("?view=table");
      renderHook(() => useViewState({ onPopState }));

      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate", { state: null }));
      });

      expect(onPopState).toHaveBeenCalledWith(null);
    });

    it("calls onPopState with null when event.state is undefined", () => {
      const onPopState = vi.fn();
      mockWindowLocation("?view=table");
      renderHook(() => useViewState({ onPopState }));

      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(onPopState).toHaveBeenCalledWith(null);
    });

    it("does not call onPopState when syncUrl is false", () => {
      const onPopState = vi.fn();
      const addEventListenerSpy = vi.spyOn(window, "addEventListener");

      renderHook(() => useViewState({ syncUrl: false, onPopState }));

      // No popstate listener added
      const popstateListeners = addEventListenerSpy.mock.calls.filter(
        (call) => call[0] === "popstate",
      );
      expect(popstateListeners).toHaveLength(0);
    });
  });

  describe("setter reference stability", () => {
    it("setView function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() =>
        useViewState({ syncUrl: false }),
      );

      const setView1 = result.current.setView;

      rerender();

      const setView2 = result.current.setView;

      expect(setView1).toBe(setView2);
    });

    it("setView remains stable when view changes", () => {
      const { result } = renderHook(() => useViewState({ syncUrl: false }));

      const setView1 = result.current.setView;

      act(() => {
        result.current.setView("table");
      });

      const setView2 = result.current.setView;

      expect(setView1).toBe(setView2);
    });

    it("navigateToView reference is stable across re-renders", () => {
      const { result, rerender } = renderHook(() =>
        useViewState({ syncUrl: false }),
      );

      const nav1 = result.current.navigateToView;

      rerender();

      const nav2 = result.current.navigateToView;

      expect(nav1).toBe(nav2);
    });
  });
});

describe("parseViewFromUrl", () => {
  beforeEach(() => {
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns DEFAULT_VIEW when no view param", () => {
    mockWindowLocation("");
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it("parses kanban view", () => {
    mockWindowLocation("?view=kanban");
    const result = parseViewFromUrl();
    expect(result).toBe("kanban");
  });

  it("parses table view", () => {
    mockWindowLocation("?view=table");
    const result = parseViewFromUrl();
    expect(result).toBe("table");
  });

  it("parses graph view", () => {
    mockWindowLocation("?view=graph");
    const result = parseViewFromUrl();
    expect(result).toBe("graph");
  });

  it("parses monitor view", () => {
    mockWindowLocation("?view=monitor");
    const result = parseViewFromUrl();
    expect(result).toBe("monitor");
  });

  it("parses settings view", () => {
    mockWindowLocation("?view=settings");
    const result = parseViewFromUrl();
    expect(result).toBe("settings");
  });

  it("parses files view", () => {
    mockWindowLocation("?view=files");
    const result = parseViewFromUrl();
    expect(result).toBe("files");
  });

  it("parses issue-detail view", () => {
    mockWindowLocation("?view=issue-detail");
    const result = parseViewFromUrl();
    expect(result).toBe("issue-detail");
  });

  it("returns DEFAULT_VIEW for invalid view", () => {
    mockWindowLocation("?view=invalid");
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it("returns DEFAULT_VIEW for empty view", () => {
    mockWindowLocation("?view=");
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it("returns DEFAULT_VIEW for numeric view", () => {
    mockWindowLocation("?view=123");
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it("returns DEFAULT_VIEW for uppercase view", () => {
    mockWindowLocation("?view=TABLE");
    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
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

describe("SSR/non-browser environment", () => {
  let originalWindow: typeof globalThis.window;

  beforeEach(() => {
    originalWindow = globalThis.window;
  });

  afterEach(() => {
    globalThis.window = originalWindow;
    vi.restoreAllMocks();
  });

  it("parseViewFromUrl returns DEFAULT_VIEW when window is undefined", () => {
    // Simulate non-browser environment
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });

  it("parseViewFromUrl returns DEFAULT_VIEW when location is undefined", () => {
    // Simulate partial browser environment
    // @ts-expect-error - intentionally creating partial window for SSR test
    globalThis.window = {};

    const result = parseViewFromUrl();
    expect(result).toBe(DEFAULT_VIEW);
  });
});

describe("pushState vs replaceState behavior", () => {
  let historyMock: {
    replaceState: ReturnType<typeof vi.fn>;
    pushState: ReturnType<typeof vi.fn>;
  };

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("initial mount sync uses replaceState (not pushState)", () => {
    mockWindowLocation("?view=table");
    historyMock = mockWindowHistory();

    renderHook(() => useViewState());

    // On initial mount, replaceState may be called to sync URL, but pushState should not
    const pushCallsWithView = historyMock.pushState.mock.calls.filter(
      (call) =>
        typeof call[2] === "string" && call[2].includes("view="),
    );
    expect(pushCallsWithView).toHaveLength(0);
  });

  it("setView after mount uses pushState (creates history entry)", () => {
    mockWindowLocation("");
    historyMock = mockWindowHistory();

    const { result } = renderHook(() => useViewState());

    // Clear any calls from mount
    historyMock.pushState.mockClear();
    historyMock.replaceState.mockClear();

    act(() => {
      result.current.setView("graph");
    });

    expect(historyMock.pushState).toHaveBeenCalledWith(
      null,
      "",
      "/app?view=graph",
    );
  });

  it("navigateToView only calls pushState once (skipNextSync prevents double)", () => {
    mockWindowLocation("");
    historyMock = mockWindowHistory();

    const { result } = renderHook(() => useViewState());

    // Clear any calls from mount
    historyMock.pushState.mockClear();
    historyMock.replaceState.mockClear();

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-42" });
    });

    // navigateToView calls pushState once directly; the sync effect should skip
    const pushCallsWithIssueDetail = historyMock.pushState.mock.calls.filter(
      (call) =>
        typeof call[2] === "string" && call[2].includes("view=issue-detail"),
    );
    expect(pushCallsWithIssueDetail).toHaveLength(1);
  });

  it("navigateToView does not trigger replaceState from sync effect", () => {
    mockWindowLocation("");
    historyMock = mockWindowHistory();

    const { result } = renderHook(() => useViewState());

    // Clear any calls from mount
    historyMock.replaceState.mockClear();

    act(() => {
      result.current.navigateToView("table");
    });

    // Sync effect should be skipped, so replaceState should not be called with the view
    const replaceCallsWithTable = historyMock.replaceState.mock.calls.filter(
      (call) =>
        typeof call[2] === "string" && call[2].includes("view=table"),
    );
    expect(replaceCallsWithTable).toHaveLength(0);
  });
});

describe("syncUrl option", () => {
  let historyMock: {
    replaceState: ReturnType<typeof vi.fn>;
    pushState: ReturnType<typeof vi.fn>;
  };

  beforeEach(() => {
    mockWindowLocation("?view=table");
    historyMock = mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("reads from URL when syncUrl is true (default)", () => {
    const { result } = renderHook(() => useViewState());
    expect(result.current.view).toBe("table");
  });

  it("ignores URL when syncUrl is false", () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));
    expect(result.current.view).toBe(DEFAULT_VIEW);
  });

  it("writes to URL when syncUrl is true (default)", () => {
    mockWindowLocation("");
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.setView("graph");
    });

    expect(historyMock.pushState).toHaveBeenCalledWith(
      null,
      "",
      expect.stringContaining("view=graph"),
    );
  });

  it("does not write to URL when syncUrl is false", () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

    act(() => {
      result.current.setView("graph");
    });

    // Neither pushState nor replaceState should be called with view param
    const pushCalls = historyMock.pushState.mock.calls.filter(
      (call) => typeof call[2] === "string" && call[2].includes("view="),
    );
    const replaceCalls = historyMock.replaceState.mock.calls.filter(
      (call) => typeof call[2] === "string" && call[2].includes("view="),
    );
    expect(pushCalls).toHaveLength(0);
    expect(replaceCalls).toHaveLength(0);
  });

  it("does not listen to popstate when syncUrl is false", () => {
    const addEventListenerSpy = vi.spyOn(window, "addEventListener");

    renderHook(() => useViewState({ syncUrl: false }));

    const popstateListeners = addEventListenerSpy.mock.calls.filter(
      (call) => call[0] === "popstate",
    );
    expect(popstateListeners).toHaveLength(0);
  });
});

describe("edge cases", () => {
  beforeEach(() => {
    mockWindowLocation("");
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("handles setting the same view multiple times", () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

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
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

    act(() => {
      result.current.setView("table");
      result.current.setView("graph");
      result.current.setView("kanban");
    });

    expect(result.current.view).toBe("kanban");
  });

  it("works with URL containing hash", () => {
    // Note: URLSearchParams handles hash correctly when passed just the search portion
    // The browser's window.location.search does not include the hash
    mockWindowLocation("?view=table");
    const { result } = renderHook(() => useViewState());

    expect(result.current.view).toBe("table");
  });

  it("handles URL with multiple view params (uses first)", () => {
    // URLSearchParams.get returns the first value when there are duplicates
    mockWindowLocation("?view=table&view=graph");
    const { result } = renderHook(() => useViewState());

    expect(result.current.view).toBe("table");
  });
});

describe("navigateToView", () => {
  let pushStateSpy: ReturnType<typeof vi.fn>;
  let replaceStateSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockWindowLocation("");
    pushStateSpy = vi.fn();
    replaceStateSpy = vi.fn();
    Object.defineProperty(window, "history", {
      value: {
        pushState: pushStateSpy,
        replaceState: replaceStateSpy,
      },
      writable: true,
      configurable: true,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("calls pushState (not replaceState) with correct URL params", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-42" });
    });

    // pushState should be called with issue-detail view and issue param
    expect(pushStateSpy).toHaveBeenCalledWith(
      { issueId: "issue-42" },
      "",
      "/app?view=issue-detail&issue=issue-42",
    );
    expect(pushStateSpy).toHaveBeenCalledTimes(1);
  });

  it("passes state object to pushState", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", {
        previousView: "kanban",
        issueId: "issue-42",
      });
    });

    expect(pushStateSpy).toHaveBeenCalledWith(
      { previousView: "kanban", issueId: "issue-42" },
      "",
      expect.any(String),
    );
  });

  it("updates view state locally", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "abc-123" });
    });

    expect(result.current.view).toBe("issue-detail");
  });

  it("includes issue param in URL when navigating to issue-detail", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "abc-123" });
    });

    const url = pushStateSpy.mock.calls.at(-1)?.[2] as string;
    expect(url).toContain("view=issue-detail");
    expect(url).toContain("issue=abc-123");
  });

  it("sets urlIssueId when navigating to issue-detail", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "abc-123" });
    });

    expect(result.current.urlIssueId).toBe("abc-123");
  });

  it("navigates to a non-detail view with pushState (issue param removed)", () => {
    const { result } = renderHook(() => useViewState());

    // First navigate to detail
    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-99" });
    });

    pushStateSpy.mockClear();

    // Now navigate to table
    act(() => {
      result.current.navigateToView("table");
    });

    expect(pushStateSpy).toHaveBeenCalledWith(null, "", "/app?view=table");

    const url = pushStateSpy.mock.calls.at(-1)?.[2] as string;
    expect(url).not.toContain("issue=");
  });

  it("clears urlIssueId when navigating to non-detail view", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-99" });
    });

    expect(result.current.urlIssueId).toBe("issue-99");

    act(() => {
      result.current.navigateToView("kanban");
    });

    expect(result.current.urlIssueId).toBeNull();
  });

  it("navigating to DEFAULT_VIEW removes view param from URL", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-1" });
    });

    pushStateSpy.mockClear();

    act(() => {
      result.current.navigateToView("kanban");
    });

    const url = pushStateSpy.mock.calls.at(-1)?.[2] as string;
    expect(url).toBe("/app");
    expect(url).not.toContain("view=");
    expect(url).not.toContain("issue=");
  });

  it("URL sync useEffect skips replaceState when URL already matches state (after pushState)", () => {
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-42" });
    });

    // Simulate URL already matching after pushState
    mockWindowLocation("?view=issue-detail&issue=issue-42");

    replaceStateSpy.mockClear();

    // Force a re-render to trigger the useEffect sync
    act(() => {
      result.current.setView(result.current.view);
    });

    // replaceState should not be called because URL already matches state
    const replaceCallsWithIssue = replaceStateSpy.mock.calls.filter(
      (call) =>
        typeof call[2] === "string" && call[2].includes("issue=issue-42"),
    );
    expect(replaceCallsWithIssue).toHaveLength(0);
  });

  it("popstate event updates urlIssueId correctly", () => {
    mockWindowLocation("?view=issue-detail&issue=deep-link-1");
    const { result } = renderHook(() => useViewState());

    expect(result.current.view).toBe("issue-detail");
    expect(result.current.urlIssueId).toBe("deep-link-1");

    // Simulate browser back to a different issue
    act(() => {
      mockWindowLocation("?view=issue-detail&issue=deep-link-2");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });

    expect(result.current.urlIssueId).toBe("deep-link-2");

    // Simulate browser back to a non-detail view
    act(() => {
      mockWindowLocation("?view=table");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });

    expect(result.current.view).toBe("table");
    expect(result.current.urlIssueId).toBeNull();
  });

  it("preserves other URL params (priority, type, etc.)", () => {
    mockWindowLocation("?priority=2&view=kanban&type=bug");
    const { result } = renderHook(() => useViewState());

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-55" });
    });

    const url = pushStateSpy.mock.calls.at(-1)?.[2] as string;
    expect(url).toContain("priority=2");
    expect(url).toContain("type=bug");
    expect(url).toContain("view=issue-detail");
    expect(url).toContain("issue=issue-55");
  });

  it("does not use pushState when syncUrl is false", () => {
    const { result } = renderHook(() => useViewState({ syncUrl: false }));

    act(() => {
      result.current.navigateToView("issue-detail", { issueId: "issue-1" });
    });

    expect(pushStateSpy).not.toHaveBeenCalled();
    // But state should still be updated
    expect(result.current.view).toBe("issue-detail");
    expect(result.current.urlIssueId).toBe("issue-1");
  });

  it("initializes urlIssueId from URL on mount", () => {
    mockWindowLocation("?view=issue-detail&issue=from-url");
    const { result } = renderHook(() => useViewState());

    expect(result.current.view).toBe("issue-detail");
    expect(result.current.urlIssueId).toBe("from-url");
  });

  it("initializes urlIssueId as null when no issue param in URL", () => {
    mockWindowLocation("?view=table");
    const { result } = renderHook(() => useViewState());

    expect(result.current.urlIssueId).toBeNull();
  });
});
