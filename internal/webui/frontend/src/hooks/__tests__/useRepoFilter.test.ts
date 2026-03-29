/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useRepoFilter hook and parseReposFromUrl helper.
 * Hook now uses React Router's useSearchParams instead of manual URL manipulation.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { RouterWrapper } from "@/test-utils/router-wrapper";

import { useRepoFilter, parseReposFromUrl } from "../useRepoFilter";

/**
 * Mock window.location for parseReposFromUrl tests (legacy helper).
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
 * Mock window.history so jsdom doesn't break.
 */
function mockWindowHistory(): void {
  Object.defineProperty(window, "history", {
    value: {
      replaceState: vi.fn(),
      pushState: vi.fn(),
    },
    writable: true,
    configurable: true,
  });
}

describe("useRepoFilter", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("initializes with empty array when no URL param", () => {
      const { result } = renderHook(() => useRepoFilter(), {
        wrapper: RouterWrapper,
      });

      const [repos] = result.current;
      expect(repos).toEqual([]);
    });
  });

  describe("setter", () => {
    it("updates state when setter is called", () => {
      const { result } = renderHook(() => useRepoFilter(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current[1](["api", "frontend"]);
      });

      expect(result.current[0]).toEqual(["api", "frontend"]);
    });

    it("clears repos when setting to empty array", () => {
      const { result } = renderHook(() => useRepoFilter(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current[1](["api"]);
      });
      expect(result.current[0]).toEqual(["api"]);

      act(() => {
        result.current[1]([]);
      });
      expect(result.current[0]).toEqual([]);
    });

    it("allows changing repos multiple times", () => {
      const { result } = renderHook(() => useRepoFilter(), {
        wrapper: RouterWrapper,
      });

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

  describe("setter reference stability", () => {
    it("setRepos function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() => useRepoFilter(), {
        wrapper: RouterWrapper,
      });

      const setRepos1 = result.current[1];

      rerender();

      const setRepos2 = result.current[1];

      expect(setRepos1).toBe(setRepos2);
    });

    it("setRepos remains callable when repos change", () => {
      const { result } = renderHook(() => useRepoFilter(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current[1](["api"]);
      });
      expect(result.current[0]).toEqual(["api"]);

      // Setter still works after state change
      act(() => {
        result.current[1](["api", "frontend"]);
      });
      expect(result.current[0]).toEqual(["api", "frontend"]);
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
