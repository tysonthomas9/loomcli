/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useRepoFilterParam hook and parseRepoFilterFromUrl helper.
 * Follows useRepoFilter test pattern: URL-synced state via replaceState + popstate.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  useRepoFilterParam,
  parseRepoFilterFromUrl,
} from "../useRepoFilterParam";

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
function mockWindowHistory(): { replaceState: ReturnType<typeof vi.fn> } {
  const replaceState = vi.fn();
  Object.defineProperty(window, "history", {
    value: {
      replaceState,
      pushState: vi.fn(),
    },
    writable: true,
    configurable: true,
  });
  return { replaceState };
}

describe("useRepoFilterParam", () => {
  beforeEach(() => {
    mockWindowLocation();
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("initializes with null when no URL param", () => {
      mockWindowLocation("");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBeNull();
    });

    it("initializes with value when repoFilter param is present", () => {
      mockWindowLocation("?repoFilter=myproject");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBe("myproject");
    });

    it("initializes with null when syncUrl is false", () => {
      mockWindowLocation("?repoFilter=myproject");
      const { result } = renderHook(() =>
        useRepoFilterParam({ syncUrl: false }),
      );

      const [repoFilter] = result.current;
      expect(repoFilter).toBeNull();
    });
  });

  describe("URL parsing", () => {
    it("parses repo filter name from URL", () => {
      mockWindowLocation("?repoFilter=myproject");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBe("myproject");
    });

    it("handles empty repoFilter param as null", () => {
      mockWindowLocation("?repoFilter=");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBeNull();
    });

    it("handles whitespace-only repoFilter param as null", () => {
      mockWindowLocation("?repoFilter=%20%20");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBeNull();
    });

    it("handles repo filter names with spaces encoded as +", () => {
      mockWindowLocation("?repoFilter=my+project");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBe("my project");
    });

    it("handles URL-encoded special characters", () => {
      mockWindowLocation("?repoFilter=my%2Fproject");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBe("my/project");
    });

    it("ignores other URL params and parses repoFilter correctly", () => {
      mockWindowLocation("?view=kanban&repoFilter=myproject&priority=2");
      const { result } = renderHook(() => useRepoFilterParam());

      const [repoFilter] = result.current;
      expect(repoFilter).toBe("myproject");
    });
  });

  describe("setter", () => {
    let historyMock: { replaceState: ReturnType<typeof vi.fn> };

    beforeEach(() => {
      mockWindowLocation("");
      historyMock = mockWindowHistory();
    });

    it("updates state when setter is called", () => {
      const { result } = renderHook(() => useRepoFilterParam());

      act(() => {
        result.current[1]("myproject");
      });

      expect(result.current[0]).toBe("myproject");
    });

    it("calls replaceState when repoFilter changes", () => {
      const { result } = renderHook(() => useRepoFilterParam());

      act(() => {
        result.current[1]("myproject");
      });

      expect(historyMock.replaceState).toHaveBeenCalledWith(
        null,
        "",
        "/app?repoFilter=myproject",
      );
    });

    it("removes repoFilter param from URL when setting to null", () => {
      mockWindowLocation("?repoFilter=myproject");
      const { result } = renderHook(() => useRepoFilterParam());

      act(() => {
        result.current[1](null);
      });

      const lastCall = historyMock.replaceState.mock.calls.at(-1);
      expect(lastCall?.[2]).toBe("/app");
    });

    it("preserves other URL params when updating repoFilter", () => {
      mockWindowLocation("?view=kanban&priority=2");
      historyMock = mockWindowHistory();
      const { result } = renderHook(() => useRepoFilterParam());

      act(() => {
        result.current[1]("myproject");
      });

      const lastCall = historyMock.replaceState.mock.calls.at(
        -1,
      )?.[2] as string;
      expect(lastCall).toContain("view=kanban");
      expect(lastCall).toContain("priority=2");
      expect(lastCall).toContain("repoFilter=myproject");
    });

    it("does not call replaceState when syncUrl is false", () => {
      const { result } = renderHook(() =>
        useRepoFilterParam({ syncUrl: false }),
      );

      act(() => {
        result.current[1]("myproject");
      });

      // State should update
      expect(result.current[0]).toBe("myproject");

      // replaceState should not be called for repoFilter changes
      const calls = historyMock.replaceState.mock.calls;
      const repoFilterCall = calls.find(
        (call) =>
          typeof call[2] === "string" && call[2].includes("repoFilter="),
      );
      expect(repoFilterCall).toBeUndefined();
    });

    it("allows changing repoFilter multiple times", () => {
      const { result } = renderHook(() =>
        useRepoFilterParam({ syncUrl: false }),
      );

      act(() => {
        result.current[1]("project-a");
      });
      expect(result.current[0]).toBe("project-a");

      act(() => {
        result.current[1]("project-b");
      });
      expect(result.current[0]).toBe("project-b");

      act(() => {
        result.current[1](null);
      });
      expect(result.current[0]).toBeNull();
    });
  });

  describe("popstate handling", () => {
    beforeEach(() => {
      mockWindowLocation("");
      mockWindowHistory();
    });

    it("updates state on browser back/forward navigation", () => {
      mockWindowLocation("?repoFilter=project-a");
      const { result } = renderHook(() => useRepoFilterParam());

      expect(result.current[0]).toBe("project-a");

      // Simulate browser navigation
      act(() => {
        mockWindowLocation("?repoFilter=project-b");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current[0]).toBe("project-b");
    });

    it("returns to null when navigating to URL without repoFilter param", () => {
      mockWindowLocation("?repoFilter=myproject");
      const { result } = renderHook(() => useRepoFilterParam());

      expect(result.current[0]).toBe("myproject");

      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current[0]).toBeNull();
    });

    it("cleans up popstate listener on unmount", () => {
      const removeEventListenerSpy = vi.spyOn(window, "removeEventListener");

      const { unmount } = renderHook(() => useRepoFilterParam());

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "popstate",
        expect.any(Function),
      );
    });

    it("does not add popstate listener when syncUrl is false", () => {
      const addEventListenerSpy = vi.spyOn(window, "addEventListener");

      renderHook(() => useRepoFilterParam({ syncUrl: false }));

      const popstateCall = addEventListenerSpy.mock.calls.find(
        (call) => call[0] === "popstate",
      );
      expect(popstateCall).toBeUndefined();
    });
  });

  describe("setter reference stability", () => {
    it("setRepoFilter function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() =>
        useRepoFilterParam({ syncUrl: false }),
      );

      const setRepoFilter1 = result.current[1];

      rerender();

      const setRepoFilter2 = result.current[1];

      expect(setRepoFilter1).toBe(setRepoFilter2);
    });

    it("setRepoFilter remains stable when repoFilter changes", () => {
      const { result } = renderHook(() =>
        useRepoFilterParam({ syncUrl: false }),
      );

      const setRepoFilter1 = result.current[1];

      act(() => {
        result.current[1]("myproject");
      });

      const setRepoFilter2 = result.current[1];

      expect(setRepoFilter1).toBe(setRepoFilter2);
    });
  });
});

describe("parseRepoFilterFromUrl", () => {
  beforeEach(() => {
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns null when no repoFilter param", () => {
    mockWindowLocation("");
    const result = parseRepoFilterFromUrl();
    expect(result).toBeNull();
  });

  it("parses repo filter name", () => {
    mockWindowLocation("?repoFilter=myproject");
    const result = parseRepoFilterFromUrl();
    expect(result).toBe("myproject");
  });

  it("returns null for empty repoFilter param", () => {
    mockWindowLocation("?repoFilter=");
    const result = parseRepoFilterFromUrl();
    expect(result).toBeNull();
  });

  it("returns null for whitespace-only repoFilter param", () => {
    mockWindowLocation("?repoFilter=%20%20");
    const result = parseRepoFilterFromUrl();
    expect(result).toBeNull();
  });

  it("handles repo filter names with special characters", () => {
    mockWindowLocation("?repoFilter=my%2Fproject");
    const result = parseRepoFilterFromUrl();
    expect(result).toBe("my/project");
  });

  it("handles repo filter names with spaces encoded as +", () => {
    mockWindowLocation("?repoFilter=my+project");
    const result = parseRepoFilterFromUrl();
    expect(result).toBe("my project");
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

  it("parseRepoFilterFromUrl returns null when window is undefined", () => {
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = parseRepoFilterFromUrl();
    expect(result).toBeNull();
  });

  it("parseRepoFilterFromUrl returns null when location is undefined", () => {
    // @ts-expect-error - intentionally creating partial window for SSR test
    globalThis.window = {};

    const result = parseRepoFilterFromUrl();
    expect(result).toBeNull();
  });
});
