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
import { ApiError, type LoomAgentStatus } from "@/types";

import { AgentDetailMain, AgentLifecycleControls } from "./AgentDetailMain";
import {
  loadPendingAgentLifecycleCommand,
  pendingAgentLifecycleCommandStorageKey,
  pendingAgentLifecycleStorageKey,
  savePendingAgentLifecycleCommand,
} from "./agentLifecyclePending";

const mocks = vi.hoisted(() => ({
  useAgentStoreInstance: vi.fn(),
  getAgentLifecycleCommand: vi.fn(),
  restartAgent: vi.fn(),
  showToast: vi.fn(),
  startAgent: vi.fn(),
  stopAgent: vi.fn(),
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: mocks.useAgentStoreInstance,
}));

vi.mock("@/hooks/api", () => ({
  getAgentLifecycleCommand: mocks.getAgentLifecycleCommand,
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
    daemon_managed: true,
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
      pending: false,
      status: "succeeded",
    });
    mocks.startAgent.mockResolvedValue({
      message: "agent started",
      pending: false,
      status: "succeeded",
    });
    mocks.stopAgent.mockResolvedValue({
      message: "agent stopped",
      pending: false,
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

  it("does not seed a terminal for an ordinary daemon-supervised worker", () => {
    const agent = activeServiceWorkerAgent();

    renderWithAgents([agent], agent.name);

    expect(screen.getByText("Worker terminal unavailable")).toBeInTheDocument();
    expect(screen.getByText(/daemon-supervised/i)).toBeInTheDocument();
    expect(screen.queryByTestId("terminal-view")).not.toBeInTheDocument();
  });

  it("still seeds a terminal for a custom interactive agent", () => {
    const agent: LoomAgentStatus = {
      ...activeServiceWorkerAgent(),
      name: "pr-reviewer",
      role: "pr-review",
      role_kind: "interactive",
      daemon_managed: false,
    };

    renderWithAgents([agent], agent.name);

    expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    expect(
      screen.queryByText("Worker terminal unavailable"),
    ).not.toBeInTheDocument();
  });

  it("keeps a fast restart locked when every Agent snapshot is unchanged", async () => {
    vi.useFakeTimers();
    try {
      const initial = activeServiceWorkerAgent();
      const onChanged = vi.fn();
      mocks.restartAgent.mockResolvedValueOnce({
        message: "restart requested",
        pending: true,
        command_id: "restart-fast",
        status: "queued",
      });
      mocks.getAgentLifecycleCommand
        .mockResolvedValueOnce({
          command_id: "restart-fast",
          action: "restart",
          status: "queued",
        })
        .mockResolvedValueOnce({
          command_id: "restart-fast",
          action: "restart",
          status: "succeeded",
        });

      const { rerender } = render(
        <AgentLifecycleControls agent={initial} onChanged={onChanged} />,
      );
      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-restart-button"));
        await Promise.resolve();
      });
      expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
      expect(mocks.getAgentLifecycleCommand).toHaveBeenCalledTimes(1);

      rerender(
        <AgentLifecycleControls agent={{ ...initial }} onChanged={onChanged} />,
      );
      expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
      fireEvent.click(screen.getByTestId("agent-restart-button"));
      expect(mocks.restartAgent).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });
      expect(screen.getByTestId("agent-restart-button")).not.toBeDisabled();
      expect(mocks.showToast).toHaveBeenCalledWith(
        "Restart completed for advanced-task-runner",
        { type: "success" },
      );
      expect(onChanged).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("coordinates two mounted lifecycle controls before either request settles", async () => {
    const initial = activeServiceWorkerAgent();
    let resolveRestart:
      | ((value: {
          message: string;
          pending: boolean;
          command_id: string;
          status: "queued";
        }) => void)
      | undefined;
    mocks.restartAgent.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRestart = resolve;
      }),
    );
    mocks.getAgentLifecycleCommand.mockResolvedValue({
      command_id: "restart-two-views",
      action: "restart",
      status: "running",
    });

    render(
      <>
        <AgentLifecycleControls agent={initial} onChanged={vi.fn()} />
        <AgentLifecycleControls agent={initial} onChanged={vi.fn()} />
      </>,
    );
    const restartButtons = screen.getAllByTestId("agent-restart-button");
    fireEvent.click(restartButtons[0]!);
    fireEvent.click(restartButtons[1]!);

    expect(mocks.restartAgent).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(restartButtons[0]).toBeDisabled();
      expect(restartButtons[1]).toBeDisabled();
    });

    await act(async () => {
      resolveRestart?.({
        message: "restart requested",
        pending: true,
        command_id: "restart-two-views",
        status: "queued",
      });
      await Promise.resolve();
    });
    expect(mocks.restartAgent).toHaveBeenCalledTimes(1);
    expect(loadPendingAgentLifecycleCommand("E2E", initial.name)).toMatchObject(
      { commandId: "restart-two-views" },
    );
  });

  it("makes two mounted controls adopt the newest exact-key storage event", async () => {
    const initial = activeServiceWorkerAgent();
    savePendingAgentLifecycleCommand({
      workspace: "E2E",
      agent: initial.name,
      action: "restart",
      commandId: "restart-older-tab",
      acceptedAt: Date.now() - 1_000,
      warningShown: false,
    });
    mocks.getAgentLifecycleCommand.mockImplementation(
      async (_workspace: string, _agent: string, commandId: string) => ({
        command_id: commandId,
        action: "restart" as const,
        status: "running" as const,
      }),
    );
    render(
      <>
        <AgentLifecycleControls agent={initial} onChanged={vi.fn()} />
        <AgentLifecycleControls agent={initial} onChanged={vi.fn()} />
      </>,
    );

    const pendingFromOtherTab = {
      workspace: "E2E",
      agent: initial.name,
      action: "restart" as const,
      commandId: "restart-other-tab",
      acceptedAt: Date.now(),
      warningShown: false,
    };
    const storageKey = pendingAgentLifecycleCommandStorageKey(
      "E2E",
      initial.name,
      pendingFromOtherTab.commandId,
    );
    act(() => {
      window.localStorage.setItem(
        storageKey,
        JSON.stringify(pendingFromOtherTab),
      );
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: storageKey,
          newValue: JSON.stringify(pendingFromOtherTab),
          storageArea: window.localStorage,
        }),
      );
    });

    await waitFor(() => {
      for (const button of screen.getAllByTestId("agent-restart-button")) {
        expect(button).toBeDisabled();
      }
      const newCommandCalls = mocks.getAgentLifecycleCommand.mock.calls.filter(
        (call) => call[2] === "restart-other-tab",
      );
      expect(newCommandCalls).toHaveLength(2);
    });
  });

  it("does not let a late older 202 response overwrite a newer cross-tab command", async () => {
    const initial = activeServiceWorkerAgent();
    let resolveOlder:
      | ((value: {
          message: string;
          pending: boolean;
          command_id: string;
          status: "queued";
        }) => void)
      | undefined;
    mocks.restartAgent.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOlder = resolve;
      }),
    );
    mocks.getAgentLifecycleCommand.mockImplementation(
      async (_workspace: string, _agent: string, commandId: string) => ({
        command_id: commandId,
        action: "restart" as const,
        status: "running" as const,
      }),
    );
    render(<AgentLifecycleControls agent={initial} onChanged={vi.fn()} />);

    fireEvent.click(screen.getByTestId("agent-restart-button"));
    expect(mocks.restartAgent).toHaveBeenCalledTimes(1);

    const newer = {
      workspace: "E2E",
      agent: initial.name,
      action: "restart" as const,
      commandId: "restart-newer",
      acceptedAt: Date.now() + 1_000,
      warningShown: false,
    };
    const storageKey = pendingAgentLifecycleStorageKey(
      newer.workspace,
      newer.agent,
    );
    act(() => {
      window.localStorage.setItem(storageKey, JSON.stringify(newer));
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: storageKey,
          newValue: JSON.stringify(newer),
          storageArea: window.localStorage,
        }),
      );
    });
    await waitFor(() =>
      expect(mocks.getAgentLifecycleCommand).toHaveBeenCalledWith(
        "E2E",
        initial.name,
        "restart-newer",
        expect.anything(),
      ),
    );

    await act(async () => {
      resolveOlder?.({
        message: "restart requested",
        pending: true,
        command_id: "restart-older",
        status: "queued",
      });
      await Promise.resolve();
    });

    expect(loadPendingAgentLifecycleCommand("E2E", initial.name)).toEqual(
      newer,
    );
    expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
  });

  it("restores a persisted pending command after unmount and remount", async () => {
    const initial = activeServiceWorkerAgent();
    const acceptedAt = Date.now();
    expect(
      savePendingAgentLifecycleCommand({
        workspace: "E2E",
        agent: initial.name,
        action: "restart",
        commandId: "restart-restored",
        acceptedAt,
        warningShown: false,
      }),
    ).toBe(true);
    mocks.getAgentLifecycleCommand
      .mockResolvedValueOnce({
        command_id: "restart-restored",
        action: "restart",
        status: "running",
      })
      .mockResolvedValueOnce({
        command_id: "restart-restored",
        action: "restart",
        status: "succeeded",
      });

    const first = render(
      <AgentLifecycleControls agent={initial} onChanged={vi.fn()} />,
    );
    expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
    await waitFor(() =>
      expect(mocks.getAgentLifecycleCommand).toHaveBeenCalledTimes(1),
    );
    first.unmount();

    render(<AgentLifecycleControls agent={initial} onChanged={vi.fn()} />);
    expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
    await waitFor(() =>
      expect(screen.getByTestId("agent-restart-button")).not.toBeDisabled(),
    );
    expect(loadPendingAgentLifecycleCommand("E2E", initial.name)).toBeNull();
  });

  it("retains the durable lock through transient polling errors and a delayed warning", async () => {
    vi.useFakeTimers();
    try {
      const initial = activeServiceWorkerAgent();
      const acceptedAt = Date.now() - 14_900;
      savePendingAgentLifecycleCommand({
        workspace: "E2E",
        agent: initial.name,
        action: "restart",
        commandId: "restart-delayed",
        acceptedAt,
        warningShown: false,
      });
      mocks.getAgentLifecycleCommand
        .mockRejectedValueOnce(new ApiError(503, "Unavailable"))
        .mockResolvedValueOnce({
          command_id: "restart-delayed",
          action: "restart",
          status: "running",
        })
        .mockResolvedValueOnce({
          command_id: "restart-delayed",
          action: "restart",
          status: "succeeded",
        });

      render(<AgentLifecycleControls agent={initial} onChanged={vi.fn()} />);
      await act(async () => {
        await Promise.resolve();
        await vi.advanceTimersByTimeAsync(100);
      });
      expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
      expect(mocks.showToast).toHaveBeenCalledWith(
        "Restart is still pending for advanced-task-runner; controls remain locked while Loom confirms the command",
        { type: "warning" },
      );
      expect(
        loadPendingAgentLifecycleCommand("E2E", initial.name),
      ).toMatchObject({ warningShown: true });

      await act(async () => {
        await vi.advanceTimersByTimeAsync(900);
      });
      expect(screen.getByTestId("agent-restart-button")).toBeDisabled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000);
      });
      expect(screen.getByTestId("agent-restart-button")).not.toBeDisabled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("surfaces terminal command failure and clears the persisted lock", async () => {
    const initial = activeServiceWorkerAgent();
    mocks.restartAgent.mockResolvedValueOnce({
      message: "restart requested",
      pending: true,
      command_id: "restart-failed",
      status: "queued",
    });
    mocks.getAgentLifecycleCommand.mockResolvedValueOnce({
      command_id: "restart-failed",
      action: "restart",
      status: "failed",
      error_class: "AgentUnreachable",
    });

    render(<AgentLifecycleControls agent={initial} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByTestId("agent-restart-button"));

    await waitFor(() =>
      expect(screen.getByTestId("agent-restart-button")).not.toBeDisabled(),
    );
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Restart failed for advanced-task-runner: AgentUnreachable",
      { type: "error" },
    );
    expect(loadPendingAgentLifecycleCommand("E2E", initial.name)).toBeNull();
  });

  it("settles a graceful stop reported as a yield command", async () => {
    const initial = activeServiceWorkerAgent();
    mocks.stopAgent.mockResolvedValueOnce({
      message: "stop requested",
      pending: true,
      command_id: "stop-yield",
      status: "queued",
    });
    mocks.getAgentLifecycleCommand.mockResolvedValueOnce({
      command_id: "stop-yield",
      action: "yield",
      status: "succeeded",
    });

    render(<AgentLifecycleControls agent={initial} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByTestId("agent-stop-button"));

    await waitFor(() =>
      expect(screen.getByTestId("agent-stop-button")).not.toBeDisabled(),
    );
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Stop completed for advanced-task-runner",
      { type: "success" },
    );
  });

  it("clears an authoritative 404 with an honest toast and refresh", async () => {
    const initial = activeServiceWorkerAgent();
    const onChanged = vi.fn();
    savePendingAgentLifecycleCommand({
      workspace: "E2E",
      agent: initial.name,
      action: "stop",
      commandId: "stop-missing",
      acceptedAt: Date.now(),
      warningShown: false,
    });
    mocks.getAgentLifecycleCommand.mockRejectedValueOnce(
      new ApiError(404, "Not Found"),
    );

    render(<AgentLifecycleControls agent={initial} onChanged={onChanged} />);

    await waitFor(() =>
      expect(screen.getByTestId("agent-stop-button")).not.toBeDisabled(),
    );
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Stop command stop-missing is no longer available; refreshed the current agent state",
      { type: "warning" },
    );
    expect(onChanged).toHaveBeenCalledTimes(1);
    expect(loadPendingAgentLifecycleCommand("E2E", initial.name)).toBeNull();
  });

  it("aborts command polling and cancels delayed work on unmount", async () => {
    vi.useFakeTimers();
    try {
      const initial = activeServiceWorkerAgent();
      let pollSignal: AbortSignal | undefined;
      mocks.restartAgent.mockResolvedValueOnce({
        message: "restart requested",
        pending: true,
        command_id: "restart-unmount",
        status: "queued",
      });
      mocks.getAgentLifecycleCommand.mockImplementationOnce(
        (
          _workspace: string,
          _agent: string,
          _command: string,
          options?: { signal?: AbortSignal },
        ) => {
          pollSignal = options?.signal;
          return new Promise(() => undefined);
        },
      );
      const { unmount } = render(
        <AgentLifecycleControls agent={initial} onChanged={vi.fn()} />,
      );

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-restart-button"));
        await Promise.resolve();
      });
      expect(pollSignal?.aborted).toBe(false);
      mocks.showToast.mockClear();
      unmount();
      expect(pollSignal?.aborted).toBe(true);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(20_000);
      });
      expect(mocks.getAgentLifecycleCommand).toHaveBeenCalledTimes(1);
      expect(mocks.showToast).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
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
