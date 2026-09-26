/**
 * @vitest-environment jsdom
 */
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { StrictMode, type ReactNode } from "react";
import type React from "react";

import { useSessionSeeding } from "../useSessionSeeding";
import type { TabState } from "@/components/TerminalView/tabs";

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
      attachable: true,
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
      attachable: true,
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
});
