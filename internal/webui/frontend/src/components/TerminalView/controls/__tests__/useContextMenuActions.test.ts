/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useContextMenuActions hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useContextMenuActions } from "../useContextMenuActions";
import type {
  TerminalInstanceHandle,
  ContextMenuEvent,
} from "@/components/TerminalView/instances";

// ── Helpers ────────────────────────────────────────────────────────────────

function makeInstanceHandle(
  overrides: Partial<TerminalInstanceHandle> = {},
): TerminalInstanceHandle {
  return {
    search: vi.fn(),
    findNext: vi.fn(),
    findPrevious: vi.fn(),
    clearSearch: vi.fn(),
    reconnect: vi.fn(),
    pasteText: vi.fn(),
    getSelection: vi.fn().mockReturnValue(""),
    hasSelection: vi.fn().mockReturnValue(false),
    selectAll: vi.fn(),
    focus: vi.fn(),
    ...overrides,
  };
}

function createOptions(
  overrides: Partial<Parameters<typeof useContextMenuActions>[0]> = {},
) {
  return {
    instanceRefs: {
      current: new Map<string, TerminalInstanceHandle>(),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>,
    activeTabId: "tab-1",
    handleCopyNotify: vi.fn(),
    handlePasteRequest: vi.fn(),
    ...overrides,
  };
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe("useContextMenuActions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // 1. handleContextMenu sets state
  it("handleContextMenu sets context menu state", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useContextMenuActions(opts));

    expect(result.current.contextMenu).toBeNull();

    const event: ContextMenuEvent = { x: 100, y: 200, hasSelection: true };
    act(() => {
      result.current.handleContextMenu(event);
    });

    expect(result.current.contextMenu).toEqual(event);
  });

  // 2. handleContextMenuClose clears state
  it("handleContextMenuClose clears context menu state", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useContextMenuActions(opts));

    act(() => {
      result.current.handleContextMenu({ x: 10, y: 20, hasSelection: false });
    });
    expect(result.current.contextMenu).not.toBeNull();

    act(() => {
      result.current.handleContextMenuClose();
    });
    expect(result.current.contextMenu).toBeNull();
  });

  // 3. handleContextMenuCopy reads selection and writes to clipboard
  it("handleContextMenuCopy reads selection and writes to clipboard", async () => {
    const getSelection = vi.fn().mockReturnValue("selected text");
    const focus = vi.fn();
    const instance = makeInstanceHandle({ getSelection, focus });
    const handleCopyNotify = vi.fn();

    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([["tab-1", instance]]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    // Mock clipboard API
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      writable: true,
      configurable: true,
    });

    const opts = createOptions({ instanceRefs, handleCopyNotify });
    const { result } = renderHook(() => useContextMenuActions(opts));

    await act(async () => {
      result.current.handleContextMenuCopy();
      await new Promise((r) => setTimeout(r, 0));
    });

    expect(getSelection).toHaveBeenCalled();
    expect(writeText).toHaveBeenCalledWith("selected text");
    expect(handleCopyNotify).toHaveBeenCalled();
    // Context menu should be closed
    expect(result.current.contextMenu).toBeNull();
    // Focus should be restored
    expect(focus).toHaveBeenCalled();
  });

  // 4. handleContextMenuCopy does nothing without selection
  it("handleContextMenuCopy does nothing without selection", async () => {
    const getSelection = vi.fn().mockReturnValue("");
    const focus = vi.fn();
    const instance = makeInstanceHandle({ getSelection, focus });
    const handleCopyNotify = vi.fn();

    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([["tab-1", instance]]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      writable: true,
      configurable: true,
    });

    const opts = createOptions({ instanceRefs, handleCopyNotify });
    const { result } = renderHook(() => useContextMenuActions(opts));

    await act(async () => {
      result.current.handleContextMenuCopy();
      await new Promise((r) => setTimeout(r, 0));
    });

    expect(getSelection).toHaveBeenCalled();
    // Should NOT write to clipboard since selection is empty string (falsy)
    expect(writeText).not.toHaveBeenCalled();
    expect(handleCopyNotify).not.toHaveBeenCalled();
    // Still closes context menu
    expect(result.current.contextMenu).toBeNull();
  });

  // 5. handleContextMenuPaste closes menu and calls handlePasteRequest
  it("handleContextMenuPaste closes menu and calls handlePasteRequest", () => {
    const handlePasteRequest = vi.fn();
    const opts = createOptions({ handlePasteRequest });
    const { result } = renderHook(() => useContextMenuActions(opts));

    // Open menu first
    act(() => {
      result.current.handleContextMenu({ x: 10, y: 20, hasSelection: false });
    });

    act(() => {
      result.current.handleContextMenuPaste();
    });

    expect(result.current.contextMenu).toBeNull();
    expect(handlePasteRequest).toHaveBeenCalledTimes(1);
  });

  // 6. handleContextMenuSelectAll calls selectAll on instance
  it("handleContextMenuSelectAll calls selectAll on instance", () => {
    const selectAll = vi.fn();
    const instance = makeInstanceHandle({ selectAll });

    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([["tab-1", instance]]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    const opts = createOptions({ instanceRefs });
    const { result } = renderHook(() => useContextMenuActions(opts));

    act(() => {
      result.current.handleContextMenuSelectAll();
    });

    expect(selectAll).toHaveBeenCalledTimes(1);
    // Context menu closed
    expect(result.current.contextMenu).toBeNull();
  });

  it("handleContextMenuCopy handles missing instance gracefully", async () => {
    // No instance in the map
    const opts = createOptions();
    const { result } = renderHook(() => useContextMenuActions(opts));

    // Should not throw
    await act(async () => {
      result.current.handleContextMenuCopy();
      await new Promise((r) => setTimeout(r, 0));
    });

    expect(result.current.contextMenu).toBeNull();
  });
});
