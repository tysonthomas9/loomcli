/**
 * @vitest-environment jsdom
 */

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import "@testing-library/jest-dom";
import { StrictMode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createAgentStore } from "@/stores/agentStore";
import { type LoomAgentStatus } from "@/types";

import { AgentDetailMain, AgentLifecycleControls } from "./AgentDetailMain";

const mocks = vi.hoisted(() => ({
  useAgentStoreInstance: vi.fn(),
  restartAgent: vi.fn(),
  showToast: vi.fn(),
  startAgent: vi.fn(),
  stopAgent: vi.fn(),
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: mocks.useAgentStoreInstance,
}));

vi.mock("@/hooks/api", () => ({
  restartAgent: mocks.restartAgent,
  startAgent: mocks.startAgent,
  stopAgent: mocks.stopAgent,
  wsUrl: (workspace: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspace)}${path}`,
}));

vi.mock("@/hooks/ui/useToast", () => ({
  useToast: () => ({
    dismissAll: vi.fn(),
    dismissToast: vi.fn(),
    showToast: mocks.showToast,
    toasts: [],
  }),
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

function activeServiceWorkerAgent(): LoomAgentStatus {
  return {
    name: "advanced-task-runner",
    branch: "worker/advanced-task-runner",
    status: "working: E2E-2",
    ahead: 0,
    behind: 0,
    workspace: "E2E",
    role: "task",
    role_kind: "worker",
    mode: "service",
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
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    mocks.restartAgent.mockResolvedValue({
      message: "agent restarted",
      status: "succeeded",
    });
    mocks.startAgent.mockResolvedValue({
      message: "agent started",
      status: "succeeded",
    });
    mocks.stopAgent.mockResolvedValue({
      message: "agent stopped",
      status: "succeeded",
    });
  });

  it("shows two-letter initials in the agent header avatar", () => {
    renderWithAgents(
      [
        {
          name: "lead-b",
          branch: "main",
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

    expect(
      screen.getByText("LB", { selector: "[aria-hidden='true']" }),
    ).toBeInTheDocument();
  });

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

  it("does not attach a terminal for active execution-owned ephemeral workers", () => {
    const agent = activeWorkerAgent();

    renderWithAgents([agent], agent.name);

    expect(screen.getByText("Ephemeral worker attempt")).toBeInTheDocument();
    expect(
      screen.getByText(/execution-owned ephemeral worker is already running/i),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open logs" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("terminal-view")).not.toBeInTheDocument();
  });

  it("does not seed a terminal for an ordinary background worker", () => {
    const agent = activeServiceWorkerAgent();

    renderWithAgents([agent], agent.name);

    expect(screen.getByText("Worker terminal unavailable")).toBeInTheDocument();
    expect(screen.getByText(/background worker/i)).toBeInTheDocument();
    expect(screen.queryByTestId("terminal-view")).not.toBeInTheDocument();
  });

  it("still seeds a terminal for a custom interactive agent", () => {
    const agent: LoomAgentStatus = {
      ...activeServiceWorkerAgent(),
      name: "pr-reviewer",
      role: "pr-review",
      role_kind: "interactive",
    };

    renderWithAgents([agent], agent.name);

    expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    expect(
      screen.queryByText("Worker terminal unavailable"),
    ).not.toBeInTheDocument();
  });

  it("keeps lifecycle controls locked until the synchronous mutation settles", async () => {
    const initial = activeServiceWorkerAgent();
    const onChanged = vi.fn();
    let resolveRestart:
      | ((value: { message: string; status: "succeeded" }) => void)
      | undefined;
    mocks.restartAgent.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRestart = resolve;
      }),
    );

    render(<AgentLifecycleControls agent={initial} onChanged={onChanged} />);
    fireEvent.click(screen.getByTestId("agent-restart-button"));
    expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
    fireEvent.click(screen.getByTestId("agent-restart-button"));
    expect(mocks.restartAgent).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveRestart?.({ message: "agent restarted", status: "succeeded" });
    });

    await waitFor(() =>
      expect(screen.getByTestId("agent-restart-button")).not.toBeDisabled(),
    );
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Restart completed for advanced-task-runner",
      { type: "success" },
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("surfaces a synchronous lifecycle failure and refreshes agent state", async () => {
    const initial = activeServiceWorkerAgent();
    const onChanged = vi.fn();
    mocks.restartAgent.mockResolvedValueOnce({
      message: "agent is unreachable",
      status: "failed",
    });

    render(<AgentLifecycleControls agent={initial} onChanged={onChanged} />);
    fireEvent.click(screen.getByTestId("agent-restart-button"));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Restart failed for advanced-task-runner: agent is unreachable",
      { type: "error" },
    );
  });

  it("surfaces a rejected lifecycle request without claiming completion", async () => {
    const initial = activeServiceWorkerAgent();
    const onChanged = vi.fn();
    mocks.stopAgent.mockRejectedValueOnce(new Error("connection lost"));

    render(<AgentLifecycleControls agent={initial} onChanged={onChanged} />);
    fireEvent.click(screen.getByTestId("agent-stop-button"));
    await waitFor(() =>
      expect(screen.getByTestId("agent-stop-button")).not.toBeDisabled(),
    );
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Stop failed: connection lost",
      { type: "error" },
    );
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("remains interactive after StrictMode effect replay", async () => {
    const initial = activeServiceWorkerAgent();
    const onChanged = vi.fn();
    render(
      <StrictMode>
        <AgentLifecycleControls agent={initial} onChanged={onChanged} />
      </StrictMode>,
    );

    fireEvent.click(screen.getByTestId("agent-restart-button"));
    await waitFor(() =>
      expect(mocks.showToast).toHaveBeenCalledWith(
        "Restart completed for advanced-task-runner",
        { type: "success" },
      ),
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
  });
});
