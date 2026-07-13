/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { wsKey } from "@/utils/scopedStorage";

import { useAgentWorkPanelViewState } from "../useAgentWorkPanelViewState";

const WS_ID = "ws-agent-work-panel-view";
const AGENT_A = "lead-1";
const AGENT_B = "lead-2";

describe("useAgentWorkPanelViewState", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("restores persisted state for the selected agent", () => {
    localStorage.setItem(
      wsKey(WS_ID, `agent-work-panel-view:${AGENT_A}`),
      JSON.stringify({
        statusFilter: "open",
        leadFilter: "running",
        taskSearch: "hello",
        expandedEpics: { "EPIC-1": true },
      }),
    );

    const { result } = renderHook(() =>
      useAgentWorkPanelViewState(WS_ID, AGENT_A),
    );

    expect(result.current.statusFilter).toBe("open");
    expect(result.current.leadFilter).toBe("running");
    expect(result.current.taskSearch).toBe("hello");
    expect(result.current.expandedEpics).toEqual({ "EPIC-1": true });
  });

  it("persists updates debounced and flushes on unmount", () => {
    const { result, unmount } = renderHook(() =>
      useAgentWorkPanelViewState(WS_ID, AGENT_A),
    );

    act(() => {
      result.current.setTaskSearch("persist me");
    });

    expect(
      localStorage.getItem(wsKey(WS_ID, `agent-work-panel-view:${AGENT_A}`)),
    ).toBeNull();

    act(() => {
      vi.runAllTimers();
    });

    expect(
      localStorage.getItem(wsKey(WS_ID, `agent-work-panel-view:${AGENT_A}`)),
    ).toContain('"taskSearch":"persist me"');

    act(() => {
      result.current.setLeadFilter("idle");
    });
    unmount();

    expect(
      localStorage.getItem(wsKey(WS_ID, `agent-work-panel-view:${AGENT_A}`)),
    ).toContain('"leadFilter":"idle"');
  });

  it("loads a different agent's state when agentName changes", () => {
    localStorage.setItem(
      wsKey(WS_ID, `agent-work-panel-view:${AGENT_A}`),
      JSON.stringify({
        statusFilter: "all",
        leadFilter: "all",
        taskSearch: "agent-a",
        expandedEpics: {},
      }),
    );
    localStorage.setItem(
      wsKey(WS_ID, `agent-work-panel-view:${AGENT_B}`),
      JSON.stringify({
        statusFilter: "all",
        leadFilter: "all",
        taskSearch: "agent-b",
        expandedEpics: {},
      }),
    );

    const { result, rerender } = renderHook(
      ({ agentName }: { agentName: string }) =>
        useAgentWorkPanelViewState(WS_ID, agentName),
      { initialProps: { agentName: AGENT_A } },
    );

    expect(result.current.taskSearch).toBe("agent-a");

    rerender({ agentName: AGENT_B });
    expect(result.current.taskSearch).toBe("agent-b");
  });
});
