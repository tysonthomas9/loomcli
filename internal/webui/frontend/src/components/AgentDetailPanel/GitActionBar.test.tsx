/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GitActionBar component.
 * Covers button rendering, disabled states (ahead=0, behind=0, agent working),
 * spinner display, inline PR form, and reset confirmation.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { GitStatus } from "@/api/git";
import type { ParsedLoomStatus } from "@/types";
import type { UseGitActionsReturn } from "@/hooks/workspace";

import { GitActionBar } from "./GitActionBar";

/** Create a default git status object. */
function makeGitStatus(overrides: Partial<GitStatus> = {}): GitStatus {
  return {
    branch: "feature-x",
    target_branch: "main",
    is_clean: true,
    ahead: 3,
    behind: 2,
    changed_files: [],
    conflicted_files: [],
    has_conflicts: false,
    stash_count: 0,
    ...overrides,
  };
}

/** Create a default parsed loom status. */
function makeAgentStatus(
  overrides: Partial<ParsedLoomStatus> = {},
): ParsedLoomStatus {
  return {
    type: "ready",
    ...overrides,
  };
}

/** Create a default actions mock. */
function makeActions(
  overrides: Partial<UseGitActionsReturn> = {},
): UseGitActionsReturn {
  return {
    push: vi.fn(),
    pull: vi.fn(),
    sync: vi.fn(),
    createPR: vi.fn(),
    reset: vi.fn(),
    updateTarget: vi.fn(),
    pushState: { isLoading: false, error: null },
    pullState: { isLoading: false, error: null },
    syncState: { isLoading: false, error: null },
    prState: { isLoading: false, error: null },
    resetState: { isLoading: false, error: null },
    targetState: { isLoading: false, error: null },
    anyLoading: false,
    ...overrides,
  };
}

describe("GitActionBar", () => {
  let actions: UseGitActionsReturn;

  beforeEach(() => {
    actions = makeActions();
  });

  describe("button rendering", () => {
    it("renders all five action buttons", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText(/^Push/)).toBeInTheDocument();
      expect(screen.getByText(/^Pull/)).toBeInTheDocument();
      expect(screen.getByText("Sync")).toBeInTheDocument();
      expect(screen.getByText("Create PR")).toBeInTheDocument();
      expect(screen.getByText("Reset")).toBeInTheDocument();
    });

    it("shows ahead count on Push button when ahead > 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 5 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push (5)")).toBeInTheDocument();
    });

    it("does not show count on Push button when ahead = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push")).toBeInTheDocument();
      expect(screen.queryByText(/Push \(/)).not.toBeInTheDocument();
    });

    it("shows behind count on Pull button when behind > 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ behind: 4 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Pull (4)")).toBeInTheDocument();
    });

    it("does not show count on Pull button when behind = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Pull")).toBeInTheDocument();
      expect(screen.queryByText(/Pull \(/)).not.toBeInTheDocument();
    });
  });

  describe("disabled states", () => {
    it("disables Push button when ahead = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0, behind: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push")).toBeDisabled();
    });

    it("disables Pull button when behind = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Pull")).toBeDisabled();
    });

    it("disables Sync button when both ahead and behind = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0, behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Sync")).toBeDisabled();
    });

    it("enables Sync button when ahead > 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Sync")).not.toBeDisabled();
    });

    it("enables Sync button when behind > 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0, behind: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Sync")).not.toBeDisabled();
    });

    it("disables Create PR button when ahead = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Create PR")).toBeDisabled();
    });

    it("disables all buttons when agent is working", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 5, behind: 3 })}
          agentStatus={makeAgentStatus({ type: "working" })}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push (5)")).toBeDisabled();
      expect(screen.getByText("Pull (3)")).toBeDisabled();
      expect(screen.getByText("Sync")).toBeDisabled();
      expect(screen.getByText("Create PR")).toBeDisabled();
      expect(screen.getByText("Reset")).toBeDisabled();
    });

    it("disables all buttons when agent is planning", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, behind: 1 })}
          agentStatus={makeAgentStatus({ type: "planning" })}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push (1)")).toBeDisabled();
      expect(screen.getByText("Pull (1)")).toBeDisabled();
      expect(screen.getByText("Sync")).toBeDisabled();
      expect(screen.getByText("Create PR")).toBeDisabled();
      expect(screen.getByText("Reset")).toBeDisabled();
    });

    it("disables all buttons when anyLoading is true", () => {
      const loadingActions = makeActions({ anyLoading: true });

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 5, behind: 3 })}
          agentStatus={makeAgentStatus()}
          actions={loadingActions}
        />,
      );

      expect(screen.getByText("Push (5)")).toBeDisabled();
      expect(screen.getByText("Pull (3)")).toBeDisabled();
      expect(screen.getByText("Sync")).toBeDisabled();
      expect(screen.getByText("Create PR")).toBeDisabled();
      expect(screen.getByText("Reset")).toBeDisabled();
    });

    it("shows 'Agent is actively working' title when agent is busy", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, behind: 1 })}
          agentStatus={makeAgentStatus({ type: "working" })}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push (1)")).toHaveAttribute(
        "title",
        "Agent is actively working",
      );
    });

    it("shows 'Nothing to push' title when ahead = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0, behind: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push")).toHaveAttribute(
        "title",
        "Nothing to push",
      );
    });

    it("shows 'Nothing to pull' title when behind = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Pull")).toHaveAttribute(
        "title",
        "Nothing to pull",
      );
    });

    it("shows 'Already in sync' title when both ahead and behind = 0", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0, behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Sync")).toHaveAttribute(
        "title",
        "Already in sync",
      );
    });

    it("Reset button is enabled when not busy and not loading", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 0, behind: 0 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Reset")).not.toBeDisabled();
    });
  });

  describe("null gitStatus", () => {
    it("treats ahead and behind as 0 when gitStatus is null", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={null}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      expect(screen.getByText("Push")).toBeDisabled();
      expect(screen.getByText("Pull")).toBeDisabled();
      expect(screen.getByText("Sync")).toBeDisabled();
      expect(screen.getByText("Create PR")).toBeDisabled();
    });
  });

  describe("spinner display", () => {
    it("shows spinner on Push button when push is loading", () => {
      const loadingActions = makeActions({
        pushState: { isLoading: true, error: null },
        anyLoading: true,
      });

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={loadingActions}
        />,
      );

      // The spinner is a span inside the Push button
      const pushButton = screen.getByText(/^Push/).closest("button");
      const spinner = pushButton?.querySelector('[class*="spinner"]');
      expect(spinner).toBeInTheDocument();
    });

    it("shows spinner on Pull button when pull is loading", () => {
      const loadingActions = makeActions({
        pullState: { isLoading: true, error: null },
        anyLoading: true,
      });

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={loadingActions}
        />,
      );

      const pullButton = screen.getByText(/^Pull/).closest("button");
      const spinner = pullButton?.querySelector('[class*="spinner"]');
      expect(spinner).toBeInTheDocument();
    });

    it("shows spinner on Sync button when sync is loading", () => {
      const loadingActions = makeActions({
        syncState: { isLoading: true, error: null },
        anyLoading: true,
      });

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={loadingActions}
        />,
      );

      const syncButton = screen.getByText("Sync").closest("button");
      const spinner = syncButton?.querySelector('[class*="spinner"]');
      expect(spinner).toBeInTheDocument();
    });

    it("shows spinner on Reset button when reset is loading", () => {
      const loadingActions = makeActions({
        resetState: { isLoading: true, error: null },
        anyLoading: true,
      });

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={loadingActions}
        />,
      );

      const resetButton = screen.getByText("Reset").closest("button");
      const spinner = resetButton?.querySelector('[class*="spinner"]');
      expect(spinner).toBeInTheDocument();
    });

    it("does not show spinner when not loading", () => {
      const { container } = render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      const spinners = container.querySelectorAll('[class*="spinner"]');
      expect(spinners).toHaveLength(0);
    });
  });

  describe("button click handlers", () => {
    it("calls actions.push when Push is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 2 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Push (2)"));

      expect(actions.push).toHaveBeenCalledTimes(1);
    });

    it("calls actions.pull when Pull is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ behind: 3 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Pull (3)"));

      expect(actions.pull).toHaveBeenCalledTimes(1);
    });

    it("calls actions.sync when Sync is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, behind: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Sync"));

      expect(actions.sync).toHaveBeenCalledTimes(1);
    });
  });

  describe("inline PR form", () => {
    it("shows PR form when Create PR button is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));

      expect(screen.getByText("Target branch")).toBeInTheDocument();
      expect(screen.getByDisplayValue("main")).toBeInTheDocument();
      expect(screen.getByText("Create")).toBeInTheDocument();
      expect(screen.getByText("Cancel")).toBeInTheDocument();
    });

    it("pre-fills target branch from gitStatus", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, target_branch: "develop" })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));

      expect(screen.getByDisplayValue("develop")).toBeInTheDocument();
    });

    it("calls createPR with target branch when Create is clicked", async () => {
      (actions.createPR as ReturnType<typeof vi.fn>).mockResolvedValue(
        undefined,
      );

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, target_branch: "main" })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));

      await act(async () => {
        fireEvent.click(screen.getByText("Create"));
      });

      expect(actions.createPR).toHaveBeenCalledWith("main");
    });

    it("closes PR form after submission", async () => {
      (actions.createPR as ReturnType<typeof vi.fn>).mockResolvedValue(
        undefined,
      );

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));
      expect(screen.getByText("Target branch")).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByText("Create"));
      });

      expect(screen.queryByText("Target branch")).not.toBeInTheDocument();
    });

    it("closes PR form when Cancel is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));
      expect(screen.getByText("Target branch")).toBeInTheDocument();

      fireEvent.click(screen.getByText("Cancel"));

      expect(screen.queryByText("Target branch")).not.toBeInTheDocument();
    });

    it("allows editing the target branch input", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, target_branch: "main" })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));

      const input = screen.getByDisplayValue("main");
      fireEvent.change(input, { target: { value: "develop" } });

      expect(screen.getByDisplayValue("develop")).toBeInTheDocument();
    });

    it("submits PR form on Enter key", async () => {
      (actions.createPR as ReturnType<typeof vi.fn>).mockResolvedValue(
        undefined,
      );

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1, target_branch: "main" })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));
      const input = screen.getByDisplayValue("main");

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      expect(actions.createPR).toHaveBeenCalledWith("main");
    });

    it("closes PR form on Escape key", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1 })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Create PR"));
      expect(screen.getByText("Target branch")).toBeInTheDocument();

      const input = screen.getByDisplayValue("main");
      fireEvent.keyDown(input, { key: "Escape" });

      expect(screen.queryByText("Target branch")).not.toBeInTheDocument();
    });

    it("shows spinner on Create PR button when PR is loading", () => {
      const loadingActions = makeActions({
        prState: { isLoading: true, error: null },
        anyLoading: true,
      });

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ ahead: 1 })}
          agentStatus={makeAgentStatus()}
          actions={loadingActions}
        />,
      );

      const prButton = screen.getByText("Create PR").closest("button");
      const spinner = prButton?.querySelector('[class*="spinner"]');
      expect(spinner).toBeInTheDocument();
    });
  });

  describe("reset confirmation", () => {
    it("shows confirmation dialog when Reset is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Reset"));

      expect(
        screen.getByText(/This will discard all local changes and reset to/),
      ).toBeInTheDocument();
      expect(screen.getByText("Confirm Reset")).toBeInTheDocument();
      // The Cancel in the reset section
      expect(screen.getAllByText("Cancel").length).toBeGreaterThanOrEqual(1);
    });

    it("shows target branch name in confirmation text", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus({ target_branch: "develop" })}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Reset"));

      expect(
        screen.getByText(
          "This will discard all local changes and reset to develop. Continue?",
        ),
      ).toBeInTheDocument();
    });

    it("uses 'main' as fallback target branch when gitStatus is null", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={null}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Reset"));

      expect(
        screen.getByText(
          "This will discard all local changes and reset to main. Continue?",
        ),
      ).toBeInTheDocument();
    });

    it("calls actions.reset when Confirm Reset is clicked", async () => {
      (actions.reset as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Reset"));

      await act(async () => {
        fireEvent.click(screen.getByText("Confirm Reset"));
      });

      expect(actions.reset).toHaveBeenCalledTimes(1);
    });

    it("closes confirmation dialog after confirming reset", async () => {
      (actions.reset as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);

      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Reset"));
      expect(screen.getByText("Confirm Reset")).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByText("Confirm Reset"));
      });

      expect(screen.queryByText("Confirm Reset")).not.toBeInTheDocument();
    });

    it("closes confirmation dialog when Cancel is clicked", () => {
      render(
        <GitActionBar
          agentName="nova"
          gitStatus={makeGitStatus()}
          agentStatus={makeAgentStatus()}
          actions={actions}
        />,
      );

      fireEvent.click(screen.getByText("Reset"));
      expect(screen.getByText("Confirm Reset")).toBeInTheDocument();

      // Click the Cancel button in the reset confirmation section
      const cancelButtons = screen.getAllByText("Cancel");
      fireEvent.click(cancelButtons[cancelButtons.length - 1]!);

      expect(screen.queryByText("Confirm Reset")).not.toBeInTheDocument();
    });
  });
});
