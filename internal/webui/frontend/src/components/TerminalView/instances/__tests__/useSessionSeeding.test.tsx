/**
 * @vitest-environment jsdom
 */
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
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
  it("focuses a restored agent tab without resolving a new session", () => {
    const existingTab: TabState = {
      id: "term_123",
      label: "agent-lead-ui-e2e",
      sessionName: "term_123",
      connectionState: "disconnected",
      backendName: "codex",
      kind: "agent",
      agentName: "lead-ui-e2e",
    };
    const setTabs = vi.fn();
    const setActiveTabId = vi.fn();
    const onAgentNameConsumed = vi.fn();

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

    expect(setTabs).not.toHaveBeenCalled();
    expect(setActiveTabId).toHaveBeenCalledWith("term_123");
    expect(onAgentNameConsumed).toHaveBeenCalledTimes(1);
    expect(mockHooksApi.ensureAgentTerminalSession).not.toHaveBeenCalled();
  });
});
