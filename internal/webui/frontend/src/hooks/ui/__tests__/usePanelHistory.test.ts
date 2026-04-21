/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for usePanelHistory hook.
 *
 * Covers: empty initial state, push/pop/clear, LIFO ordering,
 * empty-stack pop, stack depth cap, and rapid interleaving.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { usePanelHistory } from "../usePanelHistory";

describe("usePanelHistory", () => {
  describe("initial state", () => {
    it("starts with canGoBack=false and depth=0", () => {
      const { result } = renderHook(() => usePanelHistory());

      expect(result.current.canGoBack).toBe(false);
      expect(result.current.depth).toBe(0);
    });

    it("pop on empty stack returns null without throwing", () => {
      const { result } = renderHook(() => usePanelHistory());

      let popped: string | null = "not-null";
      act(() => {
        popped = result.current.pop();
      });

      expect(popped).toBeNull();
      expect(result.current.canGoBack).toBe(false);
      expect(result.current.depth).toBe(0);
    });
  });

  describe("push", () => {
    it("sets canGoBack=true after a push", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
      });

      expect(result.current.canGoBack).toBe(true);
      expect(result.current.depth).toBe(1);
    });

    it("increments depth on successive pushes", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
        result.current.push("B");
        result.current.push("C");
      });

      expect(result.current.depth).toBe(3);
      expect(result.current.canGoBack).toBe(true);
    });
  });

  describe("pop", () => {
    it("returns the most recently pushed id (LIFO)", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
        result.current.push("B");
      });

      let popped: string | null = null;
      act(() => {
        popped = result.current.pop();
      });

      expect(popped).toBe("B");
      expect(result.current.depth).toBe(1);
      expect(result.current.canGoBack).toBe(true);
    });

    it("unwinds stack fully on successive pops", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
        result.current.push("B");
        result.current.push("C");
      });

      const popped: Array<string | null> = [];
      act(() => {
        popped.push(result.current.pop());
      });
      act(() => {
        popped.push(result.current.pop());
      });
      act(() => {
        popped.push(result.current.pop());
      });
      act(() => {
        popped.push(result.current.pop());
      });

      expect(popped).toEqual(["C", "B", "A", null]);
      expect(result.current.canGoBack).toBe(false);
      expect(result.current.depth).toBe(0);
    });

    it("returns the correct value when pop is called multiple times in the same act", () => {
      // Exercises the ref mirror: pop() must read the latest stack even
      // when setStack updates are batched.
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
        result.current.push("B");
      });

      const popped: Array<string | null> = [];
      act(() => {
        popped.push(result.current.pop());
        popped.push(result.current.pop());
        popped.push(result.current.pop());
      });

      expect(popped).toEqual(["B", "A", null]);
      expect(result.current.depth).toBe(0);
    });
  });

  describe("clear", () => {
    it("empties the stack", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
        result.current.push("B");
      });
      expect(result.current.depth).toBe(2);

      act(() => {
        result.current.clear();
      });

      expect(result.current.canGoBack).toBe(false);
      expect(result.current.depth).toBe(0);
    });

    it("is a no-op on an already-empty stack", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.clear();
      });

      expect(result.current.canGoBack).toBe(false);
      expect(result.current.depth).toBe(0);
    });
  });

  describe("stack depth cap", () => {
    it("caps depth at 50, dropping oldest entries FIFO", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        for (let i = 0; i < 55; i++) {
          result.current.push(`issue-${i}`);
        }
      });

      expect(result.current.depth).toBe(50);

      // The oldest 5 entries (issue-0..issue-4) should have been dropped,
      // so popping once returns the latest push (issue-54) and eventually
      // we reach issue-5 at the bottom.
      let popped: string | null = null;
      act(() => {
        popped = result.current.pop();
      });
      expect(popped).toBe("issue-54");

      // Pop remaining 49 entries — the last one should be issue-5.
      const remaining: Array<string | null> = [];
      act(() => {
        for (let i = 0; i < 49; i++) {
          remaining.push(result.current.pop());
        }
      });
      expect(remaining[remaining.length - 1]).toBe("issue-5");
      expect(result.current.depth).toBe(0);
    });
  });

  describe("push/pop interleaving", () => {
    it("handles rapid push/pop/push correctly", () => {
      const { result } = renderHook(() => usePanelHistory());

      act(() => {
        result.current.push("A");
      });
      expect(result.current.depth).toBe(1);

      let popped: string | null = null;
      act(() => {
        popped = result.current.pop();
      });
      expect(popped).toBe("A");

      act(() => {
        result.current.push("B");
        result.current.push("C");
      });
      expect(result.current.depth).toBe(2);

      act(() => {
        popped = result.current.pop();
      });
      expect(popped).toBe("C");
      expect(result.current.depth).toBe(1);
    });
  });

  describe("callback stability", () => {
    it("keeps push/pop/clear references stable across renders", () => {
      const { result, rerender } = renderHook(() => usePanelHistory());

      const firstPush = result.current.push;
      const firstPop = result.current.pop;
      const firstClear = result.current.clear;

      rerender();

      expect(result.current.push).toBe(firstPush);
      expect(result.current.pop).toBe(firstPop);
      expect(result.current.clear).toBe(firstClear);
    });
  });
});
