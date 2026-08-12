/**
 * @vitest-environment jsdom
 */
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode, type ReactNode } from "react";
import type React from "react";

import { useSessionSeeding } from "../useSessionSeeding";
import { ApiError } from "@/types/common";
import type { TabState } from "@/components/TerminalView/tabs";
import * as reconnectBackoff from "@/utils/reconnectBackoff";

const mockHooksApi = vi.hoisted(() => ({
  ensureAgentTerminalSession: vi.fn(),
}));

vi.mock("@/hooks/api", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/api")>("@/hooks/api");
  return {
    ...actual,
    ensureAgentTerminalSession: mockHooksApi.ensureAgentTerminalSession,
  };
});

function makeArgs(overrides: Partial<Parameters<typeof useSessionSeeding>[0]>) {
  return {
    pendingIssueContext: undefined,
    pendingAgentName: undefined,
    tabs: [] as TabState[],
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    createTab: vi.fn().mockResolvedValue(undefined),
    config: { backend: "codex" },
    initializedRef: { current: true } as React.MutableRefObject<boolean>,
    tabsRef: { current: [] as TabState[] },
    workspaceIdRef: { current: "E2E" },
    ...overrides,
  };
}

describe("useSessionSeeding", () => {
  beforeEach(() => {
    mockHooksApi.ensureAgentTerminalSession.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("resolves pending agent names even when a stale restored tab exists", async () => {
    const existingTab: TabState = {
      id: "term_123",
      label: "agent-lead-ui-e2e",
      sessionName: "term_123",
      connectionState: "connected",
      backendName: "codex",
      kind: "agent",
      agentName: "lead-ui-e2e",
    };
    const duplicateExistingTab: TabState = {
      ...existingTab,
      id: "term_older",
      sessionName: "term_older",
    };
    const shellTab: TabState = {
      id: "shell-1",
      label: "Shell",
      sessionName: "shell-1",
      connectionState: "disconnected",
      backendName: "codex",
    };
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const onAgentNameConsumed = vi.fn();
    mockHooksApi.ensureAgentTerminalSession.mockResolvedValueOnce({
      session_name: "term_456",
      label: "agent-lead-ui-e2e",
      notes: "",
      sort_order: 1,
      pinned: false,
      kind: "agent",
      agent_id: "lead-ui-e2e",
      role: "lead",
      backend: "codex",
      writable: true,
      pty_alive: true,
      attached_clients: 0,
      created_at: "2026-05-11T00:00:00Z",
      updated_at: "2026-05-11T00:00:00Z",
    });

    const args = makeArgs({
      pendingAgentName: "lead-ui-e2e",
      tabs: [existingTab, duplicateExistingTab, shellTab],
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      onAgentNameConsumed,
    });

    const wrapper = ({ children }: { children: ReactNode }) => (
      <StrictMode>{children}</StrictMode>
    );
    renderHook(() => useSessionSeeding(args), { wrapper });

    await waitFor(() => {
      expect(setActiveTabId).toHaveBeenCalledWith("term_456");
    });
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledWith(
      "E2E",
      "lead-ui-e2e",
    );
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(1);
    expect(setTabs).toHaveBeenCalledTimes(1);
    const applyTabs = setTabs.mock.calls[0]?.[0] as (
      tabs: TabState[],
    ) => TabState[];
    const nextTabs = applyTabs([existingTab, duplicateExistingTab, shellTab]);
    expect(nextTabs.map((tab) => tab.id)).toEqual(["term_456", "shell-1"]);
    expect(nextTabs[0]?.connectionState).toBe("disconnected");
    expect(onAgentNameConsumed).toHaveBeenCalledTimes(1);
  });

  it("preserves runtime connection state when metadata refreshes an existing agent tab", async () => {
    const existingTab: TabState = {
      id: "term_456",
      label: "agent-lead-ui-e2e-old",
      sessionName: "term_456",
      connectionState: "connected",
      backendName: "claude",
      kind: "agent",
      agentName: "lead-ui-e2e",
      writable: true,
      pinned: false,
      crashReason: null,
    };
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const onAgentNameConsumed = vi.fn();
    mockHooksApi.ensureAgentTerminalSession.mockResolvedValueOnce({
      session_name: "term_456",
      label: "agent-lead-ui-e2e",
      notes: "",
      sort_order: 1,
      pinned: true,
      kind: "agent",
      agent_id: "lead-ui-e2e",
      role: "lead",
      backend: "codex",
      writable: false,
      pty_alive: true,
      attached_clients: 1,
      created_at: "2026-05-11T00:00:00Z",
      updated_at: "2026-05-11T00:00:00Z",
    });

    const args = makeArgs({
      pendingAgentName: "lead-ui-e2e",
      tabs: [existingTab],
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      onAgentNameConsumed,
    });

    renderHook(() => useSessionSeeding(args));

    await waitFor(() => {
      expect(setActiveTabId).toHaveBeenCalledWith("term_456");
    });
    const applyTabs = setTabs.mock.calls[0]?.[0] as (
      tabs: TabState[],
    ) => TabState[];
    const nextTabs = applyTabs([existingTab]);
    expect(nextTabs).toHaveLength(1);
    expect(nextTabs[0]).toMatchObject({
      id: "term_456",
      label: "agent-lead-ui-e2e",
      sessionName: "term_456",
      connectionState: "connected",
      backendName: "codex",
      kind: "agent",
      agentName: "lead-ui-e2e",
      writable: false,
      pinned: true,
      role: "lead",
      crashReason: null,
    });
    expect(onAgentNameConsumed).toHaveBeenCalledTimes(1);
  });

  it("retries a starting ensure without consuming the pending agent", async () => {
    vi.useFakeTimers();
    vi.spyOn(reconnectBackoff, "calculateBackoffDelay").mockReturnValue(1000);
    const onAgentNameConsumed = vi.fn();
    mockHooksApi.ensureAgentTerminalSession
      .mockRejectedValueOnce(
        new ApiError(503, "Service Unavailable", {
          error: "Lead sandbox is waking up…",
          kind: "starting",
        }),
      )
      .mockResolvedValueOnce({
        session_name: "term_456",
        label: "agent-lead-ui-e2e",
        notes: "",
        sort_order: 1,
        pinned: false,
        kind: "agent",
        agent_id: "lead-ui-e2e",
        role: "lead",
        backend: "codex",
        writable: true,
        pty_alive: true,
        attached_clients: 0,
        created_at: "2026-05-11T00:00:00Z",
        updated_at: "2026-05-11T00:00:00Z",
      });

    const args = makeArgs({
      pendingAgentName: "lead-ui-e2e",
      onAgentNameConsumed,
    });
    const { result } = renderHook(() => useSessionSeeding(args));
    await act(async () => {
      for (let i = 0; i < 5; i += 1) await Promise.resolve();
    });

    expect(onAgentNameConsumed).not.toHaveBeenCalled();
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(1);
    expect(result.current.agentResolutionState).toBe("waking");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
      await Promise.resolve();
    });
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(2);
    expect(onAgentNameConsumed).toHaveBeenCalledTimes(1);
    expect(result.current.agentResolutionState).toBe("idle");
  });

  it("fails visibly without consuming the agent on a non-starting error", async () => {
    vi.useFakeTimers();
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onAgentNameConsumed = vi.fn();
    mockHooksApi.ensureAgentTerminalSession.mockRejectedValueOnce(
      new ApiError(503, "Service Unavailable", { error: "unavailable" }),
    );
    const args = makeArgs({
      pendingAgentName: "lead-ui-e2e",
      onAgentNameConsumed,
    });
    const { result } = renderHook(() => useSessionSeeding(args));
    await act(async () => {
      for (let i = 0; i < 5; i += 1) await Promise.resolve();
      await vi.runOnlyPendingTimersAsync();
    });

    // A non-starting failure must not retry and must not silently consume the
    // pending agent — consuming it would leave a previously-selected lead's live
    // terminal attached. Instead it surfaces a failed resolution carrying the
    // server's reason, so the failure overlay renders in place of any terminal.
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(1);
    expect(onAgentNameConsumed).not.toHaveBeenCalled();
    expect(result.current.agentResolutionState).toBe("failed");
    expect(result.current.agentResolutionError).toBe("unavailable");
  });

  it("does not retry early when tab state churns during waking", async () => {
    vi.useFakeTimers();
    vi.spyOn(reconnectBackoff, "calculateBackoffDelay").mockReturnValue(1000);
    mockHooksApi.ensureAgentTerminalSession.mockRejectedValue(
      new ApiError(503, "Service Unavailable", {
        error: "Lead sandbox is waking up…",
        kind: "starting",
      }),
    );
    const tabsRef = { current: [] as TabState[] };
    const args = makeArgs({
      pendingAgentName: "lead-ui-e2e",
      tabsRef,
    });
    const { rerender } = renderHook(
      ({ tabs }: { tabs: TabState[] }) => {
        tabsRef.current = tabs;
        return useSessionSeeding({ ...args, tabs });
      },
      { initialProps: { tabs: [] as TabState[] } },
    );
    await act(async () => {
      for (let i = 0; i < 5; i += 1) await Promise.resolve();
    });
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(1);

    rerender({
      tabs: [
        {
          id: "shell-1",
          label: "Shell",
          sessionName: "shell-1",
          connectionState: "disconnected",
          backendName: "shell",
        },
      ],
    });
    await act(async () => {
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(999);
    });
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(2);
  });

  it("surfaces a visible failure state when the waking retry budget is exhausted", async () => {
    vi.useFakeTimers();
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.spyOn(reconnectBackoff, "calculateBackoffDelay").mockReturnValue(1000);
    const onAgentNameConsumed = vi.fn();
    mockHooksApi.ensureAgentTerminalSession.mockRejectedValue(
      new ApiError(503, "Service Unavailable", {
        error: "Lead sandbox is waking up…",
        kind: "starting",
      }),
    );
    const args = makeArgs({
      pendingAgentName: "lead-ui-e2e",
      onAgentNameConsumed,
    });
    const { result } = renderHook(() => useSessionSeeding(args));
    await act(async () => {
      for (let i = 0; i < 5; i += 1) await Promise.resolve();
      for (let i = 0; i < 10; i += 1) {
        await vi.advanceTimersByTimeAsync(1000);
      }
    });

    expect(mockHooksApi.ensureAgentTerminalSession).toHaveBeenCalledTimes(11);
    expect(result.current.agentResolutionState).toBe("failed");
    expect(result.current.agentResolutionError).toBe(
      "Lead sandbox did not become ready. Try opening the agent again.",
    );
    expect(onAgentNameConsumed).not.toHaveBeenCalled();
  });
});
