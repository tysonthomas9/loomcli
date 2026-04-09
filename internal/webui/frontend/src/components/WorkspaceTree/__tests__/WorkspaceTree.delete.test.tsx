/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceTree delete-undo flow.
 * SKIPPED: Workspace entries removed from sidebar in loomcli-8uy0o.
 * Delete/undo logic lives in OtherWorkspacesSection (not currently rendered).
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import "@testing-library/jest-dom";
import { WorkspaceTree } from "../WorkspaceTree";

import type { WorkspaceSummary } from "@/api/workspace";

// ── Mock deleteWorkspace API ────────────────────────────────────────────────

const mockDeleteWorkspace = vi.fn();

vi.mock("@/api/workspace", () => ({
  deleteWorkspace: (...args: unknown[]) => mockDeleteWorkspace(...args),
}));

// ── Mock CSS modules ────────────────────────────────────────────────────────

vi.mock("../WorkspaceContextMenu.module.css", () => ({
  default: {
    menu: "menu",
    menuItem: "menuItem",
    menuItemIcon: "menuItemIcon",
  },
}));

// ── Hook mocks ──────────────────────────────────────────────────────────────

const mockShowToast = vi.fn();
const mockRefetch = vi.fn();

const defaultReposReturn = {
  workspace: null as {
    workspaces: WorkspaceSummary[];
  } | null,
  repos: [] as Array<{
    name: string;
    path: string;
    default_branch: string;
    remote: string;
  }>,
  isLoading: false,
  error: null as string | null,
  refetch: mockRefetch,
};

const defaultAgentContext = {
  agents: [] as Array<{ id: string; repo?: string; status?: string }>,
  tasks: {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 0,
    need_review: 0,
    backlog: 0,
  },
  taskLists: {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    backlog: [],
    done: [],
  },
  agentTasks: {},
  sync: {
    db_synced: true,
    db_last_sync: "",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 0,
    closed: 0,
    total: 0,
    completion: 0,
    remaining: 0,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
  isLoading: false,
  isConnected: true,
  lastUpdated: new Date(),
};

let reposOverride: Partial<typeof defaultReposReturn> = {};

vi.mock("@/hooks", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentContext: () => ({ ...defaultAgentContext }),
  useWorkspaceContext: () => ({
    activeWorkspaceName: null,
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
    agents: [],
  }),
  useToast: () => ({ showToast: mockShowToast }),
  useIssueDiffStat: () => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
  LAYER_TERMINAL_SEARCH: 5,
  LAYER_WORKSPACE_SWITCHER: 42,
  useFocusTrap: vi.fn(),
  useFocusReturn: vi.fn(),
}));

vi.mock("@/components/WorkspaceSwitcher", () => ({
  WorkspaceSwitcher: () => null,
}));

vi.mock("@/hooks/useWorkspaceRepos", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
}));
vi.mock("@/hooks/useWorkspaceTree", () => ({
  useWorkspaceTree: () => ({
    epics: [],
    orphanTasks: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
}));

// ── Helpers ─────────────────────────────────────────────────────────────────

const twoWorkspaces: WorkspaceSummary[] = [
  {
    id: "uuid-ws-a",
    name: "workspace-a",
    path: "/ws/a",
    active: true,
    repo_count: 3,
  },
  {
    id: "uuid-ws-b",
    name: "workspace-b",
    path: "/ws/b",
    active: false,
    repo_count: 2,
  },
];

const twoRepos = [
  {
    name: "alpha",
    path: "/repos/alpha",
    default_branch: "main",
    remote: "origin",
  },
  {
    name: "beta",
    path: "/repos/beta",
    default_branch: "main",
    remote: "origin",
  },
];

function setupMultipleWorkspaces() {
  reposOverride = {
    workspace: { workspaces: twoWorkspaces },
    repos: twoRepos,
    refetch: mockRefetch,
  };
}

/** Open context menu, click Remove, confirm in dialog → triggers handleConfirmDelete */
function triggerDelete(workspaceName: string) {
  fireEvent.click(screen.getByTestId(`workspace-overflow-${workspaceName}`));
  fireEvent.click(screen.getByTestId("workspace-context-menu-remove"));

  // Confirm dialog should open
  const confirmButton = screen.getByRole("button", { name: /remove/i });
  fireEvent.click(confirmButton);
}

/**
 * Extract the onUndo callback from the most recent showToast call that includes one.
 */
function captureOnUndo(): () => void {
  const calls = mockShowToast.mock.calls;
  for (let i = calls.length - 1; i >= 0; i--) {
    const opts = calls[i][1];
    if (opts?.onUndo) return opts.onUndo;
  }
  throw new Error("No showToast call with onUndo found");
}

// ── Tests ───────────────────────────────────────────────────────────────────

describe.skip("WorkspaceTree – delete-undo flow", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
    reposOverride = {};
    mockDeleteWorkspace.mockReset();
    mockDeleteWorkspace.mockResolvedValue({});
    mockShowToast.mockReset();
    mockRefetch.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("calls deleteWorkspace once when handleConfirmDelete triggered twice rapidly", async () => {
    setupMultipleWorkspaces();
    render(<WorkspaceTree defaultCollapsed={false} />);

    // First delete
    triggerDelete("workspace-a");

    // Try triggering a second delete on the same workspace (re-open menu + confirm)
    // The second call to handleConfirmDelete should bail due to deletionPendingRef guard
    // We simulate by directly opening menu on workspace-b (different workspace, same ref guard)
    fireEvent.click(screen.getByTestId("workspace-overflow-workspace-b"));
    fireEvent.click(screen.getByTestId("workspace-context-menu-remove"));
    const confirmButton = screen.getByRole("button", { name: /remove/i });
    fireEvent.click(confirmButton);

    // Advance past the 5s timer
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    // Only the first deletion should have fired
    expect(mockDeleteWorkspace).toHaveBeenCalledTimes(1);
    expect(mockDeleteWorkspace).toHaveBeenCalledWith("uuid-ws-a");
  });

  it("shows 'Deletion already in progress' when onUndo called after timer fires", async () => {
    setupMultipleWorkspaces();
    render(<WorkspaceTree defaultCollapsed={false} />);

    triggerDelete("workspace-a");
    const onUndo = captureOnUndo();

    // Advance past 5s so the timer fires (and deletion starts)
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    // Now call onUndo — too late, deletion already in progress
    onUndo();

    expect(mockShowToast).toHaveBeenCalledWith("Deletion already in progress", {
      type: "info",
    });
  });

  it("cancels deletion when onUndo called before timer fires", async () => {
    setupMultipleWorkspaces();
    render(<WorkspaceTree defaultCollapsed={false} />);

    triggerDelete("workspace-a");
    const onUndo = captureOnUndo();

    // Undo immediately before the 5s timer fires
    onUndo();

    // Advance timers — deleteWorkspace should NOT be called
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    expect(mockDeleteWorkspace).not.toHaveBeenCalled();

    // Should show restored toast
    expect(mockShowToast).toHaveBeenCalledWith(
      'Workspace "workspace-a" restored',
      { type: "info" },
    );

    // Should trigger refetch to restore workspace in the list
    expect(mockRefetch).toHaveBeenCalled();
  });

  it("resets deletionPendingRef after successful deletion — allows subsequent delete", async () => {
    setupMultipleWorkspaces();
    render(<WorkspaceTree defaultCollapsed={false} />);

    triggerDelete("workspace-a");

    // Advance past 5s so deletion completes
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    // Wait for the deleteWorkspace promise to resolve
    await act(async () => {
      await vi.runAllTimersAsync();
    });

    // Now another delete should be allowed (deletionPendingRef should be false)
    // Re-render with fresh state to confirm the ref was reset
    // We trigger a second delete on workspace-b
    fireEvent.click(screen.getByTestId("workspace-overflow-workspace-b"));
    fireEvent.click(screen.getByTestId("workspace-context-menu-remove"));
    const confirmButton = screen.getByRole("button", { name: /remove/i });
    fireEvent.click(confirmButton);

    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    // Both deletes should have fired
    expect(mockDeleteWorkspace).toHaveBeenCalledTimes(2);
    expect(mockDeleteWorkspace).toHaveBeenCalledWith("uuid-ws-a");
    expect(mockDeleteWorkspace).toHaveBeenCalledWith("uuid-ws-b");
  });

  it("resets deletionPendingRef after failed deletion — allows retry", async () => {
    mockDeleteWorkspace.mockRejectedValueOnce(new Error("Network error"));
    setupMultipleWorkspaces();
    render(<WorkspaceTree defaultCollapsed={false} />);

    triggerDelete("workspace-a");

    // Advance past 5s so the timer fires, using async version to flush microtasks
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    // Error toast should have been shown
    expect(mockShowToast).toHaveBeenCalledWith("Network error", {
      type: "error",
    });

    // Should be able to try again (deletionPendingRef reset)
    mockDeleteWorkspace.mockResolvedValue({});
    fireEvent.click(screen.getByTestId("workspace-overflow-workspace-a"));
    fireEvent.click(screen.getByTestId("workspace-context-menu-remove"));
    const confirmButton = screen.getByRole("button", { name: /remove/i });
    fireEvent.click(confirmButton);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(mockDeleteWorkspace).toHaveBeenCalledTimes(2);
  });

  it("nulls deleteTimerRef inside setTimeout callback — onUndo sees timer already fired", async () => {
    setupMultipleWorkspaces();
    render(<WorkspaceTree defaultCollapsed={false} />);

    triggerDelete("workspace-a");
    const onUndo = captureOnUndo();

    // Advance past 5s — timer fires, deleteTimerRef.current should be nulled inside callback
    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    // onUndo after timer fired: deleteTimerRef is null so it takes the "already in progress" path
    onUndo();

    expect(mockShowToast).toHaveBeenCalledWith("Deletion already in progress", {
      type: "info",
    });
  });
});
