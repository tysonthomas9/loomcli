/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceTree rename functionality.
 * Covers workspace entries rendering, context menu integration,
 * inline rename input, API calls, and error handling.
 */

import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";
import { WorkspaceTree } from "../WorkspaceTree";

import type { WorkspaceSummary } from "@/api/workspace";

// ── Mock renameWorkspace API ─────────────────────────────────────────────────

const mockRenameWorkspace = vi.fn();

vi.mock("@/api/workspace", () => ({
  renameWorkspace: (...args: unknown[]) => mockRenameWorkspace(...args),
}));

// ── Mock CSS modules (vitest auto-handles, but be explicit for class queries) ─

vi.mock("../WorkspaceContextMenu.module.css", () => ({
  default: {
    menu: "menu",
    menuItem: "menuItem",
    menuItemIcon: "menuItemIcon",
  },
}));

// ── Hook mocks ───────────────────────────────────────────────────────────────

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
  refetch: vi.fn(),
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
let agentOverride: Partial<typeof defaultAgentContext> = {};

vi.mock("@/hooks", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentContext: () => ({ ...defaultAgentContext, ...agentOverride }),
  useWorkspaceContext: () => ({
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
  }),
  useToast: () => ({ showToast: vi.fn() }),
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
}));

// ── Helpers ──────────────────────────────────────────────────────────────────

const twoWorkspaces: WorkspaceSummary[] = [
  { name: "workspace-a", path: "/ws/a", active: true, repo_count: 3 },
  { name: "workspace-b", path: "/ws/b", active: false, repo_count: 2 },
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
  };
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe("WorkspaceTree – workspace entries and rename", () => {
  beforeEach(() => {
    localStorage.clear();
    reposOverride = {};
    agentOverride = {};
    mockRenameWorkspace.mockReset();
  });

  // ── Workspace entries rendering ──────────────────────────────────────────

  describe("workspace entries rendering", () => {
    it("renders workspace entries when more than 1 workspace exists", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("Workspaces")).toBeInTheDocument();
      expect(screen.getByText("workspace-a")).toBeInTheDocument();
      expect(screen.getByText("workspace-b")).toBeInTheDocument();
    });

    it("does not render workspace section when only 1 workspace exists", () => {
      reposOverride = {
        workspace: {
          workspaces: [
            {
              name: "only-one",
              path: "/ws/one",
              active: true,
              repo_count: 1,
            },
          ],
        },
        repos: twoRepos,
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.queryByText("Workspaces")).not.toBeInTheDocument();
      expect(screen.queryByText("only-one")).not.toBeInTheDocument();
    });

    it("shows active badge for active workspace", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("active")).toBeInTheDocument();
    });

    it("shows repo count for each workspace entry", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      // workspace-a has repo_count 3
      const wsA = screen.getByText("workspace-a").closest("div")!;
      expect(wsA.textContent).toContain("3");

      // workspace-b has repo_count 2
      const wsB = screen.getByText("workspace-b").closest("div")!;
      expect(wsB.textContent).toContain("2");
    });
  });

  // ── Overflow button ────────────────────────────────────────────────────────

  describe("overflow button", () => {
    it("renders overflow button for each workspace entry", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByTestId("workspace-overflow-workspace-a"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("workspace-overflow-workspace-b"),
      ).toBeInTheDocument();
    });

    it("has correct aria-label", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByTestId("workspace-overflow-workspace-a"),
      ).toHaveAttribute("aria-label", "More actions for workspace-a");
    });

    it("clicking overflow button opens context menu", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.queryByTestId("workspace-context-menu"),
      ).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId("workspace-overflow-workspace-a"));

      expect(screen.getByTestId("workspace-context-menu")).toBeInTheDocument();
    });
  });

  // ── Context menu via right-click ───────────────────────────────────────────

  describe("context menu via right-click", () => {
    it("opens context menu on right-click of workspace entry", () => {
      setupMultipleWorkspaces();

      render(<WorkspaceTree defaultCollapsed={false} />);

      const entry = screen.getByText("workspace-a").closest("div")!;
      fireEvent.contextMenu(entry);

      expect(screen.getByTestId("workspace-context-menu")).toBeInTheDocument();
      expect(
        screen.getByTestId("workspace-context-menu-rename"),
      ).toBeInTheDocument();
    });
  });

  // ── Rename flow ────────────────────────────────────────────────────────────

  describe("rename flow", () => {
    function openRenameForWorkspaceA() {
      setupMultipleWorkspaces();
      render(<WorkspaceTree defaultCollapsed={false} />);

      // Open context menu via overflow button
      fireEvent.click(screen.getByTestId("workspace-overflow-workspace-a"));

      // Click Rename
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));
    }

    it("selecting Rename from context menu enters edit mode", () => {
      openRenameForWorkspaceA();

      // Rename input should appear
      expect(screen.getByTestId("workspace-rename-input")).toBeInTheDocument();

      // The input should have the current workspace name
      const input = screen.getByTestId(
        "workspace-rename-input",
      ) as HTMLInputElement;
      expect(input.value).toBe("workspace-a");
    });

    it("hides overflow button when in edit mode", () => {
      openRenameForWorkspaceA();

      // Overflow button for workspace-a should be hidden during editing
      expect(
        screen.queryByTestId("workspace-overflow-workspace-a"),
      ).not.toBeInTheDocument();

      // But workspace-b overflow should still be visible
      expect(
        screen.getByTestId("workspace-overflow-workspace-b"),
      ).toBeInTheDocument();
    });

    it("Enter key in rename input calls the API with correct args", async () => {
      mockRenameWorkspace.mockResolvedValue({});

      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "new-name" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      await waitFor(() => {
        expect(mockRenameWorkspace).toHaveBeenCalledTimes(1);
        expect(mockRenameWorkspace).toHaveBeenCalledWith(
          "workspace-a",
          "new-name",
        );
      });
    });

    it("Escape key cancels rename without calling API", () => {
      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "new-name" } });
      fireEvent.keyDown(input, { key: "Escape" });

      expect(mockRenameWorkspace).not.toHaveBeenCalled();

      // Should exit edit mode (input gone, name restored)
      expect(
        screen.queryByTestId("workspace-rename-input"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("workspace-a")).toBeInTheDocument();
    });

    it("does not call API when name is unchanged", async () => {
      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      // Don't change the value, just press Enter
      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      expect(mockRenameWorkspace).not.toHaveBeenCalled();
    });

    it("shows inline error for empty name", async () => {
      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      expect(screen.getByTestId("workspace-rename-error")).toBeInTheDocument();
      expect(screen.getByTestId("workspace-rename-error")).toHaveTextContent(
        "Name cannot be empty",
      );
      expect(mockRenameWorkspace).not.toHaveBeenCalled();
    });

    it("shows inline error for whitespace-only name", async () => {
      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "   " } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      expect(screen.getByTestId("workspace-rename-error")).toHaveTextContent(
        "Name cannot be empty",
      );
      expect(mockRenameWorkspace).not.toHaveBeenCalled();
    });

    it("shows API error message inline on failure", async () => {
      mockRenameWorkspace.mockRejectedValue(
        new Error("workspace name already exists"),
      );

      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "workspace-b" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      await waitFor(() => {
        expect(
          screen.getByTestId("workspace-rename-error"),
        ).toBeInTheDocument();
        expect(screen.getByTestId("workspace-rename-error")).toHaveTextContent(
          "workspace name already exists",
        );
      });

      // Should stay in edit mode
      expect(screen.getByTestId("workspace-rename-input")).toBeInTheDocument();
    });

    it("shows generic error for non-Error exceptions", async () => {
      mockRenameWorkspace.mockRejectedValue("string error");

      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "new-name" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      await waitFor(() => {
        expect(screen.getByTestId("workspace-rename-error")).toHaveTextContent(
          "Failed to rename workspace",
        );
      });
    });

    it("error element has role=alert for accessibility", async () => {
      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      expect(screen.getByRole("alert")).toHaveTextContent(
        "Name cannot be empty",
      );
    });

    it("calls refetch after successful rename", async () => {
      const mockRefetch = vi.fn();
      reposOverride = {
        workspace: { workspaces: twoWorkspaces },
        repos: twoRepos,
        refetch: mockRefetch,
      };
      mockRenameWorkspace.mockResolvedValue({});

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Open context menu and start rename
      fireEvent.click(screen.getByTestId("workspace-overflow-workspace-a"));
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "renamed" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      await waitFor(() => {
        expect(mockRefetch).toHaveBeenCalled();
      });
    });

    it("saves on blur", async () => {
      mockRenameWorkspace.mockResolvedValue({});

      openRenameForWorkspaceA();

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "blurred-name" } });

      await act(async () => {
        fireEvent.blur(input);
      });

      await waitFor(() => {
        expect(mockRenameWorkspace).toHaveBeenCalledWith(
          "workspace-a",
          "blurred-name",
        );
      });
    });
  });
});
