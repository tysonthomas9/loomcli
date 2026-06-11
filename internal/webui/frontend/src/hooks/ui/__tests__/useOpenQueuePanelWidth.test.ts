import { act, renderHook } from "@testing-library/react";
/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it } from "vitest";

import {
  OPEN_QUEUE_PANEL_DEFAULT_WIDTH,
  OPEN_QUEUE_PANEL_MAX_WIDTH,
  useOpenQueuePanelWidth,
} from "../useOpenQueuePanelWidth";

const WS_ID = "ws-open-queue-width";

describe("useOpenQueuePanelWidth", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("starts at the default width and persists resize deltas", () => {
    const { result } = renderHook(() => useOpenQueuePanelWidth(WS_ID));

    expect(result.current.width).toBe(OPEN_QUEUE_PANEL_DEFAULT_WIDTH);

    act(() => {
      result.current.applyDelta(40);
    });

    expect(result.current.width).toBe(460);
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
