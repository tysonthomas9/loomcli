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

function renderWithAgents(agents: LoomAgentStatus[], agentName: string) {
  const store = createAgentStore();
  store.setState({ agents });
  mocks.useAgentStoreInstance.mockReturnValue(store);
  return render(<AgentDetailMain agentName={agentName} />);
}

describe("AgentDetailMain", () => {
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
});
