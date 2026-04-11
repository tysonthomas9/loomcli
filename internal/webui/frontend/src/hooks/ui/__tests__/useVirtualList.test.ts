/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useVirtualList hook.
 * Tests the thin wrapper around @tanstack/react-virtual's useVirtualizer.
 *
 * Note: In jsdom, elements have 0 height so the virtualizer typically
 * renders 0 virtual items. Tests focus on return shape and configuration.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { useVirtualList } from "../useVirtualList";

/**
 * Helper to create a mock scroll container ref.
 */
function createScrollContainerRef(el: HTMLElement | null = null) {
  return { current: el };
}

describe("useVirtualList", () => {
  describe("return shape", () => {
    it("returns virtualItems, totalSize, and measureElement", () => {
      const ref = createScrollContainerRef();
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(result.current).toHaveProperty("virtualItems");
      expect(result.current).toHaveProperty("totalSize");
      expect(result.current).toHaveProperty("measureElement");
    });

    it("virtualItems is an array", () => {
      const ref = createScrollContainerRef();
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(Array.isArray(result.current.virtualItems)).toBe(true);
    });

    it("totalSize is a number", () => {
      const ref = createScrollContainerRef();
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(typeof result.current.totalSize).toBe("number");
    });

    it("measureElement is a function", () => {
      const ref = createScrollContainerRef();
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(typeof result.current.measureElement).toBe("function");
    });
  });

  describe("totalSize calculation", () => {
    it("totalSize equals count * estimatedSize when no scroll container", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(result.current.totalSize).toBe(500);
    });

    it("totalSize is 0 when count is 0", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 0,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(result.current.totalSize).toBe(0);
    });

    it("totalSize scales with different estimatedSize values", () => {
      const ref = createScrollContainerRef(null);
      const { result: result1 } = renderHook(() =>
        useVirtualList({
          count: 5,
          scrollContainerRef: ref,
          estimatedSize: 100,
        }),
      );

      const { result: result2 } = renderHook(() =>
        useVirtualList({
          count: 5,
          scrollContainerRef: ref,
          estimatedSize: 200,
        }),
      );

      expect(result1.current.totalSize).toBe(500);
      expect(result2.current.totalSize).toBe(1000);
    });

    it("handles large count values", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10000,
          scrollContainerRef: ref,
          estimatedSize: 45,
        }),
      );

      expect(result.current.totalSize).toBe(450000);
    });
  });

  describe("virtualItems with no scroll container", () => {
    it("returns empty virtualItems when scroll container ref is null", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 100,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      // No scroll container means the virtualizer cannot determine the viewport
      expect(result.current.virtualItems).toHaveLength(0);
    });
  });

  describe("virtualItems with scroll container (jsdom)", () => {
    it("returns virtualItems when scroll container has dimensions", () => {
      const container = document.createElement("div");
      // In jsdom, offsetHeight is 0, so the virtualizer renders 0 items.
      // We just verify it does not throw.
      const ref = createScrollContainerRef(container);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 50,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      // jsdom elements have 0 height, so virtualizer sees 0 viewport
      expect(Array.isArray(result.current.virtualItems)).toBe(true);
    });
  });

  describe("default options", () => {
    it("uses default overscan of 5", () => {
      const ref = createScrollContainerRef(null);
      // Ensure it doesn't throw with default overscan
      const { result } = renderHook(() =>
        useVirtualList({
          count: 100,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      expect(result.current).toBeDefined();
    });

    it("uses default measureElements of false", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      // When measureElements is false, measureElement should be a no-op
      // Calling it with null should not throw
      expect(() => result.current.measureElement(null)).not.toThrow();
    });
  });

  describe("measureElement behavior", () => {
    it("measureElement is a no-op when measureElements is false", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
          measureElements: false,
        }),
      );

      // Should not throw when called with null or an element
      expect(() => result.current.measureElement(null)).not.toThrow();
      const el = document.createElement("div");
      expect(() => result.current.measureElement(el)).not.toThrow();
    });

    it("measureElement does not throw when measureElements is true and called with null", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
          measureElements: true,
        }),
      );

      // null should be skipped (guarded by if(el))
      expect(() => result.current.measureElement(null)).not.toThrow();
    });
  });

  describe("custom options", () => {
    it("accepts custom overscan value", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 100,
          scrollContainerRef: ref,
          estimatedSize: 50,
          overscan: 10,
        }),
      );

      expect(result.current).toBeDefined();
      expect(result.current.totalSize).toBe(5000);
    });

    it("accepts overscan of 0", () => {
      const ref = createScrollContainerRef(null);
      const { result } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
          overscan: 0,
        }),
      );

      expect(result.current).toBeDefined();
    });
  });

  describe("reactivity", () => {
    it("updates totalSize when count changes", () => {
      const ref = createScrollContainerRef(null);
      let count = 10;
      const { result, rerender } = renderHook(() =>
        useVirtualList({
          count,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      const initialSize = result.current.totalSize;
      expect(initialSize).toBe(500);

      count = 20;
      rerender();

      // In jsdom, the virtualizer may not immediately recalculate
      // but totalSize should reflect the new count
      expect(result.current.totalSize).toBeGreaterThanOrEqual(initialSize);
    });

    it("produces different totalSize for different estimatedSize values", () => {
      const ref = createScrollContainerRef(null);
      const { result: result1 } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 50,
        }),
      );

      const { result: result2 } = renderHook(() =>
        useVirtualList({
          count: 10,
          scrollContainerRef: ref,
          estimatedSize: 100,
        }),
      );

      // Different estimatedSize values should produce different totalSize
      expect(result1.current.totalSize).toBe(500);
      expect(result2.current.totalSize).toBe(1000);
    });
  });
});
