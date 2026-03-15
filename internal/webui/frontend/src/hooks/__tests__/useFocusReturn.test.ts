/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useFocusReturn hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useFocusReturn } from "../useFocusReturn";

describe("useFocusReturn", () => {
  let triggerButton: HTMLButtonElement;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    triggerButton = document.createElement("button");
    triggerButton.textContent = "Trigger";
    document.body.appendChild(triggerButton);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    if (document.body.contains(triggerButton)) {
      document.body.removeChild(triggerButton);
    }
  });

  it("restores focus to previously active element when isOpen goes true then false", () => {
    // Focus the trigger first
    triggerButton.focus();
    expect(document.activeElement).toBe(triggerButton);

    const { rerender } = renderHook(({ isOpen }) => useFocusReturn(isOpen), {
      initialProps: { isOpen: true },
    });

    // Close the panel
    rerender({ isOpen: false });

    expect(document.activeElement).toBe(triggerButton);
  });

  it("does not restore focus when only opened (not closed)", () => {
    const otherButton = document.createElement("button");
    document.body.appendChild(otherButton);
    otherButton.focus();

    renderHook(({ isOpen }) => useFocusReturn(isOpen), {
      initialProps: { isOpen: true },
    });

    // activeElement should still be otherButton (or focusTarget if set),
    // no restoration has happened since we haven't closed
    // Without focusTarget, the hook doesn't change focus on open
    expect(document.activeElement).toBe(otherButton);

    document.body.removeChild(otherButton);
  });

  it("falls back to document.body when trigger is removed before close", () => {
    triggerButton.focus();
    expect(document.activeElement).toBe(triggerButton);

    const { rerender } = renderHook(({ isOpen }) => useFocusReturn(isOpen), {
      initialProps: { isOpen: true },
    });

    // Remove the trigger from the DOM before closing
    document.body.removeChild(triggerButton);

    // Close the panel
    rerender({ isOpen: false });

    expect(document.activeElement).toBe(document.body);
  });

  it("falls back to custom fallback element when trigger is removed", () => {
    triggerButton.focus();

    const fallbackEl = document.createElement("button");
    fallbackEl.textContent = "Fallback";
    document.body.appendChild(fallbackEl);
    const fallbackRef = { current: fallbackEl };

    const { rerender } = renderHook(
      ({ isOpen }) => useFocusReturn(isOpen, { fallbackRef }),
      { initialProps: { isOpen: true } },
    );

    // Remove trigger and its parent chain won't help
    document.body.removeChild(triggerButton);

    rerender({ isOpen: false });

    expect(document.activeElement).toBe(fallbackEl);

    document.body.removeChild(fallbackEl);
  });

  it("focuses focusTarget when panel opens", () => {
    triggerButton.focus();

    const targetEl = document.createElement("input");
    document.body.appendChild(targetEl);
    const focusTarget = { current: targetEl };

    renderHook(({ isOpen }) => useFocusReturn(isOpen, { focusTarget }), {
      initialProps: { isOpen: true },
    });

    expect(document.activeElement).toBe(targetEl);

    document.body.removeChild(targetEl);
  });

  it("defers focus to focusTarget when focusDelay is set", () => {
    triggerButton.focus();

    const targetEl = document.createElement("input");
    document.body.appendChild(targetEl);
    const focusTarget = { current: targetEl };

    renderHook(
      ({ isOpen }) => useFocusReturn(isOpen, { focusTarget, focusDelay: 100 }),
      { initialProps: { isOpen: true } },
    );

    // Before delay, target should not be focused
    expect(document.activeElement).not.toBe(targetEl);

    // After delay, target should be focused
    act(() => {
      vi.advanceTimersByTime(100);
    });

    expect(document.activeElement).toBe(targetEl);

    document.body.removeChild(targetEl);
  });

  it("cancels deferred focus if isOpen changes before delay completes", () => {
    triggerButton.focus();

    const targetEl = document.createElement("input");
    document.body.appendChild(targetEl);
    const focusTarget = { current: targetEl };

    const { rerender } = renderHook(
      ({ isOpen }) => useFocusReturn(isOpen, { focusTarget, focusDelay: 200 }),
      { initialProps: { isOpen: true } },
    );

    // Close before the delay fires
    rerender({ isOpen: false });

    act(() => {
      vi.advanceTimersByTime(200);
    });

    // Target should NOT have received focus because the panel was closed
    // Focus should have been restored to the trigger
    expect(document.activeElement).toBe(triggerButton);

    document.body.removeChild(targetEl);
  });

  it("does nothing on close when no trigger was captured", () => {
    // Start closed, so no trigger is captured
    const { rerender } = renderHook(({ isOpen }) => useFocusReturn(isOpen), {
      initialProps: { isOpen: false },
    });

    // Rerender still closed - should not throw
    rerender({ isOpen: false });

    expect(document.activeElement).toBe(document.body);
  });

  it("captures new trigger element on each open", () => {
    triggerButton.focus();

    const { rerender } = renderHook(({ isOpen }) => useFocusReturn(isOpen), {
      initialProps: { isOpen: true },
    });

    // Close - restores to triggerButton
    rerender({ isOpen: false });
    expect(document.activeElement).toBe(triggerButton);

    // Now focus a different element
    const secondTrigger = document.createElement("button");
    secondTrigger.textContent = "Second Trigger";
    document.body.appendChild(secondTrigger);
    secondTrigger.focus();

    // Open again
    rerender({ isOpen: true });

    // Close again - should restore to secondTrigger
    rerender({ isOpen: false });
    expect(document.activeElement).toBe(secondTrigger);

    document.body.removeChild(secondTrigger);
  });
});
