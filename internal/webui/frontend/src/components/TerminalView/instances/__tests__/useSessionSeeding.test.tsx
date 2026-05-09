/**
 * @vitest-environment jsdom
 */
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type React from "react";

import { useSessionSeeding } from "../useSessionSeeding";
import type { TabState } from "@/components/TerminalView/tabs";

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
  it("repairs a restored agent tab before focusing it", () => {
    const existingTab: TabState = {
      id: "agent-lead-ui-e2e",
      label: "agent-lead-ui-e2e",
      sessionName: "agent-lead-ui-e2e",
      connectionState: "disconnected",
      backendName: "codex",
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

    expect(setTabs).toHaveBeenCalledTimes(1);
    const updater = setTabs.mock.calls[0][0] as (
      tabs: TabState[],
    ) => TabState[];
    expect(updater([existingTab])[0]).toMatchObject({
      backendName: "agent",
      agentName: "lead-ui-e2e",
    });
    expect(setActiveTabId).toHaveBeenCalledWith("agent-lead-ui-e2e");
    expect(onAgentNameConsumed).toHaveBeenCalledTimes(1);
  });
});
