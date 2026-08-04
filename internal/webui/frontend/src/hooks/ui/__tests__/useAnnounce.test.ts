/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useAnnounce hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useAnnounce, onAnnounce } from "../useAnnounce";

describe("useAnnounce", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("announce dispatches events", () => {
    it("calls the onAnnounce subscriber with the announced message", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("Hello screen reader");
        vi.advanceTimersByTime(150);
      });

      expect(callback).toHaveBeenCalledTimes(1);
      expect(callback).toHaveBeenCalledWith({
        message: "Hello screen reader",
        priority: "polite",
      });

      unsubscribe();
    });

    it("default priority is polite", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("Polite message");
        vi.advanceTimersByTime(150);
      });

      expect(callback).toHaveBeenCalledWith(
        expect.objectContaining({ priority: "polite" }),
      );

      unsubscribe();
    });

    it("can specify assertive priority", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("Urgent message", "assertive");
        vi.advanceTimersByTime(150);
      });

      expect(callback).toHaveBeenCalledWith({
        message: "Urgent message",
        priority: "assertive",
      });

      unsubscribe();
    });
  });

  describe("debouncing", () => {
    it("does not fire before the debounce window elapses", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("Too early");
        vi.advanceTimersByTime(100); // less than 150ms default
      });

      expect(callback).not.toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(50); // now at 150ms
      });

      expect(callback).toHaveBeenCalledTimes(1);

      unsubscribe();
    });

    it("only fires the last message when called rapidly within the debounce window", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("First");
        result.current.announce("Second");
        result.current.announce("Third");
        vi.advanceTimersByTime(150);
      });

      expect(callback).toHaveBeenCalledTimes(1);
      expect(callback).toHaveBeenCalledWith(
        expect.objectContaining({ message: "Third" }),
      );

      unsubscribe();
    });

    it("fires multiple messages when each is separated by more than the debounce window", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("First");
        vi.advanceTimersByTime(150);
      });

      act(() => {
        result.current.announce("Second");
        vi.advanceTimersByTime(150);
      });

      expect(callback).toHaveBeenCalledTimes(2);
      expect(callback).toHaveBeenNthCalledWith(
        1,
        expect.objectContaining({ message: "First" }),
      );
      expect(callback).toHaveBeenNthCalledWith(
        2,
        expect.objectContaining({ message: "Second" }),
      );

      unsubscribe();
    });

    it("respects a custom debounce interval", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);

      // Use 300ms debounce
      const { result } = renderHook(() => useAnnounce(300));

      act(() => {
        result.current.announce("Slow message");
        vi.advanceTimersByTime(150); // not enough
      });

      expect(callback).not.toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(150); // now at 300ms total
      });

      expect(callback).toHaveBeenCalledTimes(1);

      unsubscribe();
    });
  });

  describe("onAnnounce subscriber", () => {
    it("unsubscribing stops receiving events", () => {
      const callback = vi.fn();
      const unsubscribe = onAnnounce(callback);
      unsubscribe();

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("No one listening");
        vi.advanceTimersByTime(150);
      });

      expect(callback).not.toHaveBeenCalled();
    });

    it("multiple subscribers each receive the event", () => {
      const cb1 = vi.fn();
      const cb2 = vi.fn();
      const unsub1 = onAnnounce(cb1);
      const unsub2 = onAnnounce(cb2);

      const { result } = renderHook(() => useAnnounce());

      act(() => {
        result.current.announce("Broadcast");
        vi.advanceTimersByTime(150);
      });

      expect(cb1).toHaveBeenCalledTimes(1);
      expect(cb2).toHaveBeenCalledTimes(1);

      unsub1();
      unsub2();
    });
  });
});
