/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useFocusTrap hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useFocusTrap } from "../useFocusTrap";

describe("useFocusTrap", () => {
  let container: HTMLDivElement;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    container = document.createElement("div");
    container.tabIndex = -1;
    document.body.appendChild(container);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    if (document.body.contains(container)) {
      document.body.removeChild(container);
    }
  });

  /**
   * Helper: creates focusable elements inside the container and returns
   * a ref object pointing at the container.
   */
  function setupContainer(innerHTML: string) {
    container.innerHTML = innerHTML;
    // In jsdom, offsetParent is always null. Override it for all focusable
    // children so isFocusable considers them visible.
    const allElements = container.querySelectorAll("*");
    allElements.forEach((el) => {
      Object.defineProperty(el, "offsetParent", {
        get: () => container,
        configurable: true,
      });
    });
    return { current: container };
  }

  describe("initial focus", () => {
    it("focuses the first focusable element when activated", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
        <button>Third</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      const firstButton = container.querySelector("button");
      expect(document.activeElement).toBe(firstButton);
    });

    it("focuses the container when no focusable elements exist", () => {
      const containerRef = setupContainer("<p>No focusable elements</p>");

      renderHook(() => useFocusTrap(containerRef, true));

      expect(document.activeElement).toBe(container);
    });

    it("focuses initialFocus element when provided", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <input type="text" data-testid="target" />
        <button>Third</button>
      `);
      const targetInput = container.querySelector("input")!;
      const initialFocus = { current: targetInput };

      renderHook(() => useFocusTrap(containerRef, true, { initialFocus }));

      expect(document.activeElement).toBe(targetInput);
    });

    it("does not focus anything when isActive is false", () => {
      document.body.focus();
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
      `);

      renderHook(() => useFocusTrap(containerRef, false));

      expect(document.activeElement).toBe(document.body);
    });

    it("defers initial focus when activationDelay is set", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
      `);

      renderHook(() =>
        useFocusTrap(containerRef, true, { activationDelay: 50 }),
      );

      // Not focused yet
      const firstButton = container.querySelector("button");
      expect(document.activeElement).not.toBe(firstButton);

      act(() => {
        vi.advanceTimersByTime(50);
      });

      expect(document.activeElement).toBe(firstButton);
    });
  });

  describe("Tab wrapping", () => {
    it("wraps focus from last element to first on Tab", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
        <button>Third</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      const buttons = container.querySelectorAll("button");
      const lastButton = buttons[buttons.length - 1]!;

      // Focus the last element
      lastButton.focus();
      expect(document.activeElement).toBe(lastButton);

      // Simulate Tab keydown
      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      // Focus should wrap to the first button
      expect(document.activeElement).toBe(buttons[0]);
    });

    it("wraps focus from first element to last on Shift+Tab", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
        <button>Third</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      const buttons = container.querySelectorAll("button");
      const firstButton = buttons[0]!;

      // Focus the first element
      firstButton.focus();
      expect(document.activeElement).toBe(firstButton);

      // Simulate Shift+Tab keydown
      const shiftTabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        shiftKey: true,
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(shiftTabEvent);

      // Focus should wrap to the last button
      expect(document.activeElement).toBe(buttons[buttons.length - 1]);
    });

    it("wraps Shift+Tab from container to last element", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      // Focus the container itself
      container.focus();
      expect(document.activeElement).toBe(container);

      const buttons = container.querySelectorAll("button");

      const shiftTabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        shiftKey: true,
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(shiftTabEvent);

      expect(document.activeElement).toBe(buttons[buttons.length - 1]);
    });

    it("does not intercept Tab when focus is on a middle element", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
        <button>Third</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      const buttons = container.querySelectorAll("button");
      const middleButton = buttons[1]!;

      middleButton.focus();

      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      // Focus should still be on middle button (browser handles normal Tab)
      // The hook only prevents default when on last/first element
      expect(document.activeElement).toBe(middleButton);
    });

    it("does not intercept non-Tab keys", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Second</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      const buttons = container.querySelectorAll("button");
      const lastButton = buttons[buttons.length - 1]!;
      lastButton.focus();

      const enterEvent = new KeyboardEvent("keydown", {
        key: "Enter",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(enterEvent);

      // Focus should remain on last button
      expect(document.activeElement).toBe(lastButton);
    });
  });

  describe("isActive toggling", () => {
    it("does not trap focus when isActive is false", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Last</button>
      `);

      renderHook(() => useFocusTrap(containerRef, false));

      const buttons = container.querySelectorAll("button");
      const lastButton = buttons[buttons.length - 1]!;
      lastButton.focus();

      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      // Focus stays on last button, no wrapping occurred (and no prevent default)
      expect(document.activeElement).toBe(lastButton);
    });

    it("starts trapping after isActive transitions from false to true", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Last</button>
      `);

      const { rerender } = renderHook(
        ({ isActive }) => useFocusTrap(containerRef, isActive),
        { initialProps: { isActive: false } },
      );

      // Activate the trap
      rerender({ isActive: true });

      const buttons = container.querySelectorAll("button");
      const lastButton = buttons[buttons.length - 1]!;
      lastButton.focus();

      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      // Should wrap to first
      expect(document.activeElement).toBe(buttons[0]);
    });

    it("stops trapping after isActive transitions from true to false", () => {
      const containerRef = setupContainer(`
        <button>First</button>
        <button>Last</button>
      `);

      const { rerender } = renderHook(
        ({ isActive }) => useFocusTrap(containerRef, isActive),
        { initialProps: { isActive: true } },
      );

      // Deactivate the trap
      rerender({ isActive: false });

      const buttons = container.querySelectorAll("button");
      const lastButton = buttons[buttons.length - 1]!;
      lastButton.focus();

      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      // No wrapping - focus stays on last
      expect(document.activeElement).toBe(lastButton);
    });
  });

  describe("dynamic content", () => {
    it("re-queries focusable elements on each Tab press", () => {
      const containerRef = setupContainer(`
        <button>Only</button>
      `);

      renderHook(() => useFocusTrap(containerRef, true));

      // Add a new button dynamically
      const newButton = document.createElement("button");
      newButton.textContent = "New";
      Object.defineProperty(newButton, "offsetParent", {
        get: () => container,
        configurable: true,
      });
      container.appendChild(newButton);

      // Focus the new (now last) element
      newButton.focus();
      expect(document.activeElement).toBe(newButton);

      // Tab should wrap to the original first button
      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      const firstButton = container.querySelector("button");
      expect(document.activeElement).toBe(firstButton);
    });
  });

  describe("edge cases", () => {
    it("handles container with single focusable element", () => {
      const containerRef = setupContainer("<button>Only</button>");

      renderHook(() => useFocusTrap(containerRef, true));

      const button = container.querySelector("button")!;
      expect(document.activeElement).toBe(button);

      // Tab on the only element should wrap to itself
      const tabEvent = new KeyboardEvent("keydown", {
        key: "Tab",
        bubbles: true,
        cancelable: true,
      });
      document.dispatchEvent(tabEvent);

      expect(document.activeElement).toBe(button);
    });

    it("handles empty container without throwing", () => {
      const containerRef = setupContainer("");

      // Should not throw
      expect(() => {
        renderHook(() => useFocusTrap(containerRef, true));
      }).not.toThrow();

      // Container itself gets focused as fallback
      expect(document.activeElement).toBe(container);
    });

    it("handles null containerRef gracefully", () => {
      const containerRef = { current: null };

      expect(() => {
        renderHook(() => useFocusTrap(containerRef, true));
      }).not.toThrow();
    });
  });
});
