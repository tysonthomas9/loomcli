// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { startWorkflowRun } from "@/api";

import { AgentsPage } from "../AgentsPage";

const mocks = vi.hoisted(() => {
  const fetchData = vi.fn();
  return {
    fetchData,
    navigate: vi.fn(),
    showToast: vi.fn(),
    getWorkflowRun: vi.fn(),
    startWorkflowRun: vi.fn(),
    localSettings: { settings: null },
    workspaceContext: { repos: [] },
    agents: [] as Array<Record<string, unknown>>,
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
    useParams: () => ({ workspaceId: "DESKTOP-QA", agentName: "lead-1" }),
    useSearchParams: () => [new URLSearchParams(), vi.fn()],
  };
});

vi.mock("zustand", () => ({
  useStore: <T,>(
    _: unknown,
    selector: (state: { agents: Array<Record<string, unknown>> }) => T,
  ) => selector({ agents: mocks.agents }),
}));

vi.mock("@/api", () => ({
  EPIC_RUNNER_WORKFLOW_NAME: "epic-runner",
  getWorkflowRun: mocks.getWorkflowRun,
  isTerminalWorkflowRunStatus: (status: string | undefined) =>
    status === "completed" ||
    status === "failed" ||
    status === "needs_review" ||
    status === "cancelled",
  startWorkflowRun: mocks.startWorkflowRun,
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => mocks.agentStore,
  useAgentDiffStat: () => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
}));

vi.mock("@/hooks/workspace", () => ({
  useLocalSettings: () => mocks.localSettings,
  useWorkspaceContext: () => mocks.workspaceContext,
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
  GitTab: ({
    agent,
    isActive,
  }: {
    agent: { name: string };
    isActive?: boolean;
  }) => (
    <div
      data-testid="git-tab"
      data-agent={agent.name}
      data-active={String(isActive)}
    />
  ),
  AgentLogsTab: ({
    agentName,
    isActive,
  }: {
    agentName: string;
    isActive: boolean;
  }) => (
    <div
      data-testid="agent-logs-tab"
      data-agent={agentName}
      data-active={String(isActive)}
    />
  ),
  DiffTab: ({
    agent,
    isActive,
  }: {
    agent: { name: string };
    isActive: boolean;
  }) => (
    <div
      data-testid="diff-tab"
      data-agent={agent.name}
      data-active={String(isActive)}
    />
  ),
}));

vi.mock("@/components/FileEditorPanel", () => ({
  FileEditorPanel: ({
    agentName,
    isActive,
  }: {
    agentName: string;
    isActive: boolean;
  }) => (
    <div
      data-testid="file-editor-panel"
      data-agent={agentName}
      data-active={String(isActive)}
    />
  ),
}));

vi.mock("@/components/OpenInEditor", () => ({
  OpenInEditor: ({ path }: { path: string }) => (
    <div data-testid="open-in-editor" data-path={path} />
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
  IssueDetailPanel: () => <div data-testid="issue-detail" />,
}));

describe("AgentsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.localSettings = { settings: null };
    mocks.workspaceContext = { repos: [] };
    mocks.agents = [];
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

  it("renders a Logs tab and shows the logs pane when clicked", async () => {
    mocks.agents = [
      {
        name: "lead-1",
        status: "ready",
        role: "lead",
        branch: "agent/lead-1",
        repo: "loomcli",
      },
    ];

    render(<AgentsPage />);

    const logsTab = await screen.findByRole("button", { name: "Logs" });
    fireEvent.click(logsTab);

    expect(logsTab).toHaveAttribute("aria-current", "page");
    expect(screen.getByTestId("agent-logs-tab")).toHaveAttribute(
      "data-agent",
      "lead-1",
    );
    expect(screen.getByTestId("agent-logs-tab")).toHaveAttribute(
      "data-active",
      "true",
    );
  });

  it("renders OpenInEditor in Info when worktree_path is set", async () => {
    mocks.agents = [
      {
        name: "lead-1",
        status: "ready",
        role: "lead",
        branch: "agent/lead-1",
        repo: "loomcli",
        worktree_path: "/tmp/loomcli/lead-1",
      },
    ];

    render(<AgentsPage />);

    const openInEditor = await screen.findByTestId("open-in-editor");
    expect(openInEditor).toHaveAttribute("data-path", "/tmp/loomcli/lead-1");
  });
});
