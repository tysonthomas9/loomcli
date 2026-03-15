/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceParam hook and parseWorkspaceFromUrl helper.
 * Follows useRepoFilter test pattern: URL-synced state via replaceState + popstate.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  useWorkspaceParam,
  parseWorkspaceFromUrl,
} from "../useWorkspaceParam";

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

describe("useWorkspaceParam", () => {
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
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBeNull();
    });

    it("initializes with value when workspace param is present", () => {
      mockWindowLocation("?workspace=myproject");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBe("myproject");
    });

    it("initializes with null when syncUrl is false", () => {
      mockWindowLocation("?workspace=myproject");
      const { result } = renderHook(() =>
        useWorkspaceParam({ syncUrl: false }),
      );

      const [workspace] = result.current;
      expect(workspace).toBeNull();
    });
  });

  describe("URL parsing", () => {
    it("parses workspace name from URL", () => {
      mockWindowLocation("?workspace=myproject");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBe("myproject");
    });

    it("handles empty workspace param as null", () => {
      mockWindowLocation("?workspace=");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBeNull();
    });

    it("handles whitespace-only workspace param as null", () => {
      mockWindowLocation("?workspace=%20%20");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBeNull();
    });

    it("handles workspace names with spaces encoded as +", () => {
      mockWindowLocation("?workspace=my+project");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBe("my project");
    });

    it("handles URL-encoded special characters", () => {
      mockWindowLocation("?workspace=my%2Fproject");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBe("my/project");
    });

    it("ignores other URL params and parses workspace correctly", () => {
      mockWindowLocation("?view=kanban&workspace=myproject&priority=2");
      const { result } = renderHook(() => useWorkspaceParam());

      const [workspace] = result.current;
      expect(workspace).toBe("myproject");
    });
  });

  describe("setter", () => {
    let historyMock: { replaceState: ReturnType<typeof vi.fn> };

    beforeEach(() => {
      mockWindowLocation("");
      historyMock = mockWindowHistory();
    });

    it("updates state when setter is called", () => {
      const { result } = renderHook(() => useWorkspaceParam());

      act(() => {
        result.current[1]("myproject");
      });

      expect(result.current[0]).toBe("myproject");
    });

    it("calls replaceState when workspace changes", () => {
      const { result } = renderHook(() => useWorkspaceParam());

      act(() => {
        result.current[1]("myproject");
      });

      expect(historyMock.replaceState).toHaveBeenCalledWith(
        null,
        "",
        "/app?workspace=myproject",
      );
    });

    it("removes workspace param from URL when setting to null", () => {
      mockWindowLocation("?workspace=myproject");
      const { result } = renderHook(() => useWorkspaceParam());

      act(() => {
        result.current[1](null);
      });

      const lastCall = historyMock.replaceState.mock.calls.at(-1);
      expect(lastCall?.[2]).toBe("/app");
    });

    it("preserves other URL params when updating workspace", () => {
      mockWindowLocation("?view=kanban&priority=2");
      historyMock = mockWindowHistory();
      const { result } = renderHook(() => useWorkspaceParam());

      act(() => {
        result.current[1]("myproject");
      });

      const lastCall = historyMock.replaceState.mock.calls.at(
        -1,
      )?.[2] as string;
      expect(lastCall).toContain("view=kanban");
      expect(lastCall).toContain("priority=2");
      expect(lastCall).toContain("workspace=myproject");
    });

    it("does not call replaceState when syncUrl is false", () => {
      const { result } = renderHook(() =>
        useWorkspaceParam({ syncUrl: false }),
      );

      act(() => {
        result.current[1]("myproject");
      });

      // State should update
      expect(result.current[0]).toBe("myproject");

      // replaceState should not be called for workspace changes
      const calls = historyMock.replaceState.mock.calls;
      const workspaceCall = calls.find(
        (call) =>
          typeof call[2] === "string" && call[2].includes("workspace="),
      );
      expect(workspaceCall).toBeUndefined();
    });

    it("allows changing workspace multiple times", () => {
      const { result } = renderHook(() =>
        useWorkspaceParam({ syncUrl: false }),
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
      mockWindowLocation("?workspace=project-a");
      const { result } = renderHook(() => useWorkspaceParam());

      expect(result.current[0]).toBe("project-a");

      // Simulate browser navigation
      act(() => {
        mockWindowLocation("?workspace=project-b");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current[0]).toBe("project-b");
    });

    it("returns to null when navigating to URL without workspace param", () => {
      mockWindowLocation("?workspace=myproject");
      const { result } = renderHook(() => useWorkspaceParam());

      expect(result.current[0]).toBe("myproject");

      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current[0]).toBeNull();
    });

    it("cleans up popstate listener on unmount", () => {
      const removeEventListenerSpy = vi.spyOn(window, "removeEventListener");

      const { unmount } = renderHook(() => useWorkspaceParam());

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "popstate",
        expect.any(Function),
      );
    });

    it("does not add popstate listener when syncUrl is false", () => {
      const addEventListenerSpy = vi.spyOn(window, "addEventListener");

      renderHook(() => useWorkspaceParam({ syncUrl: false }));

      const popstateCall = addEventListenerSpy.mock.calls.find(
        (call) => call[0] === "popstate",
      );
      expect(popstateCall).toBeUndefined();
    });
  });

  describe("setter reference stability", () => {
    it("setWorkspace function is stable across re-renders", () => {
      const { result, rerender } = renderHook(() =>
        useWorkspaceParam({ syncUrl: false }),
      );

      const setWorkspace1 = result.current[1];

      rerender();

      const setWorkspace2 = result.current[1];

      expect(setWorkspace1).toBe(setWorkspace2);
    });

    it("setWorkspace remains stable when workspace changes", () => {
      const { result } = renderHook(() =>
        useWorkspaceParam({ syncUrl: false }),
      );

      const setWorkspace1 = result.current[1];

      act(() => {
        result.current[1]("myproject");
      });

      const setWorkspace2 = result.current[1];

      expect(setWorkspace1).toBe(setWorkspace2);
    });
  });
});

describe("parseWorkspaceFromUrl", () => {
  beforeEach(() => {
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns null when no workspace param", () => {
    mockWindowLocation("");
    const result = parseWorkspaceFromUrl();
    expect(result).toBeNull();
  });

  it("parses workspace name", () => {
    mockWindowLocation("?workspace=myproject");
    const result = parseWorkspaceFromUrl();
    expect(result).toBe("myproject");
  });

  it("returns null for empty workspace param", () => {
    mockWindowLocation("?workspace=");
    const result = parseWorkspaceFromUrl();
    expect(result).toBeNull();
  });

  it("returns null for whitespace-only workspace param", () => {
    mockWindowLocation("?workspace=%20%20");
    const result = parseWorkspaceFromUrl();
    expect(result).toBeNull();
  });

  it("handles workspace names with special characters", () => {
    mockWindowLocation("?workspace=my%2Fproject");
    const result = parseWorkspaceFromUrl();
    expect(result).toBe("my/project");
  });

  it("handles workspace names with spaces encoded as +", () => {
    mockWindowLocation("?workspace=my+project");
    const result = parseWorkspaceFromUrl();
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

  it("parseWorkspaceFromUrl returns null when window is undefined", () => {
    // @ts-expect-error - intentionally setting window to undefined for SSR test
    delete globalThis.window;

    const result = parseWorkspaceFromUrl();
    expect(result).toBeNull();
  });

  it("parseWorkspaceFromUrl returns null when location is undefined", () => {
    // @ts-expect-error - intentionally creating partial window for SSR test
    globalThis.window = {};

    const result = parseWorkspaceFromUrl();
    expect(result).toBeNull();
  });
});
