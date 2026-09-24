/**
 * @vitest-environment jsdom
 */

import { act, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createAgentStore } from "@/stores/agentStore";
import type { AgentBrowser, LoomAgentStatus, MutationPayload } from "@/types";

import { AgentDetailMain } from "./AgentDetailMain";

const mocks = vi.hoisted(() => ({
  useAgentStoreInstance: vi.fn(),
  listBrowsers: vi.fn(),
  selectBrowser: vi.fn(),
  subscribers: new Set<{ current: (m: MutationPayload) => void }>(),
}));

vi.mock("@/hooks", async () => {
  const actual = await vi.importActual<
    typeof import("@/hooks/agents/useAgentBrowsers")
  >("@/hooks/agents/useAgentBrowsers");
  return {
    useAgentStoreInstance: mocks.useAgentStoreInstance,
    useWorkspaceContext: () => ({ workspaceId: "E2E" }),
    useAgentBrowsers: actual.useAgentBrowsers,
  };
});

// Only the browser tab strip is needed from the AgentDetailPanel barrel.
vi.mock("@/components/AgentDetailPanel", async () => {
  const actual = await vi.importActual<
    typeof import("@/components/AgentDetailPanel/BrowserTab")
  >("@/components/AgentDetailPanel/BrowserTab");
  return { AgentBrowserTabs: actual.AgentBrowserTabs };
});

vi.mock("@/hooks/common", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  return {
    useEventSubscription: (callback: (m: MutationPayload) => void) => {
      const ref = React.useRef(callback);
      ref.current = callback;
      React.useEffect(() => {
        mocks.subscribers.add(ref);
        return () => {
          mocks.subscribers.delete(ref);
        };
      }, []);
    },
    useEventContext: () => ({ state: "connected" }),
  };
});

vi.mock("@/api/agents/browsers", async () => {
  const actual = await vi.importActual<typeof import("@/api/agents/browsers")>(
    "@/api/agents/browsers",
  );
  return {
    BrowserApiError: actual.BrowserApiError,
    listAgentBrowsers: mocks.listBrowsers,
    selectAgentBrowser: mocks.selectBrowser,
    onBrowserOperatorSessionLost: () => () => {},
  };
});

vi.mock("@/components/TerminalView", () => ({
  TerminalView: () => <div data-testid="terminal-view" />,
}));

beforeEach(() => {
  mocks.listBrowsers.mockReset();
  mocks.selectBrowser.mockReset();
  mocks.subscribers.clear();
  // Default: inventory never settles, so unrelated tests stay synchronous.
  mocks.listBrowsers.mockReturnValue(new Promise(() => {}));
});

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

  describe("durable browser tabs", () => {
    function leadAgent(): LoomAgentStatus {
      return {
        name: "lead",
        branch: "main",
        status: "idle",
        ahead: 0,
        behind: 0,
        workspace: "E2E",
        role: "lead",
        role_kind: "interactive",
        state: "idle",
      } as LoomAgentStatus;
    }

    const research: AgentBrowser = {
      id: "11111111-1111-4111-8111-111111111111",
      workspace_key: "E2E",
      owner_agent_id: "lead",
      created_by: "lead",
      name: "Research",
      desired_state: "running",
      status: "starting",
      request_id: "req-1",
      selected: false,
      created_at: "2026-09-23T10:00:00Z",
      updated_at: "2026-09-23T10:00:00Z",
    };

    it("renders a browser tab strip next to the Terminal for interactive agents", async () => {
      mocks.listBrowsers.mockResolvedValue([research]);
      renderWithAgents([leadAgent()], "lead");

      expect(
        await screen.findByRole("tab", { name: "Research · Starting" }),
      ).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Terminal" })).toHaveAttribute(
        "aria-selected",
        "true",
      );
      expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
      expect(mocks.listBrowsers).toHaveBeenCalledWith(
        "E2E",
        "lead",
        expect.anything(),
      );
    });

    it("adds a browser tab from SSE without reload while Terminal stays active", async () => {
      mocks.listBrowsers.mockResolvedValueOnce([]);
      renderWithAgents([leadAgent()], "lead");
      expect(await screen.findByText("No browsers")).toBeInTheDocument();

      mocks.listBrowsers.mockResolvedValueOnce([research]);
      act(() => {
        for (const sub of mocks.subscribers) {
          sub.current({
            type: "create",
            entity_type: "browser",
            entity_id: "lead",
            workspace_id: "E2E",
            timestamp: new Date().toISOString(),
          });
        }
      });

      expect(
        await screen.findByRole("tab", { name: "Research · Starting" }),
      ).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "Terminal" })).toHaveAttribute(
        "aria-selected",
        "true",
      );
      expect(screen.getByTestId("terminal-view")).toBeVisible();
      expect(screen.queryByTestId("browser-pane")).not.toBeInTheDocument();
    });

    it("renders no browser tabs and makes no browser calls for worker agents", () => {
      renderWithAgents([activeWorkerAgent()], activeWorkerAgent().name);
      expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
      expect(screen.queryByRole("tab")).not.toBeInTheDocument();
      expect(mocks.listBrowsers).not.toHaveBeenCalled();

      renderWithAgents(
        [
          {
            name: "planner-1",
            branch: "planner-1",
            status: "working",
            ahead: 0,
            behind: 0,
            workspace: "E2E",
            role: "planner",
            role_kind: "worker",
            state: "active",
          } as LoomAgentStatus,
        ],
        "planner-1",
      );
      expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
      expect(mocks.listBrowsers).not.toHaveBeenCalled();
    });
  });
});
