/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useSplitRatio hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useSplitRatio } from "../useSplitRatio";

const STORAGE_KEY = "cortex:detail-panel-split-ratio";

describe("useSplitRatio", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  describe("initial state", () => {
    it("returns default ratio of 0.5 when localStorage is empty", () => {
      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.5);
      expect(result.current.isMaximized).toBe(false);
    });

    it("reads stored ratio from localStorage", () => {
      localStorage.setItem(STORAGE_KEY, "0.7");

      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.7);
    });
  });

  describe("applyDelta", () => {
    it("computes correct new ratio from pixel delta and container height", () => {
      const { result } = renderHook(() => useSplitRatio());

      // Starting at 0.5, delta of 100px in 1000px container = +0.1
      act(() => {
        result.current.applyDelta(1000, 100);
      });

      expect(result.current.ratio).toBeCloseTo(0.6);
    });

    it("does not update ratio when container height is zero", () => {
      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.applyDelta(0, 100);
      });

      expect(result.current.ratio).toBe(0.5);
    });
  });

  describe("clamping", () => {
    it("clamps ratio to minimum of 0.15", () => {
      const { result } = renderHook(() => useSplitRatio());

      // Starting at 0.5, delta of -500px in 1000px container = -0.5 -> 0.0 -> clamped to 0.15
      act(() => {
        result.current.applyDelta(1000, -500);
      });

      expect(result.current.ratio).toBe(0.15);
    });

    it("clamps ratio to maximum of 0.85", () => {
      const { result } = renderHook(() => useSplitRatio());

      // Starting at 0.5, delta of +500px in 1000px container = +0.5 -> 1.0 -> clamped to 0.85
      act(() => {
        result.current.applyDelta(1000, 500);
      });

      expect(result.current.ratio).toBe(0.85);
    });
  });

  describe("resetRatio", () => {
    it("returns ratio to 0.5", () => {
      const { result } = renderHook(() => useSplitRatio());

      // Move away from default
      act(() => {
        result.current.applyDelta(1000, 200);
      });
      expect(result.current.ratio).toBeCloseTo(0.7);

      act(() => {
        result.current.resetRatio();
      });

      expect(result.current.ratio).toBe(0.5);
    });

    it("clears maximized state", () => {
      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.toggleMaximize();
      });
      expect(result.current.isMaximized).toBe(true);

      act(() => {
        result.current.resetRatio();
      });

      expect(result.current.isMaximized).toBe(false);
      expect(result.current.ratio).toBe(0.5);
    });
  });

  describe("toggleMaximize", () => {
    it("sets ratio to 0.05 when maximizing", () => {
      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.toggleMaximize();
      });

      expect(result.current.ratio).toBe(0.05);
      expect(result.current.isMaximized).toBe(true);
    });

    it("restores previous ratio when toggling back", () => {
      const { result } = renderHook(() => useSplitRatio());

      // Move to 0.7
      act(() => {
        result.current.applyDelta(1000, 200);
      });
      expect(result.current.ratio).toBeCloseTo(0.7);

      // Maximize
      act(() => {
        result.current.toggleMaximize();
      });
      expect(result.current.ratio).toBe(0.05);
      expect(result.current.isMaximized).toBe(true);

      // Restore
      act(() => {
        result.current.toggleMaximize();
      });
      expect(result.current.ratio).toBeCloseTo(0.7);
      expect(result.current.isMaximized).toBe(false);
    });
  });

  describe("localStorage persistence", () => {
    it("persists ratio to localStorage on change via applyDelta", () => {
      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.applyDelta(1000, 100);
      });

      expect(localStorage.getItem(STORAGE_KEY)).toBe("0.6");
    });

    it("persists ratio to localStorage on resetRatio", () => {
      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.applyDelta(1000, 200);
      });

      act(() => {
        result.current.resetRatio();
      });

      expect(localStorage.getItem(STORAGE_KEY)).toBe("0.5");
    });

    it("does not overwrite stored ratio when maximizing", () => {
      const { result } = renderHook(() => useSplitRatio());

      // First change ratio to something custom
      act(() => {
        result.current.applyDelta(1000, 200);
      });

      const storedBefore = localStorage.getItem(STORAGE_KEY);

      // Maximize — should NOT overwrite the stored ratio
      act(() => {
        result.current.toggleMaximize();
      });

      expect(localStorage.getItem(STORAGE_KEY)).toBe(storedBefore);
    });
  });

  describe("invalid localStorage values", () => {
    it("falls back to 0.5 for non-numeric stored value", () => {
      localStorage.setItem(STORAGE_KEY, "not-a-number");

      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.5);
    });

    it("falls back to 0.5 for out-of-range stored value (too low)", () => {
      localStorage.setItem(STORAGE_KEY, "0.01");

      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.5);
    });

    it("falls back to 0.5 for out-of-range stored value (too high)", () => {
      localStorage.setItem(STORAGE_KEY, "0.99");

      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.5);
    });

    it("falls back to 0.5 for empty stored value", () => {
      localStorage.setItem(STORAGE_KEY, "");

      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.5);
    });
  });

  describe("localStorage error handling", () => {
    it("handles localStorage.getItem throwing", () => {
      const spy = vi
        .spyOn(Storage.prototype, "getItem")
        .mockImplementation(() => {
          throw new Error("SecurityError");
        });

      const { result } = renderHook(() => useSplitRatio());

      expect(result.current.ratio).toBe(0.5);

      spy.mockRestore();
    });

    it("handles localStorage.setItem throwing on applyDelta", () => {
      const spy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("QuotaExceededError");
        });

      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.applyDelta(1000, 100);
      });

      // State still updates even though localStorage write failed
      expect(result.current.ratio).toBeCloseTo(0.6);

      spy.mockRestore();
    });

    it("handles localStorage.setItem throwing on toggleMaximize", () => {
      const spy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("QuotaExceededError");
        });

      const { result } = renderHook(() => useSplitRatio());

      act(() => {
        result.current.toggleMaximize();
      });

      // State still updates even though localStorage write failed
      expect(result.current.ratio).toBe(0.05);
      expect(result.current.isMaximized).toBe(true);

      spy.mockRestore();
    });
  });
});
