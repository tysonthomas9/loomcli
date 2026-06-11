/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  WORKSPACE_TREE_DEFAULT_WIDTH,
  WORKSPACE_TREE_MAX_WIDTH,
  useWorkspaceTreeWidth,
} from "../useWorkspaceTreeWidth";

const WS_ID = "ws-tree-width";

describe("useWorkspaceTreeWidth", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("starts at the default width and persists resize deltas (debounced)", () => {
    const { result } = renderHook(() => useWorkspaceTreeWidth(WS_ID));

    expect(result.current.width).toBe(WORKSPACE_TREE_DEFAULT_WIDTH);

    act(() => {
      result.current.applyDelta(40);
    });

    expect(result.current.width).toBe(250);

    act(() => {
      vi.runAllTimers();
    });
    expect(localStorage.getItem(`loom:${WS_ID}:workspace-tree-width`)).toBe(
      "250",
    );
  });

  it("flushes a pending write on unmount", () => {
    const { result, unmount } = renderHook(() => useWorkspaceTreeWidth(WS_ID));

    act(() => {
      result.current.applyDelta(40);
    });
    unmount();

    expect(localStorage.getItem(`loom:${WS_ID}:workspace-tree-width`)).toBe(
      "250",
    );
  });

  it("clamps to the max width", () => {
    const { result } = renderHook(() => useWorkspaceTreeWidth(WS_ID));

    act(() => {
      result.current.applyDelta(WORKSPACE_TREE_MAX_WIDTH);
    });

    expect(result.current.width).toBe(WORKSPACE_TREE_MAX_WIDTH);
  });

  it("resets to the default width", () => {
    const { result } = renderHook(() => useWorkspaceTreeWidth(WS_ID));

    act(() => {
      result.current.applyDelta(80);
      result.current.resetWidth();
    });

    expect(result.current.width).toBe(WORKSPACE_TREE_DEFAULT_WIDTH);
  });
});
