/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentSection (sidebar redesign — loomcli-8uy0o).
 * Verifies the inline two-line row replaces AgentCard chrome.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { AgentSection } from "../AgentSection";

const defaultAgentStoreState = {
  agents: [] as Array<{
    name: string;
    branch: string;
    status: string;
    ahead: number;
    behind: number;
    repo?: string;
    cross_repo?: boolean;
    role?: string;
  }>,
};
let agentStoreOverride: Partial<typeof defaultAgentStoreState> = {};

const defaultWorkspaceContext = {
  agents: [] as Array<{ name: string; cross_repo?: boolean; repos?: string[] }>,
  workspace: { name: "fixture", id: "fixture-ws", workspaces: [] },
};
let workspaceContextOverride: Partial<typeof defaultWorkspaceContext> = {};

vi.mock("zustand", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useStore: (_store: unknown, selector: (s: any) => unknown) =>
    selector({ ...defaultAgentStoreState, ...agentStoreOverride }),
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => ({}),
  useWorkspaceContext: () => ({
    ...defaultWorkspaceContext,
    ...workspaceContextOverride,
  }),
  useAgentDiffStat: () => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
}));

describe("AgentSection", () => {
  beforeEach(() => {
    agentStoreOverride = {};
    workspaceContextOverride = {};
  });

  it("renders the Agents header even when no agents are present", () => {
    render(<AgentSection />);
    expect(screen.getByText("Agents")).toBeInTheDocument();
  });

  it("renders the + Add agent button when onAddClick is provided", () => {
    const onAddClick = vi.fn();
    render(<AgentSection onAddClick={onAddClick} />);

    const button = screen.getByRole("button", { name: /\+ Add agent/i });
    fireEvent.click(button);
    expect(onAddClick).toHaveBeenCalledTimes(1);
  });

  it("renders agent name and scope line, no role subtitle, no repo badge", () => {
    agentStoreOverride = {
      agents: [
        {
          name: "alpha",
          branch: "feature-x",
          status: "working",
          ahead: 0,
          behind: 0,
          repo: "loomcli",
          role: "task",
        },
      ],
    };

    render(<AgentSection />);

    // Name + scope line (repo · branch)
    expect(screen.getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText(/loomcli/)).toBeInTheDocument();
    expect(screen.getByText(/feature-x/)).toBeInTheDocument();

    // No "Agent" role subtitle and no "Task" role label
    expect(screen.queryByText(/^Agent$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Task$/)).not.toBeInTheDocument();
  });

  it("renders 'workspace' as the scope when agent is cross_repo", () => {
    agentStoreOverride = {
      agents: [
        {
          name: "sentinel",
          branch: "",
          status: "ready",
          ahead: 0,
          behind: 0,
          cross_repo: true,
        },
      ],
    };

    render(<AgentSection />);

    expect(screen.getByText("workspace")).toBeInTheDocument();
  });

  it("status label reflects parseLoomStatus output", () => {
    agentStoreOverride = {
      agents: [
        {
          name: "alpha",
          branch: "main",
          status: "working",
          ahead: 0,
          behind: 0,
        },
        {
          name: "bravo",
          branch: "main",
          status: "ready",
          ahead: 0,
          behind: 0,
        },
        {
          name: "charlie",
          branch: "main",
          status: "error: rebase failed",
          ahead: 0,
          behind: 0,
        },
      ],
    };

    render(<AgentSection />);

    expect(screen.getByText("Working")).toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("Error")).toBeInTheDocument();
  });

  it("clicking the row calls onAgentClick with the agent name", () => {
    agentStoreOverride = {
      agents: [
        {
          name: "alpha",
          branch: "main",
          status: "ready",
          ahead: 0,
          behind: 0,
        },
      ],
    };
    const onAgentClick = vi.fn();

    render(<AgentSection onAgentClick={onAgentClick} />);

    const row = screen.getByRole("button", { name: /Agent: alpha/i });
    fireEvent.click(row);

    expect(onAgentClick).toHaveBeenCalledWith("alpha");
  });

  it("Enter/Space on the row triggers onAgentClick", () => {
    agentStoreOverride = {
      agents: [
        {
          name: "alpha",
          branch: "main",
          status: "ready",
          ahead: 0,
          behind: 0,
        },
      ],
    };
    const onAgentClick = vi.fn();

    render(<AgentSection onAgentClick={onAgentClick} />);

    const row = screen.getByRole("button", { name: /Agent: alpha/i });
    fireEvent.keyDown(row, { key: "Enter" });
    fireEvent.keyDown(row, { key: " " });

    expect(onAgentClick).toHaveBeenCalledTimes(2);
    expect(onAgentClick).toHaveBeenCalledWith("alpha");
  });
});
