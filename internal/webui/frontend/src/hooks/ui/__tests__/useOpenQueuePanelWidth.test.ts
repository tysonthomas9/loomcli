/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  OPEN_QUEUE_PANEL_DEFAULT_WIDTH,
  OPEN_QUEUE_PANEL_MAX_WIDTH,
  useOpenQueuePanelWidth,
} from "../useOpenQueuePanelWidth";

const WS_ID = "ws-open-queue-width";

describe("useOpenQueuePanelWidth", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("starts at the default width and persists resize deltas (debounced)", () => {
    const { result } = renderHook(() => useOpenQueuePanelWidth(WS_ID));

    expect(result.current.width).toBe(OPEN_QUEUE_PANEL_DEFAULT_WIDTH);

    act(() => {
      result.current.applyDelta(40);
    });

    expect(result.current.width).toBe(460);

    act(() => {
      vi.runAllTimers();
    });
    expect(localStorage.getItem(`loom:${WS_ID}:open-queue-panel-width`)).toBe(
      "460",
    );
  });

  it("flushes a pending write on unmount", () => {
    const { result, unmount } = renderHook(() => useOpenQueuePanelWidth(WS_ID));

    act(() => {
      result.current.applyDelta(40);
    });
    unmount();

    expect(localStorage.getItem(`loom:${WS_ID}:open-queue-panel-width`)).toBe(
      "460",
    );
  });

  it("clamps to the max width", () => {
    const { result } = renderHook(() => useOpenQueuePanelWidth(WS_ID));

    act(() => {
      result.current.applyDelta(OPEN_QUEUE_PANEL_MAX_WIDTH);
    });

    expect(result.current.width).toBe(OPEN_QUEUE_PANEL_MAX_WIDTH);
  });

  it("resets to the default width", () => {
    const { result } = renderHook(() => useOpenQueuePanelWidth(WS_ID));

    act(() => {
      result.current.applyDelta(80);
      result.current.resetWidth();
    });

    expect(result.current.width).toBe(OPEN_QUEUE_PANEL_DEFAULT_WIDTH);
  });
});
