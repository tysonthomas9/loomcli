// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { existsSync } from "node:fs";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { startWorkflowRun } from "@/api";
import type { AgentRecordSummary } from "@/api";
import {
  loadAgentWorkPanelView,
  saveAgentWorkPanelView,
} from "@/utils/agentWorkPanelStorage";

import { AgentsPage } from "../AgentsPage";

const mocks = vi.hoisted(() => {
  const fetchData = vi.fn();
  return {
    fetchData,
    navigate: vi.fn(),
    showToast: vi.fn(),
    getWorkflowRun: vi.fn(),
    getWorkspaceRole: vi.fn(),
    listTriggerBindingRuns: vi.fn(),
    promptAgentRoleName: vi.fn(),
    startWorkflowRun: vi.fn(),
    updateWorkspaceRole: vi.fn(),
    setBindingEnabled: vi.fn(),
    updateBinding: vi.fn(),
    deleteBinding: vi.fn(),
    runBinding: vi.fn(),
    setRecordEnabled: vi.fn(),
    deleteRecord: vi.fn(),
    fetchIssue: vi.fn(),
    clearIssue: vi.fn(),
    updateIssueDetails: vi.fn(),
    handleApprove: vi.fn(),
    handleReject: vi.fn(),
    handleCopyLink: vi.fn(),
    useAgentHistory: vi.fn(),
    routeWorkspaceId: "DESKTOP-QA",
    routeAgentName: "lead-1" as string | undefined,
    agentRecords: [],
    bindings: [],
    localSettings: { settings: null },
    workspaceContext: { repos: [] } as {
      repos: Array<{
        name: string;
        remote_url?: string;
        default_branch: string;
      }>;
      agents?: Array<{
        name: string;
        repos: string[];
        repo_groups: string[];
        cross_repo: boolean;
        role_name?: string;
      }>;
      workspace?: { name: string } | null;
    },
    workspaceIssues: [] as Array<{
      id: string;
      title: string;
      status: string;
      issue_type: string;
      priority: number;
    }>,
    issueDetails: null as { id: string; title?: string } | null,
    agents: [
      {
        name: "lead-1",
        role: "plan",
        status: "ready",
        branch: "main",
        repo: "sandbox",
        worktree_path: "/tmp/lead-1",
      },
    ] as Array<{
      name: string;
      role?: string;
      role_kind?: string;
      repo?: string;
      status?: string;
      branch?: string;
      cross_repo?: boolean;
      worktree_path?: string;
    }>,
    agentStore: {
      getState: () => ({ fetchData }),
    },
  };
});

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => mocks.navigate,
    useParams: () => ({
      workspaceId: mocks.routeWorkspaceId,
      agentName: mocks.routeAgentName,
    }),
    useSearchParams: () => [new URLSearchParams(), vi.fn()],
  };
});

vi.mock("zustand", () => ({
  useStore: <T,>(
    _: unknown,
    selector: (state: { agents: typeof mocks.agents }) => T,
  ) => selector({ agents: mocks.agents }),
}));

vi.mock("@/api", () => ({
  EPIC_RUNNER_WORKFLOW_NAME: "epic-runner",
  getWorkflowRun: mocks.getWorkflowRun,
  getWorkspaceRole: mocks.getWorkspaceRole,
  isTerminalWorkflowRunStatus: (status: string | undefined) =>
    status === "completed" ||
    status === "failed" ||
    status === "needs_review" ||
    status === "cancelled",
  listTriggerBindingRuns: mocks.listTriggerBindingRuns,
  promptAgentRoleName: mocks.promptAgentRoleName,
  startWorkflowRun: mocks.startWorkflowRun,
  updateWorkspaceRole: mocks.updateWorkspaceRole,
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => mocks.agentStore,
}));

vi.mock("@/hooks/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/agents")>();
  return {
    ...actual,
    useAgentHistory: (...args: unknown[]) => mocks.useAgentHistory(...args),
  };
});

vi.mock("@/hooks/workspace", () => ({
  useAutomations: () => ({
    agentRecords: mocks.agentRecords,
    bindings: mocks.bindings,
    initialized: true,
    setEnabled: mocks.setBindingEnabled,
    setRecordEnabled: mocks.setRecordEnabled,
    updateBinding: mocks.updateBinding,
    deleteRecord: mocks.deleteRecord,
    deleteBinding: mocks.deleteBinding,
    runBinding: mocks.runBinding,
  }),
  useLocalSettings: () => mocks.localSettings,
  useWorkspaceContext: () => mocks.workspaceContext,
}));

vi.mock("@/contexts/WorkspaceViewContext", () => ({
  useWorkspaceViewData: () => ({
    issues: mocks.workspaceIssues,
    issueDetails: mocks.issueDetails,
    isLoadingDetails: false,
    detailError: null,
  }),
  useWorkspaceViewActions: () => ({
    fetchIssue: mocks.fetchIssue,
    clearIssue: mocks.clearIssue,
    updateIssueDetails: mocks.updateIssueDetails,
    handleApprove: mocks.handleApprove,
    handleReject: mocks.handleReject,
    handleCopyLink: mocks.handleCopyLink,
  }),
}));

vi.mock("@/components/WorkflowSourceModal", () => ({
  WorkflowSourceModal: ({ isOpen }: { isOpen: boolean }) =>
    isOpen ? <div data-testid="workflow-source-modal" /> : null,
}));

vi.mock("@/hooks/ui/useToast", () => ({
  useToast: () => ({ showToast: mocks.showToast }),
}));

vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: ReactNode }) => <>{children}</>,
  LoadingSkeleton: {
    Monitor: () => <div>loading</div>,
  },
}));

vi.mock("@/components/AgentDetailMain/AgentDetailMain", () => ({
  AgentDetailMain: () => <div data-testid="agent-detail" />,
  AgentLifecycleControls: ({ agent }: { agent: { name: string } }) => (
    <div data-testid="agent-lifecycle-controls">{agent.name}</div>
  ),
}));

vi.mock("@/components/AgentDetailPanel", () => ({
  GitTab: ({ agent }: { agent: { name: string } }) => (
    <div data-testid="git-tab" data-agent={agent.name} />
  ),
  DiffTab: ({ agent }: { agent: { name: string } }) => (
    <div data-testid="diff-tab" data-agent={agent.name} />
  ),
}));

vi.mock("@/components/AgentWorkPanel/AgentWorkPanel", () => ({
  AgentWorkPanel: ({
    epicRunnerRuns,
    onRunEpic,
  }: {
    epicRunnerRuns?: Record<string, { status?: string; run_id?: string }>;
    onRunEpic?: (epicId: string) => void | Promise<void>;
  }) => {
    const run = epicRunnerRuns?.["EPIC-1"];
    return (
      <>
        <button type="button" onClick={() => void onRunEpic?.("EPIC-1")}>
          Run lead epic
        </button>
        <div data-testid="epic-runner-run-state">
          {run ? `${run.run_id}:${run.status}` : "none"}
        </div>
      </>
    );
  },
}));

vi.mock("@/components/IssueDetailPanel/IssueDetailPanel", () => ({
  IssueDetailPanel: ({
    issue,
    inline,
    onClose,
  }: {
    issue: { id?: string } | null;
    inline?: boolean;
    onClose?: () => void;
  }) => (
    <div
      data-testid="issue-detail"
      data-task-id={issue?.id ?? ""}
      data-inline={String(inline === true)}
    >
      <button type="button" onClick={onClose}>
        Close task
      </button>
    </div>
  ),
}));

vi.mock("@/components/FileExplorer", () => ({
  WorkspaceFileBrowser: ({
    mode,
    agentName,
    isActive,
  }: {
    mode?: string;
    agentName?: string;
    isActive?: boolean;
  }) => (
    <div
      data-testid="workspace-file-browser"
      data-mode={mode}
      data-agent={agentName}
      data-active={String(isActive)}
    />
  ),
}));

function durableRecord(
  overrides: Partial<AgentRecordSummary> = {},
): AgentRecordSummary {
  return {
    id: "agent-record-1",
    name: "Documentation reviewer",
    kind: "prompt",
    enabled: true,
    behavior: { role_name: "documentation" },
    workspace_key: "DESKTOP-QA",
    ...overrides,
  };
}

describe("AgentsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.clear();
    mocks.routeWorkspaceId = "DESKTOP-QA";
    mocks.routeAgentName = "lead-1";
    mocks.agents = [
      {
        name: "lead-1",
        role: "plan",
        status: "ready",
        branch: "main",
        repo: "sandbox",
        worktree_path: "/tmp/lead-1",
      },
    ];
    mocks.bindings = [];
    mocks.agentRecords = [];
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mocks.localSettings = { settings: null };
    mocks.workspaceContext = { repos: [] };
    mocks.workspaceIssues = [];
    mocks.issueDetails = null;
    mocks.listTriggerBindingRuns.mockResolvedValue({ runs: [] });
    mocks.promptAgentRoleName.mockReturnValue("");
    mocks.setRecordEnabled.mockResolvedValue(undefined);
    mocks.deleteRecord.mockResolvedValue(undefined);
    mocks.startWorkflowRun.mockResolvedValue({
      run_id: "run-1",
      status: "queued",
      updated_at: "2026-01-23T00:00:00Z",
    });
    mocks.getWorkflowRun.mockResolvedValue({
      run_id: "run-1",
      status: "running",
      updated_at: "2026-01-23T00:00:01Z",
    });
  });

  it("shows the editor-group split control for agent detail tabs", async () => {
    render(<AgentsPage />);

    const toggle = await screen.findByTestId("agent-editor-split");
    expect(toggle.getAttribute("aria-label")).toBe("Split editor right");
  });

  it("resolves a direct route for a configured agent without a live monitor row", async () => {
    mocks.routeAgentName = "configured-task";
    mocks.agents = [];
    mocks.workspaceContext = {
      repos: [],
      agents: [
        {
          name: "configured-task",
          repos: ["sandbox"],
          repo_groups: [],
          cross_repo: false,
          role_name: "task",
        },
      ],
      workspace: { name: "Desktop QA" },
    };

    render(<AgentsPage />);

    expect(await screen.findByRole("button", { name: "Runs" })).toBeTruthy();
    expect(mocks.useAgentHistory).toHaveBeenCalledWith(
      "DESKTOP-QA",
      "configured-task",
      true,
    );
    fireEvent.click(screen.getByRole("button", { name: "Info" }));
    expect(screen.queryByText("Select an agent to view info.")).toBeNull();
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.queryByTestId("agent-lifecycle-controls")).toBeNull();
  });

  it("keeps configured-agent detail resolved when the route changes", async () => {
    mocks.routeAgentName = "configured-one";
    mocks.agents = [];
    mocks.workspaceContext = {
      repos: [],
      agents: [
        {
          name: "configured-one",
          repos: ["sandbox"],
          repo_groups: [],
          cross_repo: false,
          role_name: "task",
        },
        {
          name: "configured-two",
          repos: ["sandbox"],
          repo_groups: [],
          cross_repo: false,
          role_name: "plan",
        },
      ],
      workspace: { name: "Desktop QA" },
    };

    const rendered = render(<AgentsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Info" }));
    expect(
      screen.getByRole("heading", { name: "configured-one" }),
    ).toBeTruthy();

    mocks.routeAgentName = "configured-two";
    rendered.rerender(<AgentsPage />);
    fireEvent.click(screen.getByRole("button", { name: "Info" }));

    expect(
      screen.getByRole("heading", { name: "configured-two" }),
    ).toBeTruthy();
    expect(screen.queryByText("Select an agent to view info.")).toBeNull();
    expect(mocks.useAgentHistory).toHaveBeenLastCalledWith(
      "DESKTOP-QA",
      "configured-two",
      false,
    );
  });

  it("does not expose Terminal for a configured interactive agent without a live runtime row", async () => {
    mocks.routeAgentName = "configured-lead";
    mocks.agents = [];
    mocks.workspaceContext = {
      repos: [],
      agents: [
        {
          name: "configured-lead",
          repos: ["sandbox"],
          repo_groups: [],
          cross_repo: false,
          role_name: "lead",
        },
      ],
      workspace: { name: "Desktop QA" },
    };

    render(<AgentsPage />);

    expect(await screen.findByRole("button", { name: "Runs" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Info" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.queryByTestId("agent-detail")).toBeNull();
  });

  it("queues the built-in epic runner workflow from the lead-panel Run button", async () => {
    render(<AgentsPage />);

    fireEvent.click(screen.getByRole("button", { name: "Run lead epic" }));

    await waitFor(() => {
      expect(startWorkflowRun).toHaveBeenCalledWith(
        "DESKTOP-QA",
        "epic-runner",
        {
          epicId: "EPIC-1",
          leadName: "lead-1",
          requestedBy: "ui",
          runner: "local-task-runner",
          deliveryMode: "patch-back",
        },
      );
    });
    expect(mocks.fetchData).toHaveBeenCalledTimes(1);
    expect(mocks.showToast).toHaveBeenCalledWith(
      "Epic runner queued for lead-1: run-1",
      { type: "success" },
    );
    expect(screen.getByTestId("epic-runner-run-state").textContent).toBe(
      "run-1:queued",
    );
  });

  it("renders a binding through the shared editor-group tabs", async () => {
    mocks.routeAgentName = "binding-1";
    mocks.agents = [];
    mocks.bindings = [
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-1",
        name: "Planner binding",
        source_kind: "cron",
        route_key: "binding-1",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        enabled: true,
        schedule: "*/10 * * * *",
      },
    ];

    render(<AgentsPage />);

    expect(await screen.findByTestId("agent-editor-groups")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Runs" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Info" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Git" })).toBeNull();
    expect(screen.getByTestId("workflow-agent-run-now")).toBeTruthy();
    expect(mocks.useAgentHistory).not.toHaveBeenCalled();
  });

  it("resolves a durable agent id and reads aggregate history across its bindings", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [];
    mocks.agentRecords = [durableRecord()];
    mocks.bindings = [
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-review",
        name: "Documentation reviewer",
        source_kind: "internal",
        route_key: "binding-review",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        target_agent_service_id: "agent-record-1",
        event_type_patterns: ["internal.task.ready"],
        enabled: true,
      },
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-schedule",
        name: "Documentation reviewer",
        source_kind: "cron",
        route_key: "binding-schedule",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        target_agent_service_id: "agent-record-1",
        enabled: true,
        schedule: "0 9 * * *",
      },
    ];

    render(<AgentsPage />);

    expect(await screen.findByTestId("workflow-agent-no-runs")).toBeTruthy();
    expect(mocks.useAgentHistory).toHaveBeenCalledWith(
      "DESKTOP-QA",
      "agent-record-1",
      true,
    );
    expect(screen.queryByTestId("workflow-agent-run-now")).toBeNull();
    expect(screen.getByTestId("agent-record-header")).toHaveTextContent(
      "2 trigger bindings",
    );
    fireEvent.click(screen.getByTestId("agent-record-toggle-enabled"));
    await waitFor(() => {
      expect(mocks.setRecordEnabled).toHaveBeenCalledWith(
        "agent-record-1",
        false,
      );
    });

    fireEvent.click(screen.getByRole("button", { name: "Info" }));
    expect(screen.getByTestId("agent-record-trigger-list")).toHaveTextContent(
      "binding-review",
    );
    expect(screen.getByTestId("agent-record-trigger-list")).toHaveTextContent(
      "binding-schedule",
    );
    fireEvent.click(screen.getByTestId("agent-record-trigger-binding-review"));
    expect(mocks.navigate).toHaveBeenCalledWith(
      "/ws/DESKTOP-QA/agents/binding-review",
    );
  });

  it("prefers a durable record over its same-id legacy roster projection", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [
      {
        name: "agent-record-1",
        role: "plan",
        status: "ready",
        branch: "main",
        repo: "sandbox",
        worktree_path: "/tmp/legacy-projection",
      },
    ];
    mocks.agentRecords = [durableRecord()];
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentsPage />);

    expect(await screen.findByTestId("agent-record-header")).toBeTruthy();
    expect(mocks.useAgentHistory).toHaveBeenCalledWith(
      "DESKTOP-QA",
      "agent-record-1",
      true,
    );
    expect(screen.queryByTestId("agent-lifecycle-controls")).toBeNull();
    expect(screen.queryByRole("button", { name: "Git" })).toBeNull();
  });

  it("opens a durable-agent run task in the inline right pane without leaving the agent route", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [];
    mocks.agentRecords = [durableRecord()];
    mocks.bindings = [
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-review",
        name: "Documentation reviewer",
        source_kind: "internal",
        route_key: "binding-review",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        target_agent_service_id: "agent-record-1",
        event_type_patterns: ["internal.task.review"],
        enabled: true,
      },
    ];
    mocks.workspaceIssues = [
      {
        id: "TASK-1",
        title: "Document the run",
        status: "review",
        issue_type: "task",
        priority: 2,
      },
    ];
    const run = {
      workspace_key: "DESKTOP-QA",
      run_id: "run-task-1",
      driver_id: "prompt-agent",
      driver_version_id: "v1",
      status: "completed",
      steps: [
        {
          id: "step-task-1",
          step_kind: "task_run",
          task_run_id: "task-run-1",
          task_id: "TASK-1",
          status: "completed",
        },
      ],
      created_at: "2026-07-29T00:00:00Z",
      updated_at: "2026-07-29T00:01:00Z",
      finished_at: "2026-07-29T00:01:00Z",
    };
    mocks.useAgentHistory.mockReturnValue({
      runs: [run],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mocks.getWorkflowRun.mockResolvedValue(run);

    render(<AgentsPage />);

    const taskLink = await screen.findByRole("link", { name: "TASK-1" });
    expect(fireEvent.click(taskLink)).toBe(false);

    const detail = await screen.findByTestId("issue-detail");
    expect(detail).toHaveAttribute("data-task-id", "TASK-1");
    expect(detail).toHaveAttribute("data-inline", "true");
    expect(
      loadAgentWorkPanelView("DESKTOP-QA", "agent-record-1").selectedTaskId,
    ).toBe("TASK-1");
    await waitFor(() => {
      expect(mocks.fetchIssue).toHaveBeenCalledWith("TASK-1");
    });
    expect(mocks.navigate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Close task" }));
    expect(screen.queryByTestId("issue-detail")).toBeNull();
    expect(
      loadAgentWorkPanelView("DESKTOP-QA", "agent-record-1").selectedTaskId,
    ).toBeNull();
  });

  it("does not fetch or render the previous agent's selected task while switching routes", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [];
    mocks.agentRecords = [
      durableRecord(),
      durableRecord({
        id: "agent-record-2",
        name: "Bug triage",
        behavior: { role_name: "bug-triage" },
      }),
    ];
    mocks.workspaceIssues = [
      {
        id: "TASK-1",
        title: "Document the run",
        status: "review",
        issue_type: "task",
        priority: 2,
      },
    ];
    saveAgentWorkPanelView("DESKTOP-QA", "agent-record-1", {
      selectedTaskId: "TASK-1",
    });

    const rendered = render(<AgentsPage />);
    expect(await screen.findByTestId("issue-detail")).toHaveAttribute(
      "data-task-id",
      "TASK-1",
    );

    mocks.fetchIssue.mockClear();
    mocks.routeAgentName = "agent-record-2";
    rendered.rerender(<AgentsPage />);

    expect(screen.queryByTestId("issue-detail")).toBeNull();
    expect(mocks.fetchIssue).not.toHaveBeenCalledWith("TASK-1");
  });

  it("keeps binding-specific Run now on a record route with exactly one trigger", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [];
    mocks.agentRecords = [durableRecord()];
    mocks.bindings = [
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-review",
        name: "Documentation reviewer",
        source_kind: "internal",
        route_key: "binding-review",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        target_agent_service_id: "agent-record-1",
        event_type_patterns: ["internal.task.ready"],
        enabled: true,
      },
    ];

    render(<AgentsPage />);

    expect(await screen.findByTestId("workflow-agent-run-now")).toBeTruthy();
    expect(screen.getByTestId("agent-record-header")).toHaveTextContent(
      "1 trigger binding",
    );
  });

  it("replaces Run now with the Review-transition hint on a single-binding record route", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [];
    mocks.agentRecords = [durableRecord()];
    mocks.bindings = [
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-review",
        name: "Documentation reviewer",
        source_kind: "internal",
        route_key: "binding-review",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        target_agent_service_id: "agent-record-1",
        event_type_patterns: ["internal.task.review"],
        enabled: true,
      },
    ];

    render(<AgentsPage />);

    expect(
      await screen.findByTestId("workflow-agent-run-now-hint"),
    ).toHaveTextContent("Move a task to Review to run this agent.");
    expect(screen.queryByTestId("workflow-agent-run-now")).toBeNull();
    expect(mocks.runBinding).not.toHaveBeenCalled();
  });

  it("replaces Run now with the Review-transition hint on a binding-child route", async () => {
    mocks.routeAgentName = "binding-review";
    mocks.agents = [];
    mocks.agentRecords = [];
    mocks.bindings = [
      {
        workspace_key: "DESKTOP-QA",
        binding_id: "binding-review",
        name: "Documentation reviewer",
        source_kind: "internal",
        route_key: "binding-review",
        driver_id: "prompt-agent",
        driver_version_id: "v1",
        event_type_patterns: ["internal.task.review"],
        enabled: true,
      },
    ];

    render(<AgentsPage />);

    const hint = await screen.findByTestId("workflow-agent-run-now-hint");
    expect(hint).toHaveTextContent("Move a task to Review to run this agent.");
    expect(hint).toHaveAttribute(
      "title",
      "Move a task to Review to run this agent.",
    );
    expect(screen.queryByTestId("workflow-agent-run-now")).toBeNull();
    expect(mocks.runBinding).not.toHaveBeenCalled();
  });

  it("keeps an orphan record visible and offers record-scoped archival recovery", async () => {
    mocks.routeAgentName = "agent-record-1";
    mocks.agents = [];
    mocks.agentRecords = [durableRecord({ enabled: false })];

    render(<AgentsPage />);

    expect(await screen.findByTestId("agent-record-header")).toHaveTextContent(
      "No trigger bindings",
    );
    expect(screen.getByTestId("agent-record-status-pill")).toHaveTextContent(
      "Disabled",
    );
    expect(screen.queryByTestId("workflow-agent-run-now")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Info" }));
    expect(screen.getByTestId("agent-record-trigger-list")).toHaveTextContent(
      "No trigger bindings are configured",
    );
    fireEvent.click(screen.getByTestId("agent-record-archive"));
    fireEvent.click(screen.getByTestId("agent-record-archive-confirm-yes"));

    await waitFor(() => {
      expect(mocks.deleteRecord).toHaveBeenCalledWith("agent-record-1");
    });
    expect(mocks.navigate).toHaveBeenCalledWith("/ws/DESKTOP-QA/kanban");
  });

  it("does not auto-route a bare workspace B URL through workspace A records", async () => {
    mocks.routeWorkspaceId = "WS-A";
    mocks.routeAgentName = undefined;
    mocks.agents = [];
    mocks.agentRecords = [
      durableRecord({
        id: "agent-record-a",
        workspace_key: "WS-A",
      }),
    ];

    const rendered = render(<AgentsPage />);

    await waitFor(() => {
      expect(mocks.navigate).toHaveBeenCalledWith(
        "/ws/WS-A/agents/agent-record-a",
        { replace: true },
      );
    });

    mocks.navigate.mockClear();
    mocks.routeWorkspaceId = "WS-B";
    mocks.agentRecords = [];
    rendered.rerender(<AgentsPage />);

    expect(mocks.navigate).not.toHaveBeenCalled();

    mocks.agentRecords = [
      durableRecord({
        id: "agent-record-b",
        workspace_key: "WS-B",
      }),
    ];
    rendered.rerender(<AgentsPage />);

    await waitFor(() => {
      expect(mocks.navigate).toHaveBeenCalledWith(
        "/ws/WS-B/agents/agent-record-b",
        { replace: true },
      );
    });
  });

  it("does not expose or seed Terminal for a background advanced worker", async () => {
    mocks.routeAgentName = "triage-1";
    mocks.agents = [
      {
        name: "triage-1",
        role: "bug-triage",
        role_kind: "worker",
        status: "ready",
        branch: "agent/triage-1",
        repo: "loomcli",
        worktree_path: "/tmp/triage-1",
      },
    ];

    render(<AgentsPage />);

    expect(await screen.findByRole("button", { name: "Runs" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Info" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Git" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Diff" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Files" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.queryByTestId("agent-detail")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Info" }));
    expect(screen.getByTestId("agent-lifecycle-controls").textContent).toBe(
      "triage-1",
    );
  });

  it.each([
    ["legacy lead", "lead", undefined],
    ["PR review", "pr-review", "interactive"],
    ["custom interactive", "operator", "interactive"],
  ])(
    "keeps Terminal available for %s agents",
    async (_label, role, roleKind) => {
      mocks.agents = [
        {
          name: "lead-1",
          role,
          ...(roleKind ? { role_kind: roleKind } : {}),
          status: "ready",
          branch: "agent/lead-1",
          repo: "loomcli",
          worktree_path: "/tmp/lead-1",
        },
      ];

      render(<AgentsPage />);

      const terminal = await screen.findByRole("button", { name: "Terminal" });
      expect(terminal.getAttribute("aria-current")).toBe("page");
      expect(screen.getByRole("button", { name: "Runs" })).toBeTruthy();
      expect(screen.getByTestId("agent-detail")).toBeTruthy();
    },
  );

  it("passes local PR runtime payload from the lead-panel Run button", async () => {
    mocks.localSettings = {
      settings: {
        agent_runtime: { default: "local" },
      },
    };
    mocks.workspaceContext = {
      repos: [
        {
          name: "sandbox",
          remote_url: "git@github.com:tyson/sandbox.git",
          default_branch: "develop",
        },
      ],
    };

    render(<AgentsPage />);

    fireEvent.click(screen.getByRole("button", { name: "Run lead epic" }));

    await waitFor(() => {
      expect(startWorkflowRun).toHaveBeenCalledWith(
        "DESKTOP-QA",
        "epic-runner",
        {
          epicId: "EPIC-1",
          leadName: "lead-1",
          requestedBy: "ui",
          runner: "local-task-runner",
          deliveryMode: "pull-request",
          repoUrl: "https://github.com/tyson/sandbox.git",
          baseBranch: "develop",
          openPullRequest: true,
        },
      );
    });
  });

  it("passes Daytona runtime payload from the lead-panel Run button", async () => {
    mocks.localSettings = {
      settings: {
        agent_runtime: { default: "daytona" },
      },
    };
    mocks.workspaceContext = {
      repos: [
        {
          name: "sandbox",
          remote_url: "git@github.com:tyson/sandbox.git",
          default_branch: "develop",
        },
      ],
    };

    render(<AgentsPage />);

    fireEvent.click(screen.getByRole("button", { name: "Run lead epic" }));

    await waitFor(() => {
      expect(startWorkflowRun).toHaveBeenCalledWith(
        "DESKTOP-QA",
        "epic-runner",
        {
          epicId: "EPIC-1",
          leadName: "lead-1",
          requestedBy: "ui",
          runner: "daytona-task-runner",
          deliveryMode: "pull-request",
          repoUrl: "https://github.com/tyson/sandbox.git",
          baseBranch: "develop",
          openPullRequest: true,
          stackedPullRequests: true,
        },
      );
    });
  });

  it("renders the files tab with the agent-rooted v3 browser and gates shortcuts while inactive", async () => {
    mocks.agents = [
      {
        name: "lead-1",
        role: "lead",
        repo: "loomcli",
        status: "ready",
        branch: "agent/lead-1",
      },
    ];

    render(<AgentsPage />);

    const browser = await screen.findByTestId("workspace-file-browser");
    expect(browser.getAttribute("data-mode")).toBe("agent");
    expect(browser.getAttribute("data-agent")).toBe("lead-1");
    expect(browser.getAttribute("data-active")).toBe("false");

    fireEvent.click(screen.getByRole("button", { name: "Files" }));
    await waitFor(() => {
      expect(
        screen
          .getByTestId("workspace-file-browser")
          .getAttribute("data-active"),
      ).toBe("true");
    });
  });

  it("does not expose the file-prompt role editor for an interactive custom agent", async () => {
    mocks.agents = [
      {
        name: "lead-1",
        role: "pr-review",
        role_kind: "interactive",
        status: "ready",
        branch: "agent/lead-1",
        repo: "loomcli",
        worktree_path: "/tmp/lead-1",
      },
    ];

    render(<AgentsPage />);
    fireEvent.click(await screen.findByRole("button", { name: "Info" }));

    expect(screen.queryByTestId("agents-page-edit-config")).toBeNull();
  });

  it("does not leave the retired legacy file editor module in source", () => {
    const retiredModule = ["File", "Editor", "Panel"].join("");
    expect(
      existsSync(new URL(`../../components/${retiredModule}`, import.meta.url)),
    ).toBe(false);
  });
});
