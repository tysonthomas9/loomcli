/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MoveIssueDialog component.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";
import type { WorkspaceSummary } from "@/api/workspace";
import type { IssueWithDependencyMetadata } from "@/types";

import { MoveIssueDialog } from "../MoveIssueDialog";

// Hoist mock for useRegisterEscapeLayer
const { mockUseRegisterEscapeLayer } = vi.hoisted(() => ({
  mockUseRegisterEscapeLayer: vi.fn(),
}));

vi.mock("@/hooks", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/hooks")>();
  return {
    ...orig,
    useRegisterEscapeLayer: mockUseRegisterEscapeLayer,
  };
});

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-001",
    title: "Test issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  } as Issue;
}

/**
 * Create test workspaces including the current one.
 */
function createTestWorkspaces(): WorkspaceSummary[] {
  return [
    {
      id: "alpha",
      name: "alpha",
      path: "/ws/alpha",
      active: true,
      repo_count: 2,
      is_default: false,
    },
    {
      id: "beta",
      name: "beta",
      path: "/ws/beta",
      active: false,
      repo_count: 1,
      is_default: false,
    },
    {
      id: "gamma",
      name: "gamma",
      path: "/ws/gamma",
      active: false,
      repo_count: 3,
      is_default: false,
    },
  ];
}

describe("MoveIssueDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("rendering", () => {
    it("does not render when isOpen is false", () => {
      render(
        <MoveIssueDialog
          isOpen={false}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("move-dialog-overlay"),
      ).not.toBeInTheDocument();
    });

    it("renders dialog when isOpen is true", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("move-dialog-overlay")).toBeInTheDocument();
      expect(screen.getByText("Move to workspace")).toBeInTheDocument();
    });

    it("renders workspace list excluding current workspace", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const select = screen.getByTestId(
        "move-workspace-select",
      ) as HTMLSelectElement;
      const options = Array.from(select.options).map((o) => o.value);

      // Should have placeholder + beta + gamma (not alpha)
      expect(options).toContain("beta");
      expect(options).toContain("gamma");
      expect(options).not.toContain("alpha");
    });

    it("shows dependency warnings when dependencies exist", () => {
      const openDeps: IssueWithDependencyMetadata[] = [
        {
          ...createTestIssue({ id: "dep-001", title: "Blocking dep" }),
          dependency_type: "blocks",
          status: "open",
        } as IssueWithDependencyMetadata,
        {
          ...createTestIssue({ id: "dep-002", title: "Another dep" }),
          dependency_type: "blocks",
          status: "in_progress",
        } as IssueWithDependencyMetadata,
      ];

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          dependencies={openDeps}
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("move-warnings")).toBeInTheDocument();
      expect(
        screen.getByText(/2 dependencies will be broken/),
      ).toBeInTheDocument();
    });

    it("does not show dependency warnings when all dependencies are closed", () => {
      const closedDeps: IssueWithDependencyMetadata[] = [
        {
          ...createTestIssue({ id: "dep-001", title: "Done dep" }),
          dependency_type: "blocks",
          status: "closed",
        } as IssueWithDependencyMetadata,
      ];

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue({ assignee: "" })}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          dependencies={closedDeps}
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.queryByTestId("move-warnings")).not.toBeInTheDocument();
    });

    it("shows agent warning when issue has active assignee", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue({ assignee: "agent-42" })}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("move-warnings")).toBeInTheDocument();
      expect(screen.getByText(/agent-42/)).toBeInTheDocument();
      expect(screen.getByText(/will not stop the agent/)).toBeInTheDocument();
    });

    it("does not show agent warning when issue has no assignee", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue({ assignee: "" })}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.queryByTestId("move-warnings")).not.toBeInTheDocument();
    });
  });

  describe("interactions", () => {
    it("calls onConfirm with selected workspace", async () => {
      const onConfirm = vi.fn().mockResolvedValue(undefined);

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={onConfirm}
          onCancel={vi.fn()}
        />,
      );

      // Select a workspace
      fireEvent.change(screen.getByTestId("move-workspace-select"), {
        target: { value: "beta" },
      });

      // Click confirm
      fireEvent.click(screen.getByTestId("move-dialog-confirm"));

      await waitFor(() => {
        expect(onConfirm).toHaveBeenCalledWith("beta");
      });
    });

    it("cancel button calls onCancel", () => {
      const onCancel = vi.fn();

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByTestId("move-dialog-cancel"));
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("confirm button is disabled when no workspace selected", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const confirmBtn = screen.getByTestId("move-dialog-confirm");
      expect(confirmBtn).toBeDisabled();
    });

    it("confirm button shows loading text when moving", async () => {
      // Create a promise that we control
      let resolveMove: () => void;
      const movePromise = new Promise<void>((resolve) => {
        resolveMove = resolve;
      });
      const onConfirm = vi.fn().mockReturnValue(movePromise);

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={onConfirm}
          onCancel={vi.fn()}
        />,
      );

      // Select a workspace
      fireEvent.change(screen.getByTestId("move-workspace-select"), {
        target: { value: "beta" },
      });

      // Click confirm
      fireEvent.click(screen.getByTestId("move-dialog-confirm"));

      // Should show "Moving..." while in progress
      await waitFor(() => {
        expect(screen.getByTestId("move-dialog-confirm")).toHaveTextContent(
          "Moving...",
        );
      });

      // Resolve the move
      resolveMove!();

      // Should go back to "Move"
      await waitFor(() => {
        expect(screen.getByTestId("move-dialog-confirm")).toHaveTextContent(
          "Move",
        );
      });
    });

    it("auto-selects workspace when only one available", () => {
      // Only two workspaces total: alpha (current) and beta
      const twoWorkspaces: WorkspaceSummary[] = [
        {
          id: "alpha",
          name: "alpha",
          path: "/ws/alpha",
          active: true,
          repo_count: 2,
          is_default: false,
        },
        {
          id: "beta",
          name: "beta",
          path: "/ws/beta",
          active: false,
          repo_count: 1,
          is_default: false,
        },
      ];

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={twoWorkspaces}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const select = screen.getByTestId(
        "move-workspace-select",
      ) as HTMLSelectElement;
      expect(select.value).toBe("beta");

      // Confirm button should be enabled since workspace is auto-selected
      expect(screen.getByTestId("move-dialog-confirm")).not.toBeDisabled();
    });

    it("calls onCancel when overlay is clicked", () => {
      const onCancel = vi.fn();

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByTestId("move-dialog-overlay"));
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("does not call onCancel when dialog content is clicked", () => {
      const onCancel = vi.fn();

      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      const dialog = screen.getByRole("dialog");
      fireEvent.click(dialog);
      expect(onCancel).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it("has dialog role with aria-modal and aria-labelledby", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const dialog = screen.getByRole("dialog");
      expect(dialog).toBeInTheDocument();
      expect(dialog).toHaveAttribute("aria-modal", "true");
      expect(dialog).toHaveAttribute("aria-labelledby", "move-dialog-title");
    });

    it("buttons have type=button", () => {
      render(
        <MoveIssueDialog
          isOpen={true}
          issue={createTestIssue()}
          workspaces={createTestWorkspaces()}
          currentWorkspace="alpha"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("move-dialog-confirm")).toHaveAttribute(
        "type",
        "button",
      );
      expect(screen.getByTestId("move-dialog-cancel")).toHaveAttribute(
        "type",
        "button",
      );
    });
  });
});
