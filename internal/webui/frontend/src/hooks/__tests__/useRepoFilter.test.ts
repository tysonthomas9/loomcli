/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useRepoFilter hook and parseReposFromUrl helper.
 * Follows useViewState test pattern: URL-synced state via replaceState + popstate.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useRepoFilter, parseReposFromUrl } from "../useRepoFilter";

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

describe("useRepoFilter", () => {
  beforeEach(() => {
    mockWindowLocation();
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("initializes with empty array when no URL param", () => {
      mockWindowLocation("");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual([]);
    });

    it("initializes with empty array when syncUrl is false", () => {
      mockWindowLocation("?repos=api,frontend");
      const { result } = renderHook(() => useRepoFilter({ syncUrl: false }));

      const [repos] = result.current;
      expect(repos).toEqual([]);
    });
  });

  describe("URL parsing", () => {
    it("parses ?repos=api,frontend into ['api', 'frontend']", () => {
      mockWindowLocation("?repos=api,frontend");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual(["api", "frontend"]);
    });

    it("parses single repo from URL", () => {
      mockWindowLocation("?repos=api");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual(["api"]);
    });

    it("handles ?repos= (empty) as []", () => {
      mockWindowLocation("?repos=");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual([]);
    });

    it("trims whitespace from repo names", () => {
      mockWindowLocation("?repos=%20api%20,%20frontend%20");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual(["api", "frontend"]);
    });

    it("filters out empty entries from trailing commas", () => {
      mockWindowLocation("?repos=api,,frontend,");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual(["api", "frontend"]);
    });

    it("ignores other URL params and parses repos correctly", () => {
      mockWindowLocation("?view=kanban&repos=api,backend&priority=2");
      const { result } = renderHook(() => useRepoFilter());

      const [repos] = result.current;
      expect(repos).toEqual(["api", "backend"]);
    });
  });

  describe("setter", () => {
    let historyMock: { replaceState: ReturnType<typeof vi.fn> };

    beforeEach(() => {
      mockWindowLocation("");
      historyMock = mockWindowHistory();
    });

    it("updates state when setter is called", () => {
      const { result } = renderHook(() => useRepoFilter());

      act(() => {
        result.current[1](["api", "frontend"]);
      });

      expect(result.current[0]).toEqual(["api", "frontend"]);
    });

    it("calls replaceState when repos change", () => {
      const { result } = renderHook(() => useRepoFilter());

      act(() => {
        result.current[1](["api", "frontend"]);
      });

      expect(historyMock.replaceState).toHaveBeenCalledWith(
        null,
        "",
        "/app?repos=api%2Cfrontend",
      );
    });

    it("removes repos param from URL when setting to empty array", () => {
      mockWindowLocation("?repos=api");
      const { result } = renderHook(() => useRepoFilter());

      act(() => {
        result.current[1]([]);
      });

      const lastCall = historyMock.replaceState.mock.calls.at(-1);
      expect(lastCall?.[2]).toBe("/app");
    });

    it("does not call replaceState when syncUrl is false", () => {
      const { result } = renderHook(() => useRepoFilter({ syncUrl: false }));

      act(() => {
        result.current[1](["api"]);
      });

      // State should update
      expect(result.current[0]).toEqual(["api"]);

      // replaceState should not be called for repos changes
      const calls = historyMock.replaceState.mock.calls;
      const reposCall = calls.find(
        (call) => typeof call[2] === "string" && call[2].includes("repos="),
      );
      expect(reposCall).toBeUndefined();
    });

    it("allows changing repos multiple times", () => {
      const { result } = renderHook(() => useRepoFilter({ syncUrl: false }));

      act(() => {
        result.current[1](["api"]);
      });
      expect(result.current[0]).toEqual(["api"]);

      act(() => {
        result.current[1](["api", "frontend"]);
      });
      expect(result.current[0]).toEqual(["api", "frontend"]);

      act(() => {
        result.current[1]([]);
      });
      expect(result.current[0]).toEqual([]);
    });
  });

  describe("preserving other URL params", () => {
    let historyMock: { replaceState: ReturnType<typeof vi.fn> };

    beforeEach(() => {
      historyMock = mockWindowHistory();
    });

    it("preserves other URL params when updating repos", () => {
      mockWindowLocation("?view=kanban&priority=2");
      const { result } = renderHook(() => useRepoFilter());

      act(() => {
        result.current[1](["api"]);
      });

      const lastCall = historyMock.replaceState.mock.calls.at(
        -1,
      )?.[2] as string;
      expect(lastCall).toContain("view=kanban");
      expect(lastCall).toContain("priority=2");
      expect(lastCall).toContain("repos=api");
    });
  });

  describe("popstate handling", () => {
    beforeEach(() => {
      mockWindowLocation("");
      mockWindowHistory();
    });

    it("updates state on browser back/forward navigation", () => {
      mockWindowLocation("?repos=api");
      const { result } = renderHook(() => useRepoFilter());

      expect(result.current[0]).toEqual(["api"]);

      // Simulate browser navigation
      act(() => {
        mockWindowLocation("?repos=frontend,backend");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current[0]).toEqual(["frontend", "backend"]);
    });

    it("returns to empty array when navigating to URL without repos param", () => {
      mockWindowLocation("?repos=api");
      const { result } = renderHook(() => useRepoFilter());

      expect(result.current[0]).toEqual(["api"]);

      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current[0]).toEqual([]);
    });

    it("cleans up popstate listener on unmount", () => {
      const removeEventListenerSpy = vi.spyOn(window, "removeEventListener");

      const { unmount } = renderHook(() => useRepoFilter());

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "popstate",
        expect.any(Function),
      );
    });

    it("does not add popstate listener when syncUrl is false", () => {
      const addEventListenerSpy = vi.spyOn(window, "addEventListener");

      renderHook(() => useRepoFilter({ syncUrl: false }));

      const popstateCall = addEventListenerSpy.mock.calls.find(
        (call) => call[0] === "popstate",
      );
      expect(popstateCall).toBeUndefined();
    });
  });

  describe("setter reference stability", () => {
    it("setRepos function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() =>
        useRepoFilter({ syncUrl: false }),
      );

      const setRepos1 = result.current[1];

      rerender();

      const setRepos2 = result.current[1];

      expect(setRepos1).toBe(setRepos2);
    });

    it("setRepos remains stable when repos change", () => {
      const { result } = renderHook(() => useRepoFilter({ syncUrl: false }));

      const setRepos1 = result.current[1];

      act(() => {
        result.current[1](["api"]);
      });

      const setRepos2 = result.current[1];

      expect(setRepos1).toBe(setRepos2);
    });
  });
});

describe("parseReposFromUrl", () => {
  beforeEach(() => {
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns empty array when no repos param", () => {
    mockWindowLocation("");
    const result = parseReposFromUrl();
    expect(result).toEqual([]);
  });

  it("parses single repo", () => {
    mockWindowLocation("?repos=api");
    const result = parseReposFromUrl();
    expect(result).toEqual(["api"]);
  });

  it("parses multiple comma-separated repos", () => {
    mockWindowLocation("?repos=api,frontend,backend");
    const result = parseReposFromUrl();
    expect(result).toEqual(["api", "frontend", "backend"]);
  });

  it("returns empty array for empty repos param", () => {
    mockWindowLocation("?repos=");
    const result = parseReposFromUrl();
    expect(result).toEqual([]);
  });

  it("trims whitespace from repo names", () => {
    mockWindowLocation("?repos=%20api%20,%20frontend%20");
    const result = parseReposFromUrl();
    expect(result).toEqual(["api", "frontend"]);
  });

  it("filters out empty entries", () => {
    mockWindowLocation("?repos=api,,frontend");
    const result = parseReposFromUrl();
    expect(result).toEqual(["api", "frontend"]);
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

  it("parseReposFromUrl returns empty array when window is undefined", () => {
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = parseReposFromUrl();
    expect(result).toEqual([]);
  });

  it("parseReposFromUrl returns empty array when location is undefined", () => {
    // @ts-expect-error - intentionally creating partial window for SSR test
    globalThis.window = {};

    const result = parseReposFromUrl();
    expect(result).toEqual([]);
  });
});
