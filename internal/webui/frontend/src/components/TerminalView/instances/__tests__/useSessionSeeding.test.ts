/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useSessionSeeding hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useSessionSeeding } from "../useSessionSeeding";
import type { TabState } from "@/components/TerminalView/tabs";
import { MAX_TABS } from "@/components/TerminalView/tabs";
import type { IssueContext } from "@/api/terminal";

// ── Mock @/api/terminal ────────────────────────────────────────────────────

const mockSeedTerminalSession = vi.fn();

vi.mock("@/api/terminal", () => ({
  seedTerminalSession: (...args: unknown[]) => mockSeedTerminalSession(...args),
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
  overrides: Partial<Parameters<typeof useSessionSeeding>[0]> = {},
) {
  const tabs: TabState[] = overrides.tabs ?? [];
  return {
    pendingIssueContext: undefined as IssueContext | undefined,
    onIssueContextConsumed: undefined as (() => void) | undefined,
    pendingAgentName: undefined as string | undefined,
    onAgentNameConsumed: undefined as (() => void) | undefined,
    tabs,
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    createTab: vi.fn().mockResolvedValue(undefined) as (
      session: string,
      label: string,
      sortOrder: number,
    ) => Promise<void>,
    config: { backend: "claude" } as { backend: string } | undefined,
    initializedRef: { current: true } as React.MutableRefObject<boolean>,
    tabsRef: { current: tabs } as React.MutableRefObject<TabState[]>,
    workspaceIdRef: { current: "ws-1" } as React.MutableRefObject<string>,
    ...overrides,
  };
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe("useSessionSeeding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockSeedTerminalSession.mockResolvedValue(undefined);
  });

  // 1. Issue context creates new tab when no match
  it("issue context creates new tab when no match", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const onIssueContextConsumed = vi.fn();

    const issueContext: IssueContext = {
      issue_id: "TEST-1",
      title: "Test issue",
    };

    renderHook(() =>
      useSessionSeeding(
        createOptions({
          pendingIssueContext: issueContext,
          onIssueContextConsumed,
          tabs: [],
          setTabs,
          setActiveTabId,
          createTab,
        }),
      ),
    );

    expect(setTabs).toHaveBeenCalledTimes(1);
    expect(setActiveTabId).toHaveBeenCalledWith("issue-TEST-1");
    expect(createTab).toHaveBeenCalledWith("issue-TEST-1", "issue-TEST-1", 0);
    expect(onIssueContextConsumed).toHaveBeenCalled();
  });

  // 2. Issue context switches to existing tab
  it("issue context switches to existing tab", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const onIssueContextConsumed = vi.fn();

    const existingTab = makeTab("issue-TEST-1", "issue-TEST-1");
    const issueContext: IssueContext = {
      issue_id: "TEST-1",
      title: "Test issue",
    };

    renderHook(() =>
      useSessionSeeding(
        createOptions({
          pendingIssueContext: issueContext,
          onIssueContextConsumed,
          tabs: [existingTab],
          setTabs,
          setActiveTabId,
          createTab,
        }),
      ),
    );

    expect(setActiveTabId).toHaveBeenCalledWith("issue-TEST-1");
    expect(setTabs).not.toHaveBeenCalled();
    expect(createTab).not.toHaveBeenCalled();
    expect(onIssueContextConsumed).toHaveBeenCalled();
  });

  // 3. Agent name creates new tab
  it("agent name creates new tab", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const onAgentNameConsumed = vi.fn();

    renderHook(() =>
      useSessionSeeding(
        createOptions({
          pendingAgentName: "coder",
          onAgentNameConsumed,
          tabs: [],
          setTabs,
          setActiveTabId,
          createTab,
        }),
      ),
    );

    expect(setTabs).toHaveBeenCalledTimes(1);
    expect(setActiveTabId).toHaveBeenCalledWith("agent-coder");
    expect(createTab).toHaveBeenCalledWith("agent-coder", "agent-coder", 0);
    expect(onAgentNameConsumed).toHaveBeenCalled();
  });

  // 4. Agent name respects MAX_TABS
  it("agent name respects MAX_TABS", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const onAgentNameConsumed = vi.fn();

    // Create MAX_TABS tabs
    const tabs = Array.from({ length: MAX_TABS }, (_, i) =>
      makeTab(`tab-${i}`),
    );

    renderHook(() =>
      useSessionSeeding(
        createOptions({
          pendingAgentName: "coder",
          onAgentNameConsumed,
          tabs,
          setTabs,
          setActiveTabId,
          createTab,
        }),
      ),
    );

    // Should not create a new tab
    expect(setTabs).not.toHaveBeenCalled();
    expect(createTab).not.toHaveBeenCalled();
    // Consumed callback should still be called
    expect(onAgentNameConsumed).toHaveBeenCalled();
  });

  // 5. trySeedOnConnect seeds on first connect
  it("trySeedOnConnect seeds on first connect", async () => {
    const issueContext: IssueContext = {
      issue_id: "SEED-1",
      title: "Seed issue",
    };

    const issueTab = makeTab("issue-SEED-1", "issue-SEED-1");
    // tabsRef must start empty so the effect populates pendingSeedRef
    const tabsRef = { current: [] as TabState[] } as React.MutableRefObject<
      TabState[]
    >;
    const workspaceIdRef = {
      current: "ws-1",
    } as React.MutableRefObject<string>;

    const setTabs = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const onIssueContextConsumed = vi.fn();

    const { result, rerender } = renderHook(
      (props) => useSessionSeeding(props),
      {
        initialProps: createOptions({
          pendingIssueContext: issueContext,
          onIssueContextConsumed,
          tabs: [],
          setTabs,
          createTab,
          tabsRef,
          workspaceIdRef,
        }),
      },
    );

    // After the effect runs, pendingSeedRef is populated and tab created.
    // Update tabsRef to include the new tab (simulating setTabs effect)
    tabsRef.current = [issueTab];

    // Re-render with consumed context — reuse same stable refs
    rerender(
      createOptions({
        pendingIssueContext: undefined,
        onIssueContextConsumed,
        tabs: [issueTab],
        setTabs,
        createTab,
        tabsRef,
        workspaceIdRef,
      }),
    );

    await act(async () => {
      result.current.trySeedOnConnect("issue-SEED-1");
      await new Promise((r) => setTimeout(r, 0));
    });

    expect(mockSeedTerminalSession).toHaveBeenCalledWith(
      "ws-1",
      "issue-SEED-1",
      issueContext,
    );
  });

  // 6. trySeedOnConnect skips already-seeded session
  it("trySeedOnConnect skips already-seeded session", async () => {
    const issueContext: IssueContext = {
      issue_id: "SEED-2",
      title: "Seed issue 2",
    };

    const issueTab = makeTab("issue-SEED-2", "issue-SEED-2");
    // tabsRef must start empty so the effect populates pendingSeedRef
    const tabsRef = { current: [] as TabState[] } as React.MutableRefObject<
      TabState[]
    >;
    const workspaceIdRef = {
      current: "ws-1",
    } as React.MutableRefObject<string>;

    const setTabs = vi.fn();
    const createTab = vi.fn().mockResolvedValue(undefined);
    const onIssueContextConsumed = vi.fn();

    const { result, rerender } = renderHook(
      (props) => useSessionSeeding(props),
      {
        initialProps: createOptions({
          pendingIssueContext: issueContext,
          onIssueContextConsumed,
          tabs: [],
          setTabs,
          createTab,
          tabsRef,
          workspaceIdRef,
        }),
      },
    );

    tabsRef.current = [issueTab];
    rerender(
      createOptions({
        pendingIssueContext: undefined,
        onIssueContextConsumed,
        tabs: [issueTab],
        setTabs,
        createTab,
        tabsRef,
        workspaceIdRef,
      }),
    );

    // First connect — seeds
    await act(async () => {
      result.current.trySeedOnConnect("issue-SEED-2");
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(mockSeedTerminalSession).toHaveBeenCalledTimes(1);

    // Second connect — should NOT seed again
    await act(async () => {
      result.current.trySeedOnConnect("issue-SEED-2");
      await new Promise((r) => setTimeout(r, 0));
    });
    expect(mockSeedTerminalSession).toHaveBeenCalledTimes(1);
  });

  // 7. trySeedOnConnect skips if no pending context
  it("trySeedOnConnect skips if no pending context", () => {
    const tab = makeTab("tab-1");
    const tabsRef = { current: [tab] } as React.MutableRefObject<TabState[]>;

    const { result } = renderHook(() =>
      useSessionSeeding(
        createOptions({
          tabs: [tab],
          tabsRef,
        }),
      ),
    );

    act(() => {
      result.current.trySeedOnConnect("tab-1");
    });

    expect(mockSeedTerminalSession).not.toHaveBeenCalled();
  });

  // 8. trySeedOnConnect handles missing tab gracefully
  it("trySeedOnConnect handles missing tab gracefully", () => {
    const tabsRef = { current: [] } as React.MutableRefObject<TabState[]>;

    const { result } = renderHook(() =>
      useSessionSeeding(
        createOptions({
          tabs: [],
          tabsRef,
        }),
      ),
    );

    // Should not throw
    act(() => {
      result.current.trySeedOnConnect("non-existent");
    });

    expect(mockSeedTerminalSession).not.toHaveBeenCalled();
  });

  it("does nothing when not initialized", () => {
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();

    renderHook(() =>
      useSessionSeeding(
        createOptions({
          pendingIssueContext: {
            issue_id: "TEST-1",
            title: "Test",
          },
          tabs: [],
          setTabs,
          setActiveTabId,
          initializedRef: { current: false },
        }),
      ),
    );

    expect(setTabs).not.toHaveBeenCalled();
    expect(setActiveTabId).not.toHaveBeenCalled();
  });
});
