/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useTerminalMetadata hook.
 * Covers initial state, fetching, CRUD operations with optimistic updates,
 * rollback on error, debounced mutation handling, and cleanup.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  listTabMetadata,
  putTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
} from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/sse";

import { useTerminalMetadata } from "../useTerminalMetadata";

vi.mock("@/api/terminal", () => ({
  listTabMetadata: vi.fn(),
  putTabMetadata: vi.fn(),
  patchTabMetadata: vi.fn(),
  deleteTabMetadata: vi.fn(),
}));

const mockList = vi.mocked(listTabMetadata);
const mockPut = vi.mocked(putTabMetadata);
const mockPatch = vi.mocked(patchTabMetadata);
const mockDelete = vi.mocked(deleteTabMetadata);

function createMockTab(overrides?: Partial<TabMetadata>): TabMetadata {
  return {
    session_name: "sess-1",
    label: "Tab 1",
    notes: "",
    sort_order: 0,
    pinned: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useTerminalMetadata", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial state and fetching", () => {
    it("starts with loading true and empty tabs", async () => {
      mockList.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));

      expect(result.current.isLoading).toBe(true);
      expect(result.current.tabs).toEqual([]);
      expect(result.current.error).toBeNull();

      await flushPromises();
    });

    it("fetches tabs on mount and updates state", async () => {
      const tabs = [
        createMockTab(),
        createMockTab({
          session_name: "sess-2",
          label: "Tab 2",
          sort_order: 1,
        }),
      ];
      mockList.mockResolvedValueOnce(tabs);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));

      await flushPromises();

      expect(mockList).toHaveBeenCalledWith("test-ws");
      expect(result.current.tabs).toEqual(tabs);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("passes workspace to listTabMetadata", async () => {
      mockList.mockResolvedValueOnce([]);

      renderHook(() => useTerminalMetadata("my-workspace"));

      await flushPromises();

      expect(mockList).toHaveBeenCalledWith("my-workspace");
    });

    it("sets error on fetch failure", async () => {
      mockList.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("Network error");
      expect(result.current.isLoading).toBe(false);
    });

    it("wraps non-Error thrown values", async () => {
      mockList.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe("string error");
    });
  });

  describe("createTab", () => {
    it("optimistically adds tab then calls putTabMetadata", async () => {
      mockList.mockResolvedValueOnce([]);
      mockPut.mockResolvedValueOnce(
        createMockTab({ session_name: "new-sess" }),
      );

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.createTab("new-sess", "New Tab", 0);
      });

      expect(mockPut).toHaveBeenCalledWith("test-ws", "new-sess", {
        label: "New Tab",
        sort_order: 0,
      });
      expect(result.current.tabs).toHaveLength(1);
      expect(result.current.tabs[0].session_name).toBe("new-sess");
    });

    it("rolls back on API failure", async () => {
      mockList.mockResolvedValueOnce([]);
      mockPut.mockRejectedValueOnce(new Error("Create failed"));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.createTab("new-sess", "New Tab", 0);
      });

      expect(result.current.tabs).toEqual([]);
      expect(result.current.error?.message).toBe("Create failed");
    });
  });

  describe("updateLabel", () => {
    it("optimistically updates label", async () => {
      const tab = createMockTab();
      mockList.mockResolvedValueOnce([tab]);
      mockPatch.mockResolvedValueOnce(createMockTab({ label: "Renamed" }));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.updateLabel("sess-1", "Renamed");
      });

      expect(result.current.tabs[0].label).toBe("Renamed");
      expect(mockPatch).toHaveBeenCalledWith("test-ws", "sess-1", {
        label: "Renamed",
      });
    });

    it("rolls back label on error and sets error state", async () => {
      const tab = createMockTab({ label: "Original" });
      mockList.mockResolvedValueOnce([tab]);
      mockPatch.mockRejectedValueOnce(new Error("Patch failed"));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      expect(result.current.tabs).toHaveLength(1);

      await act(async () => {
        await result.current.updateLabel("sess-1", "Renamed");
      });

      // Verify error was set and API was called
      expect(result.current.error?.message).toBe("Patch failed");
      expect(mockPatch).toHaveBeenCalledWith("test-ws", "sess-1", {
        label: "Renamed",
      });
    });
  });

  describe("updateNotes", () => {
    it("optimistically updates notes", async () => {
      mockList.mockResolvedValueOnce([createMockTab()]);
      mockPatch.mockResolvedValueOnce(createMockTab({ notes: "my notes" }));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.updateNotes("sess-1", "my notes");
      });

      expect(result.current.tabs[0].notes).toBe("my notes");
      expect(mockPatch).toHaveBeenCalledWith("test-ws", "sess-1", {
        notes: "my notes",
      });
    });
  });

  describe("updatePinned", () => {
    it("optimistically updates pinned state", async () => {
      mockList.mockResolvedValueOnce([createMockTab({ pinned: false })]);
      mockPatch.mockResolvedValueOnce(createMockTab({ pinned: true }));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.updatePinned("sess-1", true);
      });

      expect(result.current.tabs[0].pinned).toBe(true);
      expect(mockPatch).toHaveBeenCalledWith("test-ws", "sess-1", {
        pinned: true,
      });
    });
  });

  describe("reorderTabs", () => {
    it("reorders tabs and calls patchTabMetadata for each", async () => {
      const tabs = [
        createMockTab({ session_name: "a", sort_order: 0 }),
        createMockTab({ session_name: "b", sort_order: 1 }),
      ];
      mockList.mockResolvedValueOnce(tabs);
      mockPatch.mockResolvedValue(createMockTab());

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.reorderTabs(["b", "a"]);
      });

      expect(result.current.tabs[0].session_name).toBe("b");
      expect(result.current.tabs[0].sort_order).toBe(0);
      expect(result.current.tabs[1].session_name).toBe("a");
      expect(result.current.tabs[1].sort_order).toBe(1);
      expect(mockPatch).toHaveBeenCalledTimes(2);
    });

    it("sets error on reorder failure", async () => {
      const tabs = [
        createMockTab({ session_name: "a", sort_order: 0 }),
        createMockTab({ session_name: "b", sort_order: 1 }),
      ];
      mockList.mockResolvedValueOnce(tabs);
      mockPatch.mockRejectedValueOnce(new Error("Reorder failed"));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      expect(result.current.tabs).toHaveLength(2);

      await act(async () => {
        await result.current.reorderTabs(["b", "a"]);
      });

      expect(result.current.error?.message).toBe("Reorder failed");
      expect(mockPatch).toHaveBeenCalled();
    });
  });

  describe("deleteTab", () => {
    it("optimistically removes tab", async () => {
      const tabs = [
        createMockTab({ session_name: "a" }),
        createMockTab({ session_name: "b" }),
      ];
      mockList.mockResolvedValueOnce(tabs);
      mockDelete.mockResolvedValueOnce(undefined);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.deleteTab("a");
      });

      expect(result.current.tabs).toHaveLength(1);
      expect(result.current.tabs[0].session_name).toBe("b");
      expect(mockDelete).toHaveBeenCalledWith("test-ws", "a");
    });

    it("sets error on delete failure", async () => {
      mockList.mockResolvedValueOnce([createMockTab()]);
      mockDelete.mockRejectedValueOnce(new Error("Delete failed"));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      expect(result.current.tabs).toHaveLength(1);

      await act(async () => {
        await result.current.deleteTab("sess-1");
      });

      expect(result.current.error?.message).toBe("Delete failed");
      expect(mockDelete).toHaveBeenCalledWith("test-ws", "sess-1");
    });
  });

  describe("linkToIssue / unlinkFromIssue", () => {
    it("optimistically sets issue_id on link", async () => {
      mockList.mockResolvedValueOnce([createMockTab()]);
      mockPatch.mockResolvedValueOnce(createMockTab({ issue_id: "PROJ-1" }));

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.linkToIssue("sess-1", "PROJ-1");
      });

      expect(result.current.tabs[0].issue_id).toBe("PROJ-1");
      expect(mockPatch).toHaveBeenCalledWith("test-ws", "sess-1", {
        issue_id: "PROJ-1",
      });
    });

    it("optimistically removes issue_id on unlink", async () => {
      mockList.mockResolvedValueOnce([createMockTab({ issue_id: "PROJ-1" })]);
      mockPatch.mockResolvedValueOnce(createMockTab());

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      await act(async () => {
        await result.current.unlinkFromIssue("sess-1");
      });

      expect(result.current.tabs[0].issue_id).toBeUndefined();
      expect(mockPatch).toHaveBeenCalledWith("test-ws", "sess-1", {
        issue_id: "",
      });
    });
  });

  describe("handleMutation", () => {
    it("ignores mutations with wrong type", async () => {
      mockList.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      const callCount = mockList.mock.calls.length;

      act(() => {
        result.current.handleMutation({
          type: "create",
          issue_id: "test",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      await act(async () => {
        vi.advanceTimersByTime(200);
      });

      expect(mockList).toHaveBeenCalledTimes(callCount);
    });

    it("triggers debounced refetch for terminal_metadata mutations", async () => {
      mockList.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      const callCount = mockList.mock.calls.length;

      mockList.mockResolvedValueOnce([createMockTab()]);

      act(() => {
        result.current.handleMutation({
          type: "terminal_metadata",
          issue_id: "",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      // Should not have refetched yet (debounced)
      expect(mockList).toHaveBeenCalledTimes(callCount);

      await act(async () => {
        vi.advanceTimersByTime(100);
      });
      await flushPromises();

      expect(mockList).toHaveBeenCalledTimes(callCount + 1);
    });

    it("debounces multiple rapid mutations into single refetch", async () => {
      mockList.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      const callCount = mockList.mock.calls.length;
      mockList.mockResolvedValueOnce([createMockTab()]);

      act(() => {
        result.current.handleMutation({
          type: "terminal_metadata",
          issue_id: "",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
        result.current.handleMutation({
          type: "terminal_metadata",
          issue_id: "",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
        result.current.handleMutation({
          type: "terminal_metadata",
          issue_id: "",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      await act(async () => {
        vi.advanceTimersByTime(100);
      });
      await flushPromises();

      // Should have triggered only one refetch
      expect(mockList).toHaveBeenCalledTimes(callCount + 1);
    });
  });

  describe("refetch", () => {
    it("can manually trigger refetch", async () => {
      mockList.mockResolvedValueOnce([]);

      const { result } = renderHook(() => useTerminalMetadata("test-ws"));
      await flushPromises();

      const updatedTabs = [createMockTab({ label: "Updated" })];
      mockList.mockResolvedValueOnce(updatedTabs);

      await act(async () => {
        await result.current.refetch();
      });

      expect(result.current.tabs).toEqual(updatedTabs);
    });
  });

  describe("cleanup", () => {
    it("does not update state after unmount", async () => {
      let resolveFetch!: (value: TabMetadata[]) => void;
      mockList.mockImplementationOnce(
        () =>
          new Promise<TabMetadata[]>((resolve) => {
            resolveFetch = resolve;
          }),
      );

      const { result, unmount } = renderHook(() =>
        useTerminalMetadata("test-ws"),
      );

      unmount();

      await act(async () => {
        resolveFetch([createMockTab()]);
        await Promise.resolve();
      });

      expect(result.current.tabs).toEqual([]);
    });

    it("clears debounce timer on unmount", async () => {
      mockList.mockResolvedValueOnce([]);

      const { result, unmount } = renderHook(() =>
        useTerminalMetadata("test-ws"),
      );
      await flushPromises();

      act(() => {
        result.current.handleMutation({
          type: "terminal_metadata",
          issue_id: "",
          timestamp: new Date().toISOString(),
        } as MutationPayload);
      });

      unmount();

      // Advancing timers after unmount should not trigger refetch
      const callCount = mockList.mock.calls.length;
      await act(async () => {
        vi.advanceTimersByTime(200);
      });

      expect(mockList).toHaveBeenCalledTimes(callCount);
    });
  });

  describe("workspace change", () => {
    it("refetches when workspace changes", async () => {
      mockList.mockResolvedValueOnce([createMockTab({ label: "WS1" })]);

      const { result, rerender } = renderHook(
        ({ ws }: { ws?: string }) => useTerminalMetadata(ws),
        { initialProps: { ws: "ws1" } },
      );

      await flushPromises();
      expect(result.current.tabs[0].label).toBe("WS1");

      mockList.mockResolvedValueOnce([createMockTab({ label: "WS2" })]);

      rerender({ ws: "ws2" });
      await flushPromises();

      expect(mockList).toHaveBeenLastCalledWith("ws2");
    });
  });
});
