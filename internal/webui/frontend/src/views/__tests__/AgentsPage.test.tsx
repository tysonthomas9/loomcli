// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { startWorkflowRun } from "@/api";

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
    routeAgentName: "lead-1",
    agents: [
      {
        name: "lead-1",
        role: "plan",
        status: "ready",
        branch: "main",
        repo: "sandbox",
        worktree_path: "/tmp/lead-1",
      },
    ],
    bindings: [],
    localSettings: { settings: null },
    workspaceContext: { repos: [] },
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
      workspaceId: "DESKTOP-QA",
      agentName: mocks.routeAgentName,
    }),
    useSearchParams: () => [new URLSearchParams(), vi.fn()],
  };
});

vi.mock("zustand", () => ({
  useStore: <T,>(_: unknown, selector: (state: any) => T) =>
    selector({ agents: mocks.agents }),
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

vi.mock("@/hooks/workspace", () => ({
  useAutomations: () => ({
    bindings: mocks.bindings,
    initialized: true,
    setEnabled: mocks.setBindingEnabled,
    updateBinding: mocks.updateBinding,
    deleteBinding: mocks.deleteBinding,
    runBinding: mocks.runBinding,
  }),
  useLocalSettings: () => mocks.localSettings,
  useWorkspaceContext: () => mocks.workspaceContext,
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
}));

vi.mock("@/components/AgentDetailPanel", () => ({
  DiffTab: () => <div data-testid="diff-tab" />,
  GitTab: () => <div data-testid="git-tab" />,
}));

vi.mock("@/components/FileEditorPanel", () => ({
  FileEditorPanel: () => <div data-testid="files-tab" />,
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
  IssueDetailPanel: () => <div data-testid="issue-detail" />,
}));

describe("AgentsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
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
    mocks.localSettings = { settings: null };
    mocks.workspaceContext = { repos: [] };
    mocks.listTriggerBindingRuns.mockResolvedValue({ runs: [] });
    mocks.promptAgentRoleName.mockReturnValue("");
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
  });

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
          repoUrl: "https://github.com/tyson/sandbox.git",
          baseBranch: "develop",
          openPullRequest: true,
          stackedPullRequests: true,
        },
      );
    });
  });
});
