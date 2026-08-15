// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { existsSync } from "node:fs";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { startWorkflowRun } from "@/api";
import type { AgentServiceDTO, DriverRunDTO } from "@/api/agentServices";

import { AgentsPage } from "../AgentsPage";

const mocks = vi.hoisted(() => {
  const fetchData = vi.fn();
  return {
    fetchData,
    navigate: vi.fn(),
    showToast: vi.fn(),
    getWorkflowRun: vi.fn(),
    startWorkflowRun: vi.fn(),
    routeAgentName: "lead-1",
    localSettings: { settings: null },
    workspaceContext: { repos: [] },
    agentServices: [] as AgentServiceDTO[],
    agentServicesInitialized: true,
    agentServicesError: null as Error | null,
    serviceRuns: [] as DriverRunDTO[],
    serviceRunsNotFound: false,
    serviceRunsError: null as Error | null,
    agents: [] as Array<{
      name: string;
      role?: string;
      repo?: string;
      status?: string;
      branch?: string;
      cross_repo?: boolean;
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
      workspaceId: "DESKTOP-QA",
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
  isTerminalWorkflowRunStatus: (status: string | undefined) =>
    status === "completed" ||
    status === "failed" ||
    status === "needs_review" ||
    status === "cancelled",
  startWorkflowRun: mocks.startWorkflowRun,
}));

vi.mock("@/hooks", () => ({
  useAgentStoreInstance: () => mocks.agentStore,
}));

vi.mock("@/hooks/workspace", () => ({
  useLocalSettings: () => mocks.localSettings,
  useWorkspaceContext: () => mocks.workspaceContext,
  useAgentServices: () => ({
    services: mocks.agentServices,
    total: mocks.agentServices.length,
    loading: false,
    initialized: mocks.agentServicesInitialized,
    error: mocks.agentServicesError,
    refresh: vi.fn(),
  }),
  useAgentServiceRuns: () => ({
    runs: mocks.serviceRuns,
    total: mocks.serviceRuns.length,
    loading: false,
    initialized: true,
    error: mocks.serviceRunsError,
    notFound: mocks.serviceRunsNotFound,
    refresh: vi.fn(),
  }),
  useAgentServiceRunEvents: () => ({
    events: [],
    loading: false,
    initialized: true,
    error: null,
    refresh: vi.fn(),
  }),
  useAgentServiceRunTasks: () => ({
    tasks: [],
    loading: false,
    initialized: true,
    error: null,
    refresh: vi.fn(),
  }),
  useAgentServiceJournal: () => ({
    journal: null,
    loading: false,
    initialized: false,
    error: null,
    refresh: vi.fn(),
  }),
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

vi.mock("@/components/RolePromptCard", () => ({
  RolePromptCard: ({
    workspaceId,
    roleName,
  }: {
    workspaceId: string;
    roleName: string;
  }) => (
    <div
      data-testid="role-prompt-card"
      data-workspace={workspaceId}
      data-role={roleName}
    />
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
  IssueDetailPanel: () => <div data-testid="issue-detail" />,
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

describe("AgentsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.localSettings = { settings: null };
    mocks.workspaceContext = { repos: [] };
    mocks.routeAgentName = "lead-1";
    mocks.agentServices = [];
    mocks.agentServicesInitialized = true;
    mocks.agentServicesError = null;
    mocks.serviceRuns = [];
    mocks.serviceRunsNotFound = false;
    mocks.serviceRunsError = null;
    mocks.agents = [
      {
        name: "lead-1",
        role: "lead",
        repo: "loomcli",
        status: "ready",
        branch: "agent/lead-1",
      },
    ];
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

  it("shows the selected roster agent's real role in the prompt card", async () => {
    mocks.agents = [
      {
        name: "lead-1",
        role: "custom-reviewer",
        repo: "loomcli",
        status: "task",
        branch: "agent/lead-1",
      },
    ];
    render(<AgentsPage />);

    fireEvent.click(await screen.findByRole("button", { name: "Info" }));
    const card = await screen.findByTestId("role-prompt-card");
    expect(card).toHaveAttribute("data-workspace", "DESKTOP-QA");
    expect(card).toHaveAttribute("data-role", "custom-reviewer");
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

  it("resolves a durable service id before a matching live projection and renders its runs", async () => {
    mocks.routeAgentName = "scout";
    mocks.agents = [
      {
        name: "scout",
        role: "task",
        status: "ready",
        branch: "agent/scout",
      },
    ];
    mocks.agentServices = [
      {
        id: "scout",
        name: "Scout",
        kind: "scripted",
        enabled: true,
        behavior: {
          driverId: "scout-driver",
          driverVersionId: "scout-v1",
        },
        bindings: [
          {
            id: "binding-scout-weekly",
            sourceKind: "cron",
            schedule: "@weekly",
            enabled: true,
            routeKey: "cron.scout.weekly",
          },
        ],
        nextFireAt: "2026-08-17T00:00:00Z",
        lastRunStatus: "completed",
        consecutiveFailures: 0,
        errors: [],
        createdAt: "2026-08-14T00:00:00Z",
        updatedAt: "2026-08-14T00:00:00Z",
      },
    ];
    mocks.serviceRuns = [
      {
        workspaceKey: "DESKTOP-QA",
        runId: "run-scout-1",
        driverId: "scout-driver",
        driverVersionId: "scout-v1",
        agentServiceId: "scout",
        status: "completed",
        summary: "Reviewed 3 backlog tickets",
        startedAt: "2026-08-14T10:00:00Z",
        finishedAt: "2026-08-14T10:01:00Z",
        createdAt: "2026-08-14T10:00:00Z",
        updatedAt: "2026-08-14T10:01:00Z",
      },
    ];

    render(<AgentsPage />);

    expect(await screen.findByTestId("agent-service-detail")).toHaveTextContent(
      "Scout",
    );
    expect(screen.getByTestId("agent-service-detail")).toHaveTextContent(
      "scout-driver",
    );
    expect(screen.getByTestId("agent-service-detail")).toHaveTextContent(
      "Weekly",
    );
    expect(
      screen.getByTestId("agent-service-run-run-scout-1"),
    ).toHaveTextContent("Completed");
    expect(
      screen.getByTestId("agent-service-run-run-scout-1"),
    ).toHaveTextContent("Reviewed 3 backlog tickets");
    expect(screen.queryByTestId("agent-editor-groups")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Run lead epic" }),
    ).not.toBeInTheDocument();
  });

  it("handles a service deleted between resolution and run-history loading", () => {
    mocks.routeAgentName = "scout";
    mocks.agentServices = [
      {
        id: "scout",
        name: "Scout",
        kind: "scripted",
        enabled: true,
        behavior: { driverId: "scout", driverVersionId: "v1" },
        bindings: [],
        nextFireAt: null,
        lastRunStatus: "",
        consecutiveFailures: 0,
        errors: [],
        createdAt: "2026-08-14T00:00:00Z",
        updatedAt: "2026-08-14T00:00:00Z",
      },
    ];
    mocks.serviceRunsNotFound = true;

    render(<AgentsPage />);

    expect(screen.getByTestId("agent-service-not-found")).toHaveTextContent(
      "no longer exists",
    );
  });

  it("does not leave the retired legacy file editor module in source", () => {
    const retiredModule = ["File", "Editor", "Panel"].join("");
    expect(
      existsSync(new URL(`../../components/${retiredModule}`, import.meta.url)),
    ).toBe(false);
  });
});
