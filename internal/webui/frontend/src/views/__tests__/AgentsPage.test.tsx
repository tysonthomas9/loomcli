// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { existsSync } from "node:fs";
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
    bindings: [],
    localSettings: { settings: null },
    workspaceContext: { repos: [] },
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
      daemon_managed?: boolean;
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
    window.localStorage.clear();
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
  });

  it("does not expose or seed Terminal for a daemon-supervised advanced worker", async () => {
    mocks.routeAgentName = "triage-1";
    mocks.agents = [
      {
        name: "triage-1",
        role: "bug-triage",
        role_kind: "worker",
        daemon_managed: true,
        status: "ready",
        branch: "agent/triage-1",
        repo: "loomcli",
        worktree_path: "/tmp/triage-1",
      },
    ];

    render(<AgentsPage />);

    expect(await screen.findByRole("button", { name: "Info" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Git" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Diff" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Files" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    expect(screen.queryByTestId("agent-detail")).toBeNull();
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
