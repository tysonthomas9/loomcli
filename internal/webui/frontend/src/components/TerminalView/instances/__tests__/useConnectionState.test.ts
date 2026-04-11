/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useConnectionState hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useConnectionState } from "../useConnectionState";
import type { TerminalInstanceHandle } from "../TerminalInstance";
import type { TabState } from "@/components/TerminalView/tabs";

// ── Mock @/api/terminal ────────────────────────────────────────────────────

const mockFetchTerminalToken = vi.fn();
const mockRestartTerminalSession = vi.fn();

vi.mock("@/api/terminal", () => ({
  fetchTerminalToken: (...args: unknown[]) => mockFetchTerminalToken(...args),
  restartTerminalSession: (...args: unknown[]) =>
    mockRestartTerminalSession(...args),
}));

// ── Helpers ────────────────────────────────────────────────────────────────

function makeTab(id: string, sessionName?: string): TabState {
  return {
    id,
    label: id,
    sessionName: sessionName ?? id,
    connectionState: "disconnected",
    backendName: "claude",
  };
}

function createOptions(
  overrides: Partial<Parameters<typeof useConnectionState>[0]> = {},
) {
  return {
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    instanceRefs: {
      current: new Map<string, TerminalInstanceHandle>(),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>,
    workspaceId: "ws-1",
    onTabConnected: undefined as ((tabId: string) => void) | undefined,
    ...overrides,
  };
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe("useConnectionState", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTerminalToken.mockResolvedValue("fake-token");
    mockRestartTerminalSession.mockResolvedValue(undefined);
  });

  // 1. handleConnectionStateChange updates tab connection state via setTabs
  it("handleConnectionStateChange updates tab connection state via setTabs", () => {
    const setTabs = vi.fn();
    const opts = createOptions({ setTabs });
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleConnectionStateChange("tab-1", "connected", false);
    });

    expect(setTabs).toHaveBeenCalledTimes(1);
    // The setTabs call receives an updater function — invoke it to verify
    const updater = setTabs.mock.calls[0][0] as (
      prev: TabState[],
    ) => TabState[];
    const prev = [makeTab("tab-1"), makeTab("tab-2")];
    const next = updater(prev);
    expect(next[0].connectionState).toBe("connected");
    expect(next[1].connectionState).toBe("disconnected");
  });

  // 2. First connection tracked in tabHasConnected map
  it("first connection tracked in tabHasConnected map", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useConnectionState(opts));

    expect(result.current.tabHasConnected.has("tab-1")).toBe(false);

    act(() => {
      result.current.handleConnectionStateChange("tab-1", "connected", true);
    });

    expect(result.current.tabHasConnected.get("tab-1")).toBe(true);
  });

  // 3. Duplicate connection doesn't duplicate map entry
  it("duplicate connection does not duplicate map entry", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleConnectionStateChange("tab-1", "connected", true);
    });
    const firstMap = result.current.tabHasConnected;

    act(() => {
      result.current.handleConnectionStateChange("tab-1", "connected", true);
    });
    // Map reference should be identical (no state update)
    expect(result.current.tabHasConnected).toBe(firstMap);
  });

  // 4. onTabConnected called on "connected" state
  it("onTabConnected called on connected state", () => {
    const onTabConnected = vi.fn();
    const opts = createOptions({ onTabConnected });
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleConnectionStateChange("tab-1", "connected", false);
    });

    expect(onTabConnected).toHaveBeenCalledWith("tab-1");
    expect(onTabConnected).toHaveBeenCalledTimes(1);
  });

  // 5. onTabConnected NOT called on "disconnected" state
  it("onTabConnected NOT called on disconnected state", () => {
    const onTabConnected = vi.fn();
    const opts = createOptions({ onTabConnected });
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleConnectionStateChange(
        "tab-1",
        "disconnected",
        false,
      );
    });

    expect(onTabConnected).not.toHaveBeenCalled();
  });

  // 6. handleReconnectStateChange updates state map
  it("handleReconnectStateChange updates state map", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleReconnectStateChange("tab-1", "reconnecting");
    });

    expect(result.current.tabReconnectState.get("tab-1")).toBe("reconnecting");
  });

  // 7. handleReconnectStateChange null clears entry
  it("handleReconnectStateChange null clears entry", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleReconnectStateChange("tab-1", "reconnecting");
    });
    expect(result.current.tabReconnectState.has("tab-1")).toBe(true);

    act(() => {
      result.current.handleReconnectStateChange("tab-1", null);
    });
    expect(result.current.tabReconnectState.has("tab-1")).toBe(false);
  });

  // 8. handleReconnect calls instance reconnect
  it("handleReconnect calls instance reconnect", () => {
    const reconnectFn = vi.fn();
    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([
        [
          "tab-1",
          {
            reconnect: reconnectFn,
            search: vi.fn(),
            findNext: vi.fn(),
            findPrevious: vi.fn(),
            clearSearch: vi.fn(),
            pasteText: vi.fn(),
            getSelection: vi.fn(),
            hasSelection: vi.fn(),
            selectAll: vi.fn(),
            focus: vi.fn(),
          },
        ],
      ]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    const opts = createOptions({ instanceRefs });
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleReconnect("tab-1");
    });

    expect(reconnectFn).toHaveBeenCalledTimes(1);
  });

  // 9. handleBackendCrash sets crashReason on tab
  it("handleBackendCrash sets crashReason on tab", () => {
    const setTabs = vi.fn();
    const opts = createOptions({ setTabs });
    const { result } = renderHook(() => useConnectionState(opts));

    act(() => {
      result.current.handleBackendCrash("tab-1", "OOM killed");
    });

    expect(setTabs).toHaveBeenCalledTimes(1);
    const updater = setTabs.mock.calls[0][0] as (
      prev: TabState[],
    ) => TabState[];
    const prev = [makeTab("tab-1")];
    const next = updater(prev);
    expect(next[0].crashReason).toBe("OOM killed");
  });

  // 10. handleCrashRestart clears crash, fetches token, restarts, reconnects
  it("handleCrashRestart clears crash, fetches token, restarts, reconnects", async () => {
    const setTabs = vi.fn();
    const reconnectFn = vi.fn();
    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([
        [
          "tab-1",
          {
            reconnect: reconnectFn,
            search: vi.fn(),
            findNext: vi.fn(),
            findPrevious: vi.fn(),
            clearSearch: vi.fn(),
            pasteText: vi.fn(),
            getSelection: vi.fn(),
            hasSelection: vi.fn(),
            selectAll: vi.fn(),
            focus: vi.fn(),
          },
        ],
      ]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    const opts = createOptions({ setTabs, instanceRefs, workspaceId: "ws-1" });
    const { result } = renderHook(() => useConnectionState(opts));

    await act(async () => {
      result.current.handleCrashRestart("tab-1", "session-1");
      // Allow promise chain to settle
      await new Promise((r) => setTimeout(r, 0));
    });

    // Crash cleared via setTabs
    expect(setTabs).toHaveBeenCalledTimes(1);
    const updater = setTabs.mock.calls[0][0] as (
      prev: TabState[],
    ) => TabState[];
    const prev = [{ ...makeTab("tab-1"), crashReason: "OOM" }];
    const next = updater(prev);
    expect(next[0].crashReason).toBeNull();

    // Token fetched
    expect(mockFetchTerminalToken).toHaveBeenCalledWith("ws-1", "session-1");

    // Session restarted
    expect(mockRestartTerminalSession).toHaveBeenCalledWith(
      "ws-1",
      "session-1",
      "fake-token",
    );

    // Reconnected
    expect(reconnectFn).toHaveBeenCalledTimes(1);
  });

  it("handleCrashRestart reconnects directly when token is null", async () => {
    mockFetchTerminalToken.mockResolvedValue(null);

    const reconnectFn = vi.fn();
    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([
        [
          "tab-1",
          {
            reconnect: reconnectFn,
            search: vi.fn(),
            findNext: vi.fn(),
            findPrevious: vi.fn(),
            clearSearch: vi.fn(),
            pasteText: vi.fn(),
            getSelection: vi.fn(),
            hasSelection: vi.fn(),
            selectAll: vi.fn(),
            focus: vi.fn(),
          },
        ],
      ]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    const opts = createOptions({ setTabs: vi.fn(), instanceRefs });
    const { result } = renderHook(() => useConnectionState(opts));

    await act(async () => {
      result.current.handleCrashRestart("tab-1", "session-1");
      await new Promise((r) => setTimeout(r, 0));
    });

    // Should still reconnect even though token was null
    expect(reconnectFn).toHaveBeenCalledTimes(1);
    // Should NOT call restartTerminalSession
    expect(mockRestartTerminalSession).not.toHaveBeenCalled();
  });

  it("handleCrashRestart reconnects on error", async () => {
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    mockFetchTerminalToken.mockRejectedValue(new Error("network error"));

    const reconnectFn = vi.fn();
    const instanceRefs = {
      current: new Map<string, TerminalInstanceHandle>([
        [
          "tab-1",
          {
            reconnect: reconnectFn,
            search: vi.fn(),
            findNext: vi.fn(),
            findPrevious: vi.fn(),
            clearSearch: vi.fn(),
            pasteText: vi.fn(),
            getSelection: vi.fn(),
            hasSelection: vi.fn(),
            selectAll: vi.fn(),
            focus: vi.fn(),
          },
        ],
      ]),
    } as React.MutableRefObject<Map<string, TerminalInstanceHandle>>;

    const opts = createOptions({ setTabs: vi.fn(), instanceRefs });
    const { result } = renderHook(() => useConnectionState(opts));

    await act(async () => {
      result.current.handleCrashRestart("tab-1", "session-1");
      await new Promise((r) => setTimeout(r, 0));
    });

    expect(consoleSpy).toHaveBeenCalled();
    // Still reconnects as fallback
    expect(reconnectFn).toHaveBeenCalledTimes(1);

    consoleSpy.mockRestore();
  });
});
