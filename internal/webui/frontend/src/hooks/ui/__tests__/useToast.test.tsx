/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useToast hook.
 */

import { renderHook, act } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { ToastProvider, useToast } from "../useToast";

// Wrapper component that provides the ToastProvider context
function wrapper({ children }: { children: ReactNode }) {
  return <ToastProvider>{children}</ToastProvider>;
}

describe("useToast", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("initial state", () => {
    it("returns empty toasts array initially", () => {
      const { result } = renderHook(() => useToast(), { wrapper });
      expect(result.current.toasts).toEqual([]);
    });

    it("throws error when used outside ToastProvider", () => {
      expect(() => {
        renderHook(() => useToast());
      }).toThrow("useToast must be used within a ToastProvider");
    });
  });

  describe("showToast", () => {
    it("adds a toast with correct properties", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Test message");
      });

      expect(result.current.toasts).toHaveLength(1);
      expect(result.current.toasts[0].message).toBe("Test message");
      expect(result.current.toasts[0].type).toBe("info");
      expect(result.current.toasts[0].duration).toBe(5000); // info default
    });

    it("returns toast ID", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      let id: string = "";
      act(() => {
        id = result.current.showToast("Test message");
      });

      expect(id).toMatch(/^toast-\d+-\d+$/);
      expect(result.current.toasts[0].id).toBe(id);
    });

    it("uses specified type from options", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Error message", { type: "error" });
      });

      expect(result.current.toasts[0].type).toBe("error");
    });

    it("uses specified duration from options", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Quick toast", { duration: 2000 });
      });

      expect(result.current.toasts[0].duration).toBe(2000);
    });

    it("supports success type", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Success!", { type: "success" });
      });

      expect(result.current.toasts[0].type).toBe("success");
    });

    it("supports warning type", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Warning!", { type: "warning" });
      });

      expect(result.current.toasts[0].type).toBe("warning");
    });

    it("can add multiple toasts", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("First");
        result.current.showToast("Second");
        result.current.showToast("Third");
      });

      expect(result.current.toasts).toHaveLength(3);
    });
  });

  describe("dismissToast", () => {
    it("removes specific toast by ID", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      let id: string = "";
      act(() => {
        id = result.current.showToast("Test message", { duration: 0 });
      });

      expect(result.current.toasts).toHaveLength(1);

      act(() => {
        result.current.dismissToast(id);
      });

      expect(result.current.toasts).toHaveLength(0);
    });

    it("removes only the specified toast when multiple exist", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      let id1: string = "";
      let id2: string = "";
      act(() => {
        id1 = result.current.showToast("First", { duration: 0 });
        id2 = result.current.showToast("Second", { duration: 0 });
      });

      expect(result.current.toasts).toHaveLength(2);

      act(() => {
        result.current.dismissToast(id1);
      });

      expect(result.current.toasts).toHaveLength(1);
      expect(result.current.toasts[0].id).toBe(id2);
    });

    it("handles dismissing non-existent ID gracefully", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Test", { duration: 0 });
      });

      act(() => {
        result.current.dismissToast("non-existent-id");
      });

      expect(result.current.toasts).toHaveLength(1);
    });
  });

  describe("dismissAll", () => {
    it("removes all toasts", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("First", { duration: 0 });
        result.current.showToast("Second", { duration: 0 });
        result.current.showToast("Third", { duration: 0 });
      });

      expect(result.current.toasts).toHaveLength(3);

      act(() => {
        result.current.dismissAll();
      });

      expect(result.current.toasts).toHaveLength(0);
    });
  });

  describe("auto-dismiss", () => {
    it("automatically dismisses toast after duration", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Auto dismiss", { duration: 3000 });
      });

      expect(result.current.toasts).toHaveLength(1);

      act(() => {
        vi.advanceTimersByTime(3000);
      });

      expect(result.current.toasts).toHaveLength(0);
    });

    it("does not auto-dismiss when duration is 0", async () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("No auto dismiss", { duration: 0 });
      });

      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(result.current.toasts).toHaveLength(1);
    });

    it("clears timeout when toast is manually dismissed", async () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      let id: string = "";
      act(() => {
        id = result.current.showToast("Test", { duration: 5000 });
      });

      act(() => {
        result.current.dismissToast(id);
      });

      expect(result.current.toasts).toHaveLength(0);

      // Advance past the original timeout - should not cause any issues
      act(() => {
        vi.advanceTimersByTime(5000);
      });

      expect(result.current.toasts).toHaveLength(0);
    });
  });

  describe("maxToasts limit", () => {
    it("respects maxToasts limit (default 3)", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        for (let i = 0; i < 5; i++) {
          result.current.showToast(`Toast ${i}`, { duration: 0 });
        }
      });

      // After adding 5 toasts, we should have at most 3
      expect(result.current.toasts.length).toBeLessThanOrEqual(3);
    });

    it("removes oldest toasts when limit exceeded", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Toast 1", { duration: 0 });
        result.current.showToast("Toast 2", { duration: 0 });
        result.current.showToast("Toast 3", { duration: 0 });
        result.current.showToast("Toast 4", { duration: 0 });
      });

      expect(result.current.toasts.length).toBe(3);
      // The oldest (Toast 1) should be removed
      expect(result.current.toasts.map((t) => t.message)).not.toContain(
        "Toast 1",
      );
    });

    it("respects custom maxToasts", () => {
      function customWrapper({ children }: { children: ReactNode }) {
        return <ToastProvider maxToasts={2}>{children}</ToastProvider>;
      }

      const { result } = renderHook(() => useToast(), {
        wrapper: customWrapper,
      });

      act(() => {
        result.current.showToast("Toast 1", { duration: 0 });
        result.current.showToast("Toast 2", { duration: 0 });
        result.current.showToast("Toast 3", { duration: 0 });
      });

      expect(result.current.toasts.length).toBe(2);
    });
  });

  describe("type-specific default durations", () => {
    it("uses 3000ms for success toasts", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Success!", { type: "success" });
      });

      expect(result.current.toasts[0].duration).toBe(3000);
    });

    it("uses 10000ms for error toasts", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Error!", { type: "error" });
      });

      expect(result.current.toasts[0].duration).toBe(10000);
    });

    it("uses 5000ms for warning toasts", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Warning!", { type: "warning" });
      });

      expect(result.current.toasts[0].duration).toBe(5000);
    });

    it("uses 5000ms for info toasts", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Info!", { type: "info" });
      });

      expect(result.current.toasts[0].duration).toBe(5000);
    });

    it("allows overriding default duration", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Custom!", {
          type: "success",
          duration: 7000,
        });
      });

      expect(result.current.toasts[0].duration).toBe(7000);
    });
  });

  describe("onUndo callback", () => {
    it("stores onUndo in toast data", () => {
      const { result } = renderHook(() => useToast(), { wrapper });
      const undoFn = vi.fn();

      act(() => {
        result.current.showToast("Deleted", {
          type: "warning",
          onUndo: undoFn,
        });
      });

      expect(result.current.toasts[0].onUndo).toBe(undoFn);
    });

    it("does not include onUndo when not provided", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Normal toast");
      });

      expect(result.current.toasts[0].onUndo).toBeUndefined();
    });
  });

  describe("coalescing", () => {
    it("coalesces duplicate toasts within 500ms window", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Same message", {
          type: "error",
          duration: 0,
        });
        result.current.showToast("Same message", {
          type: "error",
          duration: 0,
        });
      });

      expect(result.current.toasts).toHaveLength(1);
    });

    it("does not coalesce toasts with different messages", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Message A", { type: "error", duration: 0 });
        result.current.showToast("Message B", { type: "error", duration: 0 });
      });

      expect(result.current.toasts).toHaveLength(2);
    });

    it("does not coalesce toasts with different types", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Same message", {
          type: "error",
          duration: 0,
        });
        result.current.showToast("Same message", {
          type: "warning",
          duration: 0,
        });
      });

      expect(result.current.toasts).toHaveLength(2);
    });

    it("allows duplicate toasts after 500ms window", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      act(() => {
        result.current.showToast("Same message", {
          type: "error",
          duration: 0,
        });
      });

      // Advance past the coalescing window
      act(() => {
        vi.advanceTimersByTime(600);
      });

      act(() => {
        result.current.showToast("Same message", {
          type: "error",
          duration: 0,
        });
      });

      expect(result.current.toasts).toHaveLength(2);
    });
  });

  describe("unique IDs", () => {
    it("generates unique IDs for each toast", () => {
      const { result } = renderHook(() => useToast(), { wrapper });

      const ids: string[] = [];
      act(() => {
        for (let i = 0; i < 10; i++) {
          ids.push(result.current.showToast(`Test ${i}`, { duration: 0 }));
        }
      });

      const uniqueIds = new Set(ids);
      expect(uniqueIds.size).toBe(10);
    });
  });
});
