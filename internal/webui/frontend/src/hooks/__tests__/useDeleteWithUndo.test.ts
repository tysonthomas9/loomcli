/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";

import { useDeleteWithUndo } from "../useDeleteWithUndo";

describe("useDeleteWithUndo", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("calls onDelete once when handleConfirmDelete triggered twice rapidly", () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    const onShowToast = vi.fn();

    const { result } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
        onShowToast,
      }),
    );

    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });
    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    act(() => {
      vi.advanceTimersByTime(5000);
    });

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith("workspace-1");
  });

  it("shows 'Deletion already in progress' when onUndo called after timer fires", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    const onShowInfoToast = vi.fn();
    let capturedOnUndo: (() => void) | undefined;
    const onShowToast = vi.fn(
      (_msg: string, opts?: { onUndo?: () => void }) => {
        capturedOnUndo = opts?.onUndo;
      },
    );

    const { result } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
        onShowToast,
        onShowInfoToast,
      }),
    );

    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    // Advance past the delay so the timer fires
    act(() => {
      vi.advanceTimersByTime(5000);
    });

    // Now call undo — timer already fired
    act(() => {
      capturedOnUndo?.();
    });

    expect(onShowInfoToast).toHaveBeenCalledWith(
      "Deletion already in progress",
    );
  });

  it("cancels deletion when onUndo called before timer fires", () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);
    const onShowInfoToast = vi.fn();
    let capturedOnUndo: (() => void) | undefined;
    const onShowToast = vi.fn(
      (_msg: string, opts?: { onUndo?: () => void }) => {
        capturedOnUndo = opts?.onUndo;
      },
    );

    const { result } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
        onShowToast,
        onShowInfoToast,
      }),
    );

    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    // Call undo immediately (before timer fires)
    act(() => {
      capturedOnUndo?.();
    });

    // Advance timers — deletion should not execute
    act(() => {
      vi.advanceTimersByTime(5000);
    });

    expect(onDelete).not.toHaveBeenCalled();
    expect(onShowInfoToast).toHaveBeenCalledWith('"workspace-1" restored');
  });

  it("resets isPending after successful deletion", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);

    const { result } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
      }),
    );

    expect(result.current.isPending()).toBe(false);

    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    expect(result.current.isPending()).toBe(true);

    // Advance and let the async deletion complete
    await act(async () => {
      vi.advanceTimersByTime(5000);
      // Flush microtasks
      await Promise.resolve();
    });

    expect(result.current.isPending()).toBe(false);
  });

  it("resets isPending after failed deletion", async () => {
    const onDelete = vi.fn().mockRejectedValue(new Error("Server error"));
    const onShowErrorToast = vi.fn();

    const { result } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
        onShowErrorToast,
      }),
    );

    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    expect(result.current.isPending()).toBe(true);

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(result.current.isPending()).toBe(false);
    expect(onShowErrorToast).toHaveBeenCalledWith("Server error");
  });

  it("allows new deletion after previous one completes", async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);

    const { result } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
      }),
    );

    // First deletion
    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(onDelete).toHaveBeenCalledTimes(1);

    // Second deletion should work (not blocked by stale ref)
    act(() => {
      result.current.handleConfirmDelete("workspace-2");
    });

    await act(async () => {
      vi.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(onDelete).toHaveBeenCalledTimes(2);
    expect(onDelete).toHaveBeenCalledWith("workspace-2");
  });

  it("cleans up timer on unmount", () => {
    const onDelete = vi.fn().mockResolvedValue(undefined);

    const { result, unmount } = renderHook(() =>
      useDeleteWithUndo({
        delay: 5000,
        onDelete,
      }),
    );

    act(() => {
      result.current.handleConfirmDelete("workspace-1");
    });

    unmount();

    vi.advanceTimersByTime(5000);

    expect(onDelete).not.toHaveBeenCalled();
  });
});
