/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTerminalSearch hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { MutableRefObject } from "react";

import {
  useTerminalSearch,
  type UseTerminalSearchOptions,
} from "../useTerminalSearch";
import type { TerminalInstanceHandle } from "../TerminalInstance";

// Mock the escape layer registration so it doesn't require a React context
vi.mock("@/hooks/ui", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/ui")>("@/hooks/ui");
  return { ...actual, useRegisterEscapeLayer: vi.fn() };
});

function createMockInstance(): TerminalInstanceHandle {
  return {
    search: vi.fn().mockReturnValue(true),
    findNext: vi.fn().mockReturnValue(true),
    findPrevious: vi.fn().mockReturnValue(true),
    clearSearch: vi.fn(),
    reconnect: vi.fn(),
    pasteText: vi.fn(),
    getSelection: vi.fn().mockReturnValue(""),
    hasSelection: vi.fn().mockReturnValue(false),
  };
}

function createOptions(
  overrides: Partial<UseTerminalSearchOptions> = {},
): UseTerminalSearchOptions {
  return {
    instanceRefs: {
      current: new Map(),
    } as MutableRefObject<Map<string, TerminalInstanceHandle>>,
    activeTabId: "tab-1",
    isSplitView: false,
    focusedPane: "left",
    rightPaneTabId: "tab-2",
    isActive: true,
    ...overrides,
  };
}

describe("useTerminalSearch", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("initial state", () => {
    it("starts with search closed and default values", () => {
      const { result } = renderHook(() => useTerminalSearch(createOptions()));

      expect(result.current.isSearchOpen).toBe(false);
      expect(result.current.searchTerm).toBe("");
      expect(result.current.caseSensitive).toBe(false);
      expect(result.current.useRegex).toBe(false);
      expect(result.current.searchResult).toBeNull();
    });
  });

  describe("searchTargetTabId", () => {
    it("returns activeTabId in normal (non-split) mode", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(
          createOptions({
            activeTabId: "tab-1",
            isSplitView: false,
            focusedPane: "left",
            rightPaneTabId: "tab-2",
          }),
        ),
      );

      expect(result.current.searchTargetTabId).toBe("tab-1");
    });

    it("returns activeTabId in split view when left pane is focused", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(
          createOptions({
            activeTabId: "tab-1",
            isSplitView: true,
            focusedPane: "left",
            rightPaneTabId: "tab-2",
          }),
        ),
      );

      expect(result.current.searchTargetTabId).toBe("tab-1");
    });

    it("returns rightPaneTabId in split view when right pane is focused", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(
          createOptions({
            activeTabId: "tab-1",
            isSplitView: true,
            focusedPane: "right",
            rightPaneTabId: "tab-2",
          }),
        ),
      );

      expect(result.current.searchTargetTabId).toBe("tab-2");
    });

    it("returns activeTabId when not in split view even if focusedPane is right", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(
          createOptions({
            activeTabId: "tab-1",
            isSplitView: false,
            focusedPane: "right",
            rightPaneTabId: "tab-2",
          }),
        ),
      );

      expect(result.current.searchTargetTabId).toBe("tab-1");
    });
  });

  describe("handleSearch", () => {
    it("updates searchTerm and calls instance.search with options", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      act(() => {
        result.current.handleSearch("hello");
      });

      expect(result.current.searchTerm).toBe("hello");
      expect(instance.search).toHaveBeenCalledWith("hello", {
        caseSensitive: false,
        regex: false,
      });
    });

    it("passes current caseSensitive and regex options when searching", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      // Toggle case sensitive on
      act(() => {
        result.current.handleToggleCaseSensitive();
      });
      // Toggle regex on
      act(() => {
        result.current.handleToggleRegex();
      });

      act(() => {
        result.current.handleSearch("world");
      });

      expect(instance.search).toHaveBeenLastCalledWith("world", {
        caseSensitive: true,
        regex: true,
      });
    });

    it("does not throw when instance is not found", () => {
      const instanceRefs = {
        current: new Map(),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      expect(() => {
        act(() => {
          result.current.handleSearch("test");
        });
      }).not.toThrow();
    });
  });

  describe("handleFindNext / handleFindPrevious", () => {
    it("calls findNext on the target instance", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      act(() => {
        result.current.handleFindNext();
      });

      expect(instance.findNext).toHaveBeenCalledTimes(1);
    });

    it("calls findPrevious on the target instance", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      act(() => {
        result.current.handleFindPrevious();
      });

      expect(instance.findPrevious).toHaveBeenCalledTimes(1);
    });
  });

  describe("handleToggleCaseSensitive", () => {
    it("flips caseSensitive state", () => {
      const { result } = renderHook(() => useTerminalSearch(createOptions()));

      expect(result.current.caseSensitive).toBe(false);

      act(() => {
        result.current.handleToggleCaseSensitive();
      });

      expect(result.current.caseSensitive).toBe(true);

      act(() => {
        result.current.handleToggleCaseSensitive();
      });

      expect(result.current.caseSensitive).toBe(false);
    });

    it("re-searches with new caseSensitive value when searchTerm is non-empty", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      // Set a search term first
      act(() => {
        result.current.handleSearch("test");
      });

      vi.mocked(instance.search).mockClear();

      act(() => {
        result.current.handleToggleCaseSensitive();
      });

      expect(instance.search).toHaveBeenCalledWith("test", {
        caseSensitive: true,
        regex: false,
      });
    });

    it("does not re-search when searchTerm is empty", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      // No search term set, toggle caseSensitive
      act(() => {
        result.current.handleToggleCaseSensitive();
      });

      // search should not have been called (only the initial effect, not from toggle)
      expect(instance.search).not.toHaveBeenCalled();
    });
  });

  describe("handleToggleRegex", () => {
    it("flips useRegex state", () => {
      const { result } = renderHook(() => useTerminalSearch(createOptions()));

      expect(result.current.useRegex).toBe(false);

      act(() => {
        result.current.handleToggleRegex();
      });

      expect(result.current.useRegex).toBe(true);
    });

    it("re-searches with new regex value when searchTerm is non-empty", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      act(() => {
        result.current.handleSearch("pattern");
      });

      vi.mocked(instance.search).mockClear();

      act(() => {
        result.current.handleToggleRegex();
      });

      expect(instance.search).toHaveBeenCalledWith("pattern", {
        caseSensitive: false,
        regex: true,
      });
    });

    it("does not re-search when searchTerm is empty", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      act(() => {
        result.current.handleToggleRegex();
      });

      expect(instance.search).not.toHaveBeenCalled();
    });
  });

  describe("handleSearchClose", () => {
    it("resets all search state and calls clearSearch on target instance", () => {
      const instance = createMockInstance();
      const instanceRefs = {
        current: new Map([["tab-1", instance]]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ instanceRefs })),
      );

      // Open search and set a term
      act(() => {
        result.current.setIsSearchOpen(true);
      });
      act(() => {
        result.current.handleSearch("hello");
      });

      expect(result.current.isSearchOpen).toBe(true);
      expect(result.current.searchTerm).toBe("hello");

      act(() => {
        result.current.handleSearchClose();
      });

      expect(result.current.isSearchOpen).toBe(false);
      expect(result.current.searchTerm).toBe("");
      expect(result.current.searchResult).toBeNull();
      expect(instance.clearSearch).toHaveBeenCalled();
    });
  });

  describe("handleSearchResultChange", () => {
    it("updates searchResult when tabId matches searchTargetTabId", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ activeTabId: "tab-1" })),
      );

      act(() => {
        result.current.handleSearchResultChange("tab-1", {
          resultIndex: 2,
          resultCount: 5,
        });
      });

      expect(result.current.searchResult).toEqual({
        resultIndex: 2,
        resultCount: 5,
      });
    });

    it("does not update searchResult when tabId does not match searchTargetTabId", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ activeTabId: "tab-1" })),
      );

      act(() => {
        result.current.handleSearchResultChange("tab-99", {
          resultIndex: 1,
          resultCount: 3,
        });
      });

      expect(result.current.searchResult).toBeNull();
    });

    it("sets searchResult to null when passed null", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(createOptions({ activeTabId: "tab-1" })),
      );

      // Set a result first
      act(() => {
        result.current.handleSearchResultChange("tab-1", {
          resultIndex: 0,
          resultCount: 1,
        });
      });
      expect(result.current.searchResult).not.toBeNull();

      act(() => {
        result.current.handleSearchResultChange("tab-1", null);
      });

      expect(result.current.searchResult).toBeNull();
    });

    it("uses rightPaneTabId in split view with right pane focused", () => {
      const { result } = renderHook(() =>
        useTerminalSearch(
          createOptions({
            activeTabId: "tab-1",
            isSplitView: true,
            focusedPane: "right",
            rightPaneTabId: "tab-2",
          }),
        ),
      );

      // tab-2 should be the target, so tab-1 updates should be ignored
      act(() => {
        result.current.handleSearchResultChange("tab-1", {
          resultIndex: 0,
          resultCount: 1,
        });
      });
      expect(result.current.searchResult).toBeNull();

      // tab-2 updates should be accepted
      act(() => {
        result.current.handleSearchResultChange("tab-2", {
          resultIndex: 3,
          resultCount: 10,
        });
      });
      expect(result.current.searchResult).toEqual({
        resultIndex: 3,
        resultCount: 10,
      });
    });
  });

  describe("handleSearchRequest", () => {
    it("toggles isSearchOpen", () => {
      const { result } = renderHook(() => useTerminalSearch(createOptions()));

      expect(result.current.isSearchOpen).toBe(false);

      act(() => {
        result.current.handleSearchRequest();
      });

      expect(result.current.isSearchOpen).toBe(true);

      act(() => {
        result.current.handleSearchRequest();
      });

      expect(result.current.isSearchOpen).toBe(false);
    });
  });

  describe("re-search on tab switch", () => {
    it("re-runs search when searchTargetTabId changes while search is open", () => {
      const instance1 = createMockInstance();
      const instance2 = createMockInstance();
      const instanceRefs = {
        current: new Map([
          ["tab-1", instance1],
          ["tab-2", instance2],
        ]),
      } as MutableRefObject<Map<string, TerminalInstanceHandle>>;

      const options = createOptions({
        instanceRefs,
        activeTabId: "tab-1",
      });

      const { result, rerender } = renderHook(
        (props: UseTerminalSearchOptions) => useTerminalSearch(props),
        { initialProps: options },
      );

      // Open search and search
      act(() => {
        result.current.setIsSearchOpen(true);
      });
      act(() => {
        result.current.handleSearch("hello");
      });

      vi.mocked(instance2.search).mockClear();

      // Switch to tab-2
      rerender({ ...options, activeTabId: "tab-2" });

      // The effect should re-run search on the new tab
      expect(instance2.search).toHaveBeenCalledWith("hello", {
        caseSensitive: false,
        regex: false,
      });
    });
  });
});
