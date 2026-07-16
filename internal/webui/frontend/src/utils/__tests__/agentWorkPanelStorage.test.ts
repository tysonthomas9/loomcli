/**
 * @vitest-environment jsdom
 */

import { describe, expect, it, beforeEach } from "vitest";

import { wsKey } from "@/utils/scopedStorage";

import {
  DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
  loadAgentWorkPanelView,
  saveAgentWorkPanelView,
} from "../agentWorkPanelStorage";

const WS_ID = "ws-agent-work-panel";
const AGENT_NAME = "lead-1";

describe("agentWorkPanelStorage", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns defaults when nothing is stored", () => {
    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME)).toEqual(
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
    );
  });

  it("returns defaults when workspace or agent is missing", () => {
    saveAgentWorkPanelView(WS_ID, AGENT_NAME, {
      statusFilter: "open",
      leadFilter: "running",
      taskSearch: "hello",
      expandedEpics: { "EPIC-1": true },
    });

    expect(loadAgentWorkPanelView(null, AGENT_NAME)).toEqual(
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
    );
    expect(loadAgentWorkPanelView(WS_ID, undefined)).toEqual(
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
    );
  });

  it("round-trips view state", () => {
    const state = {
      statusFilter: "review" as const,
      leadFilter: "idle" as const,
      taskSearch: "hello world",
      expandedEpics: { "EPIC-1": true, "EPIC-2": false },
      selectedTaskId: null,
    };

    saveAgentWorkPanelView(WS_ID, AGENT_NAME, state);
    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME)).toEqual(state);
    expect(
      localStorage.getItem(wsKey(WS_ID, `agent-work-panel-view:${AGENT_NAME}`)),
    ).toBe(JSON.stringify(state));
  });

  it("isolates state per agent within a workspace", () => {
    saveAgentWorkPanelView(WS_ID, "lead-1", {
      ...DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
      taskSearch: "lead one",
    });
    saveAgentWorkPanelView(WS_ID, "lead-2", {
      ...DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
      taskSearch: "lead two",
    });

    expect(loadAgentWorkPanelView(WS_ID, "lead-1").taskSearch).toBe("lead one");
    expect(loadAgentWorkPanelView(WS_ID, "lead-2").taskSearch).toBe("lead two");
  });

  it("falls back to defaults for invalid JSON", () => {
    localStorage.setItem(
      wsKey(WS_ID, `agent-work-panel-view:${AGENT_NAME}`),
      "{not-json",
    );

    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME)).toEqual(
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
    );
  });

  it("falls back to defaults for invalid filter values", () => {
    localStorage.setItem(
      wsKey(WS_ID, `agent-work-panel-view:${AGENT_NAME}`),
      JSON.stringify({
        statusFilter: "not-a-bucket",
        leadFilter: "running",
        taskSearch: "hello",
        expandedEpics: {},
      }),
    );

    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME)).toEqual(
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
    );
  });

  it("ignores invalid expandedEpics entries", () => {
    localStorage.setItem(
      wsKey(WS_ID, `agent-work-panel-view:${AGENT_NAME}`),
      JSON.stringify({
        statusFilter: "all",
        leadFilter: "all",
        taskSearch: "",
        expandedEpics: { "EPIC-1": true, "EPIC-2": "not-boolean" },
      }),
    );

    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME).expandedEpics).toEqual({
      "EPIC-1": true,
    });
  });

  it("no-ops save when workspace or agent is missing", () => {
    saveAgentWorkPanelView(undefined, AGENT_NAME, {
      ...DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
      taskSearch: "ignored",
    });
    saveAgentWorkPanelView(WS_ID, undefined, {
      ...DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
      taskSearch: "ignored",
    });

    expect(localStorage.length).toBe(0);
  });

  it("merges partial saves without clobbering other fields", () => {
    saveAgentWorkPanelView(WS_ID, AGENT_NAME, {
      taskSearch: "hello",
      selectedTaskId: "TASK-1",
    });
    saveAgentWorkPanelView(WS_ID, AGENT_NAME, {
      leadFilter: "running",
    });

    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME)).toEqual({
      ...DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
      taskSearch: "hello",
      selectedTaskId: "TASK-1",
      leadFilter: "running",
    });
  });

  it("round-trips selectedTaskId", () => {
    saveAgentWorkPanelView(WS_ID, AGENT_NAME, {
      selectedTaskId: "HELLO-WORLD-1",
    });

    expect(loadAgentWorkPanelView(WS_ID, AGENT_NAME).selectedTaskId).toBe(
      "HELLO-WORLD-1",
    );
  });
});
