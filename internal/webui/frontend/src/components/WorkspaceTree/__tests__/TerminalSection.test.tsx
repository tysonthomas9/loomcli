/**
 * @vitest-environment jsdom
 */

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { createTerminalWorktree, type RepoInfo } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import { ApiError } from "@/types/common";
import {
  DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
  publishTerminalSidebarState,
  TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT,
  TERMINAL_SIDEBAR_NEW_TAB_EVENT,
  TERMINAL_SIDEBAR_SELECT_EVENT,
  TERMINAL_SIDEBAR_SYNC_EVENT,
  TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
} from "@/utils/terminalSidebarBridge";

import { TerminalSection } from "../TerminalSection";

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: vi.fn(),
}));

vi.mock("@/hooks/api", () => ({
  createTerminalWorktree: vi.fn(),
}));

const mockUseWorkspaceContext = vi.mocked(useWorkspaceContext);
const mockCreateTerminalWorktree = vi.mocked(createTerminalWorktree);

function repo(overrides: Partial<RepoInfo>): RepoInfo {
  return {
    name: "api",
    path: "/workspace/api",
    default_branch: "main",
    current_branch: "main",
    remote: "origin",
    groups: [],
    ...overrides,
  };
}

const repos = [
  repo({ name: "api", current_branch: "main" }),
  repo({
    name: "frontend",
    path: "/workspace/frontend",
    default_branch: "develop",
    current_branch: "feature-ui",
  }),
];

function mockWorkspaceContext(contextRepos: RepoInfo[] = repos): void {
  mockUseWorkspaceContext.mockReturnValue({
    workspace: {
      id: "ws-1",
      name: "Acme Workspace",
      path: "/workspace",
      repos: contextRepos,
      groups: [],
      agents: [],
      workspaces: [],
      default_workspace: "Acme Workspace",
    },
    workspaceId: "ws-1",
    activeWorkspaceName: "Acme Workspace",
    repos: contextRepos,
  } as unknown as ReturnType<typeof useWorkspaceContext>);
}

function publishGroupedState(): void {
  act(() => {
    publishTerminalSidebarState({
      groups: [
        {
          id: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
          label: "Workspace",
          isDefault: true,
          members: [],
        },
        {
          id: "feature-auth",
          label: "feature-auth",
          members: [
            {
              repoName: "api",
              baseBranch: "main",
            },
            {
              repoName: "frontend",
              baseBranch: "8f00abc",
              baseDetached: true,
            },
            {
              repoName: "cli",
              reusedBranch: true,
            },
          ],
        },
      ],
      tabs: [
        {
          id: "workspace-tab",
          label: "workspace-tab",
          backendName: "shell",
          connectionState: "connected",
          groupId: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
        },
        {
          id: "feature-tab",
          label: "feature-tab",
          backendName: "codex",
          connectionState: "disconnected",
          groupId: "feature-auth",
        },
      ],
      activeTabId: "feature-tab",
      activeGroupId: "feature-auth",
    });
  });
}

describe("TerminalSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockWorkspaceContext();
  });

  it("renders two-level groups from bridge state and highlights the active group", () => {
    render(<TerminalSection />);
    publishGroupedState();

    expect(screen.getByTestId("terminal-group-workspace")).toHaveTextContent(
      "Acme Workspace",
    );
    const featureGroup = screen.getByTestId("terminal-group-feature-auth");
    expect(featureGroup).toHaveAttribute("data-active", "true");
    expect(featureGroup).toHaveTextContent("feature-auth");
    expect(featureGroup).toHaveTextContent(
      "api \u2190 main \u00b7 frontend \u2190 8f00abc (detached) \u00b7 cli \u2190 existing branch",
    );
    expect(screen.getByTestId("sidebar-terminal-workspace-tab")).toBeVisible();
    expect(screen.getByTestId("sidebar-terminal-feature-tab")).toBeVisible();
    expect(mockCreateTerminalWorktree).not.toHaveBeenCalled();
  });

  it("degrades missing groups to the default workspace group", () => {
    render(<TerminalSection />);

    act(() => {
      window.dispatchEvent(
        new CustomEvent(TERMINAL_SIDEBAR_SYNC_EVENT, {
          detail: {
            tabs: [
              {
                id: "old-tab",
                label: "old-tab",
                backendName: "shell",
                connectionState: "connected",
              },
            ],
            activeTabId: "old-tab",
          },
        }),
      );
    });

    expect(screen.getByTestId("terminal-group-workspace")).toHaveTextContent(
      "Acme Workspace",
    );
    expect(screen.getByTestId("sidebar-terminal-old-tab")).toBeVisible();
  });

  it("dispatches select, group-select, collapse, and new-tab events", () => {
    const onSelect = vi.fn();
    const onGroupSelect = vi.fn();
    const onNew = vi.fn();
    window.addEventListener(TERMINAL_SIDEBAR_SELECT_EVENT, onSelect);
    window.addEventListener(TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT, onGroupSelect);
    window.addEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNew);

    render(<TerminalSection />);
    publishGroupedState();

    fireEvent.click(screen.getByTestId("sidebar-terminal-feature-tab"));
    fireEvent.click(screen.getByTestId("terminal-group-feature-auth"));

    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { tabId: "feature-tab" } }),
    );
    expect(onGroupSelect).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { groupId: "feature-auth" } }),
    );
    expect(
      screen.queryByTestId("sidebar-terminal-feature-tab"),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("terminal-group-feature-auth"));
    fireEvent.click(screen.getByTestId("sidebar-new-terminal-feature-auth"));

    expect(onNew).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { groupId: "feature-auth" } }),
    );

    window.removeEventListener(TERMINAL_SIDEBAR_SELECT_EVENT, onSelect);
    window.removeEventListener(
      TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT,
      onGroupSelect,
    );
    window.removeEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNew);
  });

  it("opens the create modal with repo checkboxes and current branch hints", () => {
    render(<TerminalSection />);

    fireEvent.click(screen.getByTestId("sidebar-new-worktree"));

    expect(screen.getByRole("dialog", { name: "New Worktree" })).toBeVisible();
    expect(screen.getByLabelText(/api/)).toBeChecked();
    expect(screen.getByLabelText(/frontend/)).toBeChecked();
    expect(screen.getByText("main")).toBeInTheDocument();
    expect(screen.getByText("feature-ui")).toBeInTheDocument();
    expect(
      screen.getByText("Names become branch names; / is not supported in v1."),
    ).toBeInTheDocument();
  });

  it("creates a worktree group and dispatches the created group object", async () => {
    const onChanged = vi.fn();
    const onGroupSelect = vi.fn();
    window.addEventListener(
      TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
      onChanged,
    );
    window.addEventListener(TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT, onGroupSelect);
    mockCreateTerminalWorktree.mockResolvedValueOnce({
      group: {
        id: "group-1",
        name: "feature-auth",
        root: "/workspace/.loom/terminal-worktrees/feature-auth",
        created_at: "2026-07-02T00:00:00Z",
        members: [
          {
            repo_name: "api",
            path: "/workspace/.loom/terminal-worktrees/feature-auth/api",
            base_branch: "main",
            base_detached: false,
            reused_branch: false,
          },
        ],
      },
      results: [{ repo: "api", status: "created" }],
    });

    render(<TerminalSection />);
    fireEvent.click(screen.getByTestId("sidebar-new-worktree"));
    fireEvent.change(screen.getByLabelText("Worktree name"), {
      target: { value: "feature-auth" },
    });
    fireEvent.click(screen.getByLabelText(/frontend/));
    fireEvent.change(screen.getByLabelText("Base branch"), {
      target: { value: "main" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create Worktree" }));

    await waitFor(() => {
      expect(mockCreateTerminalWorktree).toHaveBeenCalledWith("ws-1", {
        name: "feature-auth",
        repos: ["api"],
        base: "main",
      });
    });
    expect(onChanged).toHaveBeenCalledWith(
      expect.objectContaining({
        detail: {
          id: "group-1",
          label: "feature-auth",
          members: [
            {
              repoName: "api",
              baseBranch: "main",
              baseDetached: false,
              reusedBranch: false,
            },
          ],
        },
      }),
    );
    expect(onGroupSelect).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { groupId: "group-1" } }),
    );
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    window.removeEventListener(
      TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
      onChanged,
    );
    window.removeEventListener(
      TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT,
      onGroupSelect,
    );
  });

  it("keeps the modal open with partial per-repo failure results", async () => {
    mockCreateTerminalWorktree.mockRejectedValueOnce(
      new ApiError(409, "Conflict", {
        error: "worktree create failed",
        kind: "conflict",
        results: [
          { repo: "api", status: "rolled_back" },
          {
            repo: "frontend",
            status: "conflict",
            message: "already checked out",
          },
        ],
      }),
    );

    render(<TerminalSection />);
    fireEvent.click(screen.getByTestId("sidebar-new-worktree"));
    fireEvent.change(screen.getByLabelText("Worktree name"), {
      target: { value: "feature-auth" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create Worktree" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "worktree create failed",
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Kind: conflict");
    const results = screen.getByTestId("worktree-results");
    expect(within(results).getByText("api")).toBeInTheDocument();
    expect(within(results).getByText("rolled back")).toBeInTheDocument();
    expect(within(results).getByText("frontend")).toBeInTheDocument();
    expect(within(results).getByText("conflict")).toBeInTheDocument();
    expect(
      within(results).getByText("already checked out"),
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "New Worktree" })).toBeVisible();
  });

  it("shows all-fail per-repo results", async () => {
    mockCreateTerminalWorktree.mockRejectedValueOnce(
      new ApiError(400, "Bad Request", {
        error: "no repos could be created",
        kind: "validation",
        results: [
          { repo: "api", status: "error", message: "base missing" },
          { repo: "frontend", status: "error", message: "target occupied" },
        ],
      }),
    );

    render(<TerminalSection />);
    fireEvent.click(screen.getByTestId("sidebar-new-worktree"));
    fireEvent.change(screen.getByLabelText("Worktree name"), {
      target: { value: "feature-auth" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create Worktree" }));

    expect(await screen.findByText("no repos could be created")).toBeVisible();
    expect(screen.getByText("base missing")).toBeInTheDocument();
    expect(screen.getByText("target occupied")).toBeInTheDocument();
  });

  it("disables the new-worktree action when there are no repos", () => {
    mockWorkspaceContext([]);
    render(<TerminalSection />);

    expect(screen.getByTestId("sidebar-new-worktree")).toBeDisabled();
  });
});
