/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useScrollRestore hook.
 * Tests scroll position save/restore lifecycle using mocked scrollTop.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useScrollRestore, clearScrollPositions } from "../useScrollRestore";

/**
 * Helper to create a mock scroll container with controllable scrollTop.
 */
function createMockScrollContainer(initialScrollTop = 0): HTMLElement {
  const el = document.createElement("div");
  let _scrollTop = initialScrollTop;
  Object.defineProperty(el, "scrollTop", {
    get: () => _scrollTop,
    set: (v: number) => {
      _scrollTop = v;
    },
    configurable: true,
  });
  return el;
}

describe("useScrollRestore", () => {
  let rafCallbacks: Array<FrameRequestCallback>;
  let originalRaf: typeof requestAnimationFrame;
  let originalCaf: typeof cancelAnimationFrame;

  beforeEach(() => {
    clearScrollPositions();
    rafCallbacks = [];

    originalRaf = globalThis.requestAnimationFrame;
    originalCaf = globalThis.cancelAnimationFrame;

    // Mock requestAnimationFrame to capture and manually invoke callbacks
    let nextId = 1;
    globalThis.requestAnimationFrame = vi.fn((cb: FrameRequestCallback) => {
      rafCallbacks.push(cb);
      return nextId++;
    });
    globalThis.cancelAnimationFrame = vi.fn();
  });

  afterEach(() => {
    globalThis.requestAnimationFrame = originalRaf;
    globalThis.cancelAnimationFrame = originalCaf;
    clearScrollPositions();
  });

  /**
   * Flush all pending rAF callbacks.
   */
  function flushRaf() {
    const callbacks = [...rafCallbacks];
    rafCallbacks.length = 0;
    for (const cb of callbacks) {
      cb(performance.now());
    }
  }

  describe("save on unmount", () => {
    it("saves scroll position when component unmounts", () => {
      const container = createMockScrollContainer(0);
      container.scrollTop = 150;
      const ref = { current: container };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "kanban",
          scrollContainerRef: ref,
        }),
      );

      unmount();

      // After unmount, scroll position should be saved.
      // We verify by mounting again and checking if restore is attempted.
      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "kanban",
          scrollContainerRef: ref2,
        }),
      );

      flushRaf();

      expect(container2.scrollTop).toBe(150);
    });

    it("saves current scrollTop value at unmount time", () => {
      const container = createMockScrollContainer(0);
      const ref = { current: container };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "table",
          scrollContainerRef: ref,
        }),
      );

      // Simulate scrolling
      container.scrollTop = 300;
      unmount();

      // Verify by restoring
      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "table",
          scrollContainerRef: ref2,
        }),
      );

      flushRaf();
      expect(container2.scrollTop).toBe(300);
    });

    it("does not save when enabled is false", () => {
      const container = createMockScrollContainer(0);
      container.scrollTop = 200;
      const ref = { current: container };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "disabled-view",
          scrollContainerRef: ref,
          enabled: false,
        }),
      );

      unmount();

      // Mount again — should not restore because nothing was saved
      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "disabled-view",
          scrollContainerRef: ref2,
        }),
      );

      flushRaf();
      expect(container2.scrollTop).toBe(0);
    });

    it("does not save when scroll container ref is null", () => {
      const ref = { current: null as HTMLElement | null };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "null-ref",
          scrollContainerRef: ref,
        }),
      );

      unmount();

      // Mount again — should not restore because nothing was saved
      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "null-ref",
          scrollContainerRef: ref2,
        }),
      );

      flushRaf();
      expect(container2.scrollTop).toBe(0);
    });
  });

  describe("restore on mount", () => {
    it("restores saved scroll position on mount via requestAnimationFrame", () => {
      // First: save a position
      const container1 = createMockScrollContainer(0);
      container1.scrollTop = 500;
      const ref1 = { current: container1 };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "restore-test",
          scrollContainerRef: ref1,
        }),
      );
      unmount();

      // Second: mount and verify restore
      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "restore-test",
          scrollContainerRef: ref2,
        }),
      );

      // Before rAF flush, scrollTop should still be 0
      expect(container2.scrollTop).toBe(0);

      flushRaf();

      // After rAF flush, scrollTop should be restored
      expect(container2.scrollTop).toBe(500);
    });

    it("does not restore when no position was saved", () => {
      const container = createMockScrollContainer(0);
      const ref = { current: container };

      renderHook(() =>
        useScrollRestore({
          viewKey: "no-saved-position",
          scrollContainerRef: ref,
        }),
      );

      flushRaf();
      expect(container.scrollTop).toBe(0);
    });

    it("does not call requestAnimationFrame when no position was saved", () => {
      const container = createMockScrollContainer(0);
      const ref = { current: container };

      renderHook(() =>
        useScrollRestore({
          viewKey: "no-raf-call",
          scrollContainerRef: ref,
        }),
      );

      expect(requestAnimationFrame).not.toHaveBeenCalled();
    });

    it("does not restore when enabled is false", () => {
      // Save a position first
      const container1 = createMockScrollContainer(0);
      container1.scrollTop = 250;
      const ref1 = { current: container1 };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "disabled-restore",
          scrollContainerRef: ref1,
        }),
      );
      unmount();

      // Mount with enabled: false
      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "disabled-restore",
          scrollContainerRef: ref2,
          enabled: false,
        }),
      );

      flushRaf();
      expect(container2.scrollTop).toBe(0);
    });
  });

  describe("different view keys", () => {
    it("stores positions independently per viewKey", () => {
      // Save position for 'kanban'
      const c1 = createMockScrollContainer(0);
      c1.scrollTop = 100;
      const r1 = { current: c1 };
      const h1 = renderHook(() =>
        useScrollRestore({ viewKey: "kanban", scrollContainerRef: r1 }),
      );
      h1.unmount();

      // Save position for 'table'
      const c2 = createMockScrollContainer(0);
      c2.scrollTop = 400;
      const r2 = { current: c2 };
      const h2 = renderHook(() =>
        useScrollRestore({ viewKey: "table", scrollContainerRef: r2 }),
      );
      h2.unmount();

      // Restore 'kanban'
      const c3 = createMockScrollContainer(0);
      const r3 = { current: c3 };
      renderHook(() =>
        useScrollRestore({ viewKey: "kanban", scrollContainerRef: r3 }),
      );
      flushRaf();
      expect(c3.scrollTop).toBe(100);

      // Restore 'table'
      const c4 = createMockScrollContainer(0);
      const r4 = { current: c4 };
      renderHook(() =>
        useScrollRestore({ viewKey: "table", scrollContainerRef: r4 }),
      );
      flushRaf();
      expect(c4.scrollTop).toBe(400);
    });
  });

  describe("cleanup", () => {
    it("cancels pending rAF on unmount during restore", () => {
      // Save a position first
      const c1 = createMockScrollContainer(0);
      c1.scrollTop = 100;
      const r1 = { current: c1 };
      const h1 = renderHook(() =>
        useScrollRestore({ viewKey: "cleanup-test", scrollContainerRef: r1 }),
      );
      h1.unmount();

      // Mount, but unmount before rAF fires
      const c2 = createMockScrollContainer(0);
      const r2 = { current: c2 };
      const h2 = renderHook(() =>
        useScrollRestore({ viewKey: "cleanup-test", scrollContainerRef: r2 }),
      );

      // Unmount before flushing rAF
      h2.unmount();

      expect(cancelAnimationFrame).toHaveBeenCalled();
    });
  });

  describe("clearScrollPositions", () => {
    it("clears all stored positions", () => {
      // Save a position
      const c1 = createMockScrollContainer(0);
      c1.scrollTop = 999;
      const r1 = { current: c1 };
      const h1 = renderHook(() =>
        useScrollRestore({ viewKey: "clear-test", scrollContainerRef: r1 }),
      );
      h1.unmount();

      // Clear all
      clearScrollPositions();

      // Attempt restore — should not restore
      const c2 = createMockScrollContainer(0);
      const r2 = { current: c2 };
      renderHook(() =>
        useScrollRestore({ viewKey: "clear-test", scrollContainerRef: r2 }),
      );

      flushRaf();
      expect(c2.scrollTop).toBe(0);
    });
  });

  describe("enabled default", () => {
    it("enabled defaults to true", () => {
      const container = createMockScrollContainer(0);
      container.scrollTop = 75;
      const ref = { current: container };

      const { unmount } = renderHook(() =>
        useScrollRestore({
          viewKey: "default-enabled",
          scrollContainerRef: ref,
          // enabled not specified, should default to true
        }),
      );
      unmount();

      const container2 = createMockScrollContainer(0);
      const ref2 = { current: container2 };
      renderHook(() =>
        useScrollRestore({
          viewKey: "default-enabled",
          scrollContainerRef: ref2,
        }),
      );

      flushRaf();
      expect(container2.scrollTop).toBe(75);
    });
  });

  describe("edge cases", () => {
    it("handles scrollTop of 0 correctly (does not restore 0 since nothing was saved initially)", () => {
      // Mount and unmount with scrollTop = 0
      const c1 = createMockScrollContainer(0);
      const r1 = { current: c1 };
      const h1 = renderHook(() =>
        useScrollRestore({ viewKey: "zero-scroll", scrollContainerRef: r1 }),
      );
      h1.unmount();

      // Position 0 is saved. Restoring 0 should still work.
      const c2 = createMockScrollContainer(50);
      const r2 = { current: c2 };
      renderHook(() =>
        useScrollRestore({ viewKey: "zero-scroll", scrollContainerRef: r2 }),
      );

      flushRaf();
      expect(c2.scrollTop).toBe(0);
    });

    it("overwrites previously saved position on repeated unmounts", () => {
      // First save at 100
      const c1 = createMockScrollContainer(0);
      c1.scrollTop = 100;
      const r1 = { current: c1 };
      const h1 = renderHook(() =>
        useScrollRestore({ viewKey: "overwrite", scrollContainerRef: r1 }),
      );
      h1.unmount();

      // Second mount: restore fires, then user scrolls to 250, then unmount saves 250
      const c2 = createMockScrollContainer(0);
      const r2 = { current: c2 };
      const h2 = renderHook(() =>
        useScrollRestore({ viewKey: "overwrite", scrollContainerRef: r2 }),
      );
      flushRaf(); // restores 100 to c2
      expect(c2.scrollTop).toBe(100);

      // Simulate user scrolling further
      c2.scrollTop = 250;
      h2.unmount(); // saves 250

      // Verify latest value (250) is restored
      const c3 = createMockScrollContainer(0);
      const r3 = { current: c3 };
      renderHook(() =>
        useScrollRestore({ viewKey: "overwrite", scrollContainerRef: r3 }),
      );

      flushRaf();
      expect(c3.scrollTop).toBe(250);
    });
  });
});
