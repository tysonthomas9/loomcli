/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { ActiveIssueLookup } from "@/hooks/workspace";
import type { Issue, LoomAgentStatus, LoomTaskInfo } from "@/types";
import { RunningSection } from "../RunningSection";

const activeAgent: LoomAgentStatus = {
  name: "nova",
  branch: "agents/nova",
  status: "working: LOCALMODE-19 (5m)",
  ahead: 0,
  behind: 0,
};

const staleAgentTasks: Record<string, LoomTaskInfo> = {
  nova: {
    id: "LOCALMODE-19",
    title: "Deleted task",
  } as LoomTaskInfo,
};

let agents: LoomAgentStatus[] = [];
let agentTasks: Record<string, LoomTaskInfo> = {};
let epics: Array<{ epic: Issue; tasks: Issue[] }> = [];
let orphanTasks: Issue[] = [];
let treeLoading = false;
let directLookupResults = new Map<string, ActiveIssueLookup>();

vi.mock("zustand", () => ({
  useStore: (
    _store: unknown,
    selector: (state: {
      agents: LoomAgentStatus[];
      agentTasks: Record<string, LoomTaskInfo>;
    }) => unknown,
  ) => selector({ agents, agentTasks }),
}));

vi.mock("@/hooks", () => ({
  useActiveIssueLookups: () => ({ results: directLookupResults }),
  useAgentStoreInstance: () => ({}),
  useWorkspaceContext: () => ({
    workspace: { name: "Local Mode" },
    workspaceId: "LOCALMODE",
  }),
  useWorkspaceTree: () => ({
    epics,
    orphanTasks,
    closedEpicCount: 0,
    isLoading: treeLoading,
    error: null,
    refetch: vi.fn(),
  }),
}));

describe("RunningSection", () => {
  beforeEach(() => {
    agents = [activeAgent];
    agentTasks = staleAgentTasks;
    epics = [];
    orphanTasks = [];
    treeLoading = false;
    directLookupResults = new Map();
  });

  it("keeps an unresolved direct lookup unknown and non-clickable", () => {
    const onSelect = vi.fn();

    render(<RunningSection onSelect={onSelect} />);

    const row = screen.getByRole("button", {
      name: /Deleted task.*issue status unknown/i,
    });
    expect(row).toBeDisabled();
    expect(screen.queryByText(/issue unavailable/i)).toBeNull();
    fireEvent.click(row);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("renders an authoritative 404 as unavailable instead of a broken link", () => {
    directLookupResults.set("LOCALMODE-19", { status: "missing" });
    const onSelect = vi.fn();

    render(<RunningSection onSelect={onSelect} />);

    const row = screen.getByRole("button", {
      name: /Deleted task.*issue unavailable/i,
    });
    expect(row).toBeDisabled();
    fireEvent.click(row);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("renders and selects an active task that still exists", () => {
    orphanTasks = [
      {
        id: "LOCALMODE-19",
        title: "Existing task",
        issue_type: "task",
        status: "in_progress",
      } as Issue,
    ];
    const onSelect = vi.fn();

    render(<RunningSection onSelect={onSelect} />);

    fireEvent.click(screen.getByRole("button", { name: /Existing task/ }));
    expect(onSelect).toHaveBeenCalledWith("LOCALMODE-19");
  });

  it.each(["bug", "chore", "epic"] as const)(
    "directly resolves and selects an active %s excluded from the tree projection",
    (issueType) => {
      directLookupResults.set("LOCALMODE-19", {
        status: "found",
        issue: {
          id: "LOCALMODE-19",
          title: `Existing ${issueType}`,
          issue_type: issueType,
          status: "in_progress",
        } as Issue,
      });
      const onSelect = vi.fn();

      render(<RunningSection onSelect={onSelect} />);

      const row = screen.getByRole("button", {
        name: new RegExp(`Existing ${issueType}`, "i"),
      });
      expect(row).toBeEnabled();
      fireEvent.click(row);
      expect(onSelect).toHaveBeenCalledWith("LOCALMODE-19");
    },
  );

  it("does not mislabel a transient lookup failure as deletion", () => {
    directLookupResults.set("LOCALMODE-19", { status: "error" });

    render(<RunningSection onSelect={vi.fn()} />);

    const row = screen.getByRole("button", {
      name: /Deleted task.*issue status unknown/i,
    });
    expect(row).toBeDisabled();
    expect(screen.queryByText(/issue unavailable/i)).toBeNull();
  });
});
