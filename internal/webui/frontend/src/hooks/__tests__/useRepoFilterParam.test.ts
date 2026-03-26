/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useRepoFilterParam hook and parseRepoFilterFromUrl helper.
 * Hook now uses React Router's useSearchParams instead of manual URL manipulation.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { RouterWrapper } from "@/test-utils/router-wrapper";

import {
  useRepoFilterParam,
  parseRepoFilterFromUrl,
} from "../useRepoFilterParam";

/**
 * Mock window.location for parseRepoFilterFromUrl tests (legacy helper).
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

describe("useRepoFilterParam", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("initializes with null when no URL param", () => {
      const { result } = renderHook(() => useRepoFilterParam(), {
        wrapper: RouterWrapper,
      });

      const [repoFilter] = result.current;
      expect(repoFilter).toBeNull();
    });
  });

  describe("setter", () => {
    it("updates state when setter is called", () => {
      const { result } = renderHook(() => useRepoFilterParam(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current[1]("myproject");
      });

      expect(result.current[0]).toBe("myproject");
    });

    it("clears repoFilter when setting to null", () => {
      const { result } = renderHook(() => useRepoFilterParam(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current[1]("myproject");
      });
      expect(result.current[0]).toBe("myproject");

      act(() => {
        result.current[1](null);
      });
      expect(result.current[0]).toBeNull();
    });

    it("allows changing repoFilter multiple times", () => {
      const { result } = renderHook(() => useRepoFilterParam(), {
        wrapper: RouterWrapper,
      });

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

  describe("setter reference stability", () => {
    it("setRepoFilter function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() => useRepoFilterParam(), {
        wrapper: RouterWrapper,
      });

      const setRepoFilter1 = result.current[1];

      rerender();

      const setRepoFilter2 = result.current[1];

      expect(setRepoFilter1).toBe(setRepoFilter2);
    });

    it("setRepoFilter remains callable when repoFilter changes", () => {
      const { result } = renderHook(() => useRepoFilterParam(), {
        wrapper: RouterWrapper,
      });

      act(() => {
        result.current[1]("myproject");
      });
      expect(result.current[0]).toBe("myproject");

      // Setter still works after state change
      act(() => {
        result.current[1]("other-project");
      });
      expect(result.current[0]).toBe("other-project");
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
