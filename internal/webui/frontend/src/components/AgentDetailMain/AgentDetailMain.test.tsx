/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { describe, expect, it, vi } from "vitest";

import { createAgentStore } from "@/stores/agentStore";
import type { LoomAgentStatus } from "@/types";

import { AgentDetailMain } from "./AgentDetailMain";

const mocks = vi.hoisted(() => ({
  useAgentStoreInstance: vi.fn(),
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: mocks.useAgentStoreInstance,
}));

vi.mock("@/components/TerminalView", () => ({
  TerminalView: () => <div data-testid="terminal-view" />,
}));

function completedWorkerAgent(): LoomAgentStatus {
  return {
    name: "worker-e2e-2-a1",
    branch: "worker/e2e-2-a1",
    status: "done",
    ahead: 0,
    behind: 0,
    workspace: "E2E",
    parent: "E2E-1",
    task_id: "E2E-2",
    session_id: "sess-123",
    mode: "ephemeral",
    desired_state: "stopped",
    state: "stopped",
  };
}

function activeWorkerAgent(): LoomAgentStatus {
  return {
    name: "worker-e2e-2-live",
    branch: "worker/e2e-2-live",
    status: "working: E2E-2",
    ahead: 0,
    behind: 0,
    workspace: "E2E",
    parent: "E2E-1",
    task_id: "E2E-2",
    session_id: "sess-live",
    mode: "ephemeral",
    desired_state: "running",
    state: "active",
  };
}

function renderWithAgents(agents: LoomAgentStatus[], agentName: string) {
  const store = createAgentStore();
  store.setState({ agents });
  mocks.useAgentStoreInstance.mockReturnValue(store);
  return render(<AgentDetailMain agentName={agentName} />);
}

describe("AgentDetailMain", () => {
  it("shows assigned lead epic and hides placeholder branch values", () => {
    renderWithAgents(
      [
        {
          name: "nova",
          branch: "unknown",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "E2E",
          role: "lead",
          parent: "E2E-1",
          state: "idle",
        } as LoomAgentStatus,
      ],
      "nova",
    );

    expect(screen.getByText("nova")).toBeInTheDocument();
    expect(screen.queryByText("idle")).not.toBeInTheDocument();
    expect(screen.getByText("Lead")).toBeInTheDocument();
    expect(screen.getByText("Assigned epic")).toBeInTheDocument();
    expect(screen.getByText("E2E-1")).toBeInTheDocument();
    expect(screen.queryByText("unknown")).not.toBeInTheDocument();
  });

  it("shows pending lead delivery as pending context", () => {
    renderWithAgents(
      [
        {
          name: "nova",
          branch: "unknown",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "E2E",
          role: "lead",
          parent: "E2E-1",
          delivery_state: "pending",
          state: "idle",
        } as LoomAgentStatus,
      ],
      "nova",
    );

    expect(screen.getByText("Assigned epic")).toBeInTheDocument();
    expect(screen.getByText("E2E-1")).toBeInTheDocument();
    expect(screen.getByText("context pending")).toBeInTheDocument();
  });

  it("resolves the terminal for a stopped assigned lead", () => {
    renderWithAgents(
      [
        {
          name: "nova",
          branch: "unknown",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "E2E",
          role: "lead",
          parent: "E2E-1",
          delivery_state: "pending",
          state: "idle",
          desired_state: "stopped",
        } as LoomAgentStatus,
      ],
      "nova",
    );

    expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    expect(screen.queryByText("Agent is stopped")).not.toBeInTheDocument();
  });

  it("resolves the terminal for a stopped unassigned lead", () => {
    renderWithAgents(
      [
        {
          name: "atlas",
          branch: "unknown",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "E2E",
          role: "lead",
          state: "idle",
          desired_state: "stopped",
        } as LoomAgentStatus,
      ],
      "atlas",
    );

    expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    expect(screen.queryByText("Agent is stopped")).not.toBeInTheDocument();
  });

  it("capitalizes the agent name in the detail header", () => {
    renderWithAgents(
      [
        {
          name: "lead-b",
          branch: "unknown",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "E2E",
          role: "lead",
          state: "idle",
        } as LoomAgentStatus,
      ],
      "lead-b",
    );

    expect(screen.getByText("lead-b")).toHaveStyle({
      textTransform: "capitalize",
    });
  });

  it("makes an unassigned lead explicit instead of showing unknown", () => {
    renderWithAgents(
      [
        {
          name: "atlas",
          branch: "unknown",
          status: "idle",
          ahead: 0,
          behind: 0,
          workspace: "E2E",
          role: "lead",
          state: "idle",
        } as LoomAgentStatus,
      ],
      "atlas",
    );

    expect(screen.getByText("No epic assigned")).toBeInTheDocument();
    expect(screen.queryByText("unknown")).not.toBeInTheDocument();
  });

  it("keeps completed ephemeral worker artifacts and cleanup actions visible", () => {
    const agent = completedWorkerAgent();

    renderWithAgents([agent], agent.name);

    expect(screen.getByText("Ephemeral worker attempt")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open logs" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open transcript" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open diff" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete worktree" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Archive artifacts" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Rerun task" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("terminal-view")).not.toBeInTheDocument();
  });

  it("does not attach a terminal for active daemon-owned ephemeral workers", () => {
    const agent = activeWorkerAgent();

    renderWithAgents([agent], agent.name);

    expect(screen.getByText("Ephemeral worker attempt")).toBeInTheDocument();
    expect(
      screen.getByText(/daemon-owned ephemeral worker is already running/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open logs" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("terminal-view")).not.toBeInTheDocument();
  });
});
