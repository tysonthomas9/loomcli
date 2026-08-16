/**
 * @vitest-environment jsdom
 */
import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type React from "react";

import { useTabInit } from "../useTabInit";
import type { TabMetadata } from "@/api/terminal";
import type { BackendConfigData } from "@/api/common";
import type { TabState } from "../terminalTabUtils";

function createArgs(overrides: Partial<Parameters<typeof useTabInit>[0]> = {}) {
  return {
    tabMetadata: [] as TabMetadata[],
    metaLoading: false,
    config: undefined as BackendConfigData | undefined,
    configLoading: false,
    createTab: vi.fn().mockResolvedValue(undefined),
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    initializedRef: { current: false } as React.MutableRefObject<boolean>,
    isViewActive: true,
    ...overrides,
  };
}

describe("useTabInit", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
  });

  it("does not initialize when metaLoading is true", () => {
    const args = createArgs({ metaLoading: true });

    renderHook(() => useTabInit(args));

    expect(args.initializedRef.current).toBe(false);
    expect(args.setTabs).not.toHaveBeenCalled();
  });

  it("does not initialize when configLoading is true", () => {
    const args = createArgs({ configLoading: true });

    renderHook(() => useTabInit(args));

    expect(args.initializedRef.current).toBe(false);
    expect(args.setTabs).not.toHaveBeenCalled();
  });

  it("does not initialize when isViewActive is false", () => {
    const args = createArgs({ isViewActive: false });

    renderHook(() => useTabInit(args));

    expect(args.initializedRef.current).toBe(false);
    expect(args.setTabs).not.toHaveBeenCalled();
  });

  it("does not re-initialize when already initialized", () => {
    const args = createArgs({
      initializedRef: { current: true } as React.MutableRefObject<boolean>,
    });

    renderHook(() => useTabInit(args));

    expect(args.setTabs).not.toHaveBeenCalled();
  });

  it("merges server metadata into already initialized tabs", () => {
    let tabs: TabState[] = [
      {
        id: "lead-codex-1",
        label: "Old label",
        sessionName: "lead-codex-1",
        connectionState: "connected",
        backendName: "codex",
      },
    ];
    let activeTabId = "lead-codex-1";
    const setTabs = vi.fn((update: React.SetStateAction<TabState[]>) => {
      tabs = typeof update === "function" ? update(tabs) : update;
    });
    const setActiveTabId = vi.fn((update: React.SetStateAction<string>) => {
      activeTabId = typeof update === "function" ? update(activeTabId) : update;
    });
    const metadata: TabMetadata[] = [
      {
        session_name: "lead-codex-1",
        label: "lead-codex-1",
        notes: "",
        sort_order: 0,
        pinned: false,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
      {
        session_name: "lead-codex-2",
        label: "lead-codex-2",
        notes: "",
        sort_order: 1,
        pinned: false,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
    ];
    const args = createArgs({
      initializedRef: { current: true } as React.MutableRefObject<boolean>,
      tabMetadata: metadata,
      config: {
        backend: "codex",
        source: "config",
        available: ["codex"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
    });

    renderHook(() => useTabInit(args));

    expect(setTabs).toHaveBeenCalledTimes(1);
    expect(tabs).toHaveLength(2);
    expect(tabs[0]).toMatchObject({
      sessionName: "lead-codex-1",
      label: "lead-codex-1",
      connectionState: "connected",
      backendName: "codex",
    });
    expect(tabs[1]).toMatchObject({
      sessionName: "lead-codex-2",
      label: "lead-codex-2",
      connectionState: "disconnected",
      backendName: "codex",
    });
    expect(activeTabId).toBe("lead-codex-1");
  });

  it("restores tabs from tabMetadata sorted by sort_order", () => {
    const metadata: TabMetadata[] = [
      {
        session_name: "lead-claude-2",
        label: "Claude 2",
        notes: "",
        sort_order: 2,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
      {
        session_name: "lead-claude-1",
        label: "Claude 1",
        notes: "",
        sort_order: 1,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
    ];

    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      config: {
        backend: "claude",
        source: "config",
        available: ["claude"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
    });

    renderHook(() => useTabInit(args));

    expect(args.initializedRef.current).toBe(true);
    expect(setTabs).toHaveBeenCalledTimes(1);
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(2);
    // Should be sorted: sort_order 1 first
    expect(tabs[0].sessionName).toBe("lead-claude-1");
    expect(tabs[1].sessionName).toBe("lead-claude-2");
  });

  it("restores active tab from sessionStorage when available", () => {
    sessionStorage.setItem("terminal-active-tab", "lead-claude-2");

    const metadata: TabMetadata[] = [
      {
        session_name: "lead-claude-1",
        label: "Claude 1",
        notes: "",
        sort_order: 1,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
      {
        session_name: "lead-claude-2",
        label: "Claude 2",
        notes: "",
        sort_order: 2,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
    ];

    const setActiveTabId = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
    });

    renderHook(() => useTabInit(args));

    expect(setActiveTabId).toHaveBeenCalledWith("lead-claude-2");
  });

  it("falls back to first tab when saved active tab not found", () => {
    sessionStorage.setItem("terminal-active-tab", "nonexistent");

    const metadata: TabMetadata[] = [
      {
        session_name: "lead-claude-1",
        label: "Claude 1",
        notes: "",
        sort_order: 1,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
    ];

    const setActiveTabId = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
    });

    renderHook(() => useTabInit(args));

    expect(setActiveTabId).toHaveBeenCalledWith("lead-claude-1");
  });

  it("sets empty tabs when no metadata and no backends", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);

    const args = createArgs({
      config: {
        backend: "claude",
        source: "config",
        available: [],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      createTab,
    });

    renderHook(() => useTabInit(args));

    expect(setTabs).toHaveBeenCalledTimes(1);
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(0);
    expect(setActiveTabId).toHaveBeenCalledWith("");
    expect(createTab).not.toHaveBeenCalled();
  });

  it("creates only default backend tab when no metadata exists", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);

    const args = createArgs({
      config: {
        backend: "claude",
        source: "config",
        available: ["claude", "codex"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      createTab,
    });

    renderHook(() => useTabInit(args));

    expect(setTabs).toHaveBeenCalledTimes(1);
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(1);
    expect(tabs[0].sessionName).toBe("lead-claude-1");
    expect(tabs[0].backendName).toBe("claude");

    // Should set default backend tab as active
    expect(setActiveTabId).toHaveBeenCalledWith("lead-claude-1");

    // Should persist only the one tab
    expect(createTab).toHaveBeenCalledTimes(1);
  });

  it("sanitizes workspace prefix for auto-created tab sessions", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);

    const args = createArgs({
      workspace: "FleetDB Regression Fixture",
      config: {
        backend: "claude",
        source: "config",
        available: ["claude"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      createTab,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs[0].sessionName).toBe("FleetDBRegressionFixture--lead-claude-1");
    expect(setActiveTabId).toHaveBeenCalledWith(
      "FleetDBRegressionFixture--lead-claude-1",
    );
    expect(createTab).toHaveBeenCalledWith(
      "FleetDBRegressionFixture--lead-claude-1",
      "lead-claude-1",
      0,
    );
  });

  it("creates tab for configured default backend", () => {
    const setActiveTabId = vi.fn();
    const setTabs = vi.fn();

    const args = createArgs({
      config: {
        backend: "codex",
        source: "config",
        available: ["codex", "claude"],
        agents: [],
      },
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
    });

    renderHook(() => useTabInit(args));

    // Should create only the configured default backend tab
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(1);
    expect(tabs[0].backendName).toBe("codex");
    expect(setActiveTabId).toHaveBeenCalledWith("lead-codex-1");
  });

  it("falls back to first available backend when default not in list", () => {
    const setActiveTabId = vi.fn();
    const setTabs = vi.fn();

    const args = createArgs({
      config: {
        backend: "gemini",
        source: "config",
        available: ["codex", "opencode"],
        agents: [],
      },
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(1);
    expect(tabs[0].backendName).toBe("codex");
    expect(setActiveTabId).toHaveBeenCalledWith("lead-codex-1");
  });

  it("sets all restored tabs to disconnected state", () => {
    const metadata: TabMetadata[] = [
      {
        session_name: "lead-claude-1",
        label: "Claude 1",
        notes: "",
        sort_order: 1,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
    ];

    const setTabs = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs[0].connectionState).toBe("disconnected");
  });

  it("restores persisted agent tabs with agent routing metadata", () => {
    const metadata: TabMetadata[] = [
      {
        session_name: "term_550e8400-e29b-41d4-a716-446655440000",
        label: "agent-lead-ui-e2e",
        notes: "",
        sort_order: 1,
        pinned: false,
        kind: "agent",
        agent_id: "lead-ui-e2e",
        role: "lead",
        backend: "codex",
        writable: true,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
        attachable: true,
        attached_clients: 0,
      },
    ];

    const setTabs = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      config: {
        backend: "codex",
        source: "config",
        available: ["codex"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs[0]).toMatchObject({
      sessionName: "term_550e8400-e29b-41d4-a716-446655440000",
      backendName: "codex",
      agentName: "lead-ui-e2e",
      writable: true,
    });
  });

  it("excludes agent tabs when excludeAgentTabs is true", () => {
    const metadata: TabMetadata[] = [
      {
        session_name: "term_agent_session",
        label: "agent-local-planner",
        notes: "",
        sort_order: 0,
        pinned: false,
        kind: "agent",
        agent_id: "local-planner",
        role: "plan",
        backend: "codex",
        writable: true,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
        attachable: true,
        attached_clients: 0,
      },
      {
        session_name: "lead-codex-1",
        label: "lead-codex-1",
        notes: "",
        sort_order: 1,
        pinned: false,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
        attachable: true,
        attached_clients: 0,
      },
    ];

    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      excludeAgentTabs: true,
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(1);
    expect(tabs[0]?.sessionName).toBe("lead-codex-1");
    expect(setActiveTabId).toHaveBeenCalledWith("lead-codex-1");
  });

  it("auto-creates lead tab when metadata is agent-only and excludeAgentTabs is true", () => {
    const metadata: TabMetadata[] = [
      {
        session_name: "term_agent_session",
        label: "agent-local-planner",
        notes: "",
        sort_order: 0,
        pinned: false,
        kind: "agent",
        agent_id: "local-planner",
        role: "plan",
        backend: "codex",
        writable: true,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
        attachable: true,
        attached_clients: 0,
      },
    ];

    const setTabs = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const args = createArgs({
      tabMetadata: metadata,
      excludeAgentTabs: true,
      config: {
        backend: "codex",
        source: "config",
        available: ["codex"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      createTab,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(1);
    expect(tabs[0]?.label).toBe("lead-codex-1");
    expect(createTab).toHaveBeenCalled();
  });

  it("filters shell from auto-created backends", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);

    const args = createArgs({
      config: {
        backend: "claude",
        source: "config",
        available: ["claude", "codex", "shell"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      createTab,
    });

    renderHook(() => useTabInit(args));

    expect(setTabs).toHaveBeenCalledTimes(1);
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    // Shell should be filtered out — only default backend tab created
    expect(tabs).toHaveLength(1);
    expect(tabs[0].sessionName).toBe("lead-claude-1");
    expect(tabs[0].backendName).toBe("claude");

    // No shell tab should have been created
    const shellTab = tabs.find((t) => t.backendName === "shell");
    expect(shellTab).toBeUndefined();

    // Only 1 tab persisted
    expect(createTab).toHaveBeenCalledTimes(1);
  });

  it("sets empty tabs when only shell is available", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);

    const args = createArgs({
      config: {
        backend: "claude",
        source: "config",
        available: ["shell"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      createTab,
    });

    renderHook(() => useTabInit(args));

    expect(setTabs).toHaveBeenCalledTimes(1);
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    // When all backends are filtered (only "shell"), show empty state
    expect(tabs).toHaveLength(0);
    expect(setActiveTabId).toHaveBeenCalledWith("");
    expect(createTab).not.toHaveBeenCalled();
  });

  it("initializes when isViewActive becomes true", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const initializedRef = {
      current: false,
    } as React.MutableRefObject<boolean>;

    const args = createArgs({
      config: {
        backend: "claude",
        source: "config",
        available: ["claude", "codex"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
      setActiveTabId: setActiveTabId as unknown as React.Dispatch<
        React.SetStateAction<string>
      >,
      createTab,
      initializedRef,
      isViewActive: false,
    });

    const { rerender } = renderHook((a) => useTabInit(a), {
      initialProps: args,
    });

    // Not initialized yet — view is inactive
    expect(initializedRef.current).toBe(false);
    expect(setTabs).not.toHaveBeenCalled();

    // Activate the view
    rerender({ ...args, isViewActive: true });

    // Now should have initialized with single default backend tab
    expect(initializedRef.current).toBe(true);
    expect(setTabs).toHaveBeenCalledTimes(1);
    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs).toHaveLength(1);
    expect(tabs[0].sessionName).toBe("lead-claude-1");
    expect(tabs[0].backendName).toBe("claude");
    expect(setActiveTabId).toHaveBeenCalledWith("lead-claude-1");
    expect(createTab).toHaveBeenCalledTimes(1);
  });

  it("extracts backend name from session name pattern", () => {
    const metadata: TabMetadata[] = [
      {
        session_name: "lead-codex-3",
        label: "Codex 3",
        notes: "",
        sort_order: 1,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
    ];

    const setTabs = vi.fn();
    const args = createArgs({
      tabMetadata: metadata,
      config: {
        backend: "claude",
        source: "config",
        available: ["claude"],
        agents: [],
      },
      setTabs: setTabs as unknown as React.Dispatch<
        React.SetStateAction<TabState[]>
      >,
    });

    renderHook(() => useTabInit(args));

    const tabs = setTabs.mock.calls[0][0] as TabState[];
    expect(tabs[0].backendName).toBe("codex");
  });
});
