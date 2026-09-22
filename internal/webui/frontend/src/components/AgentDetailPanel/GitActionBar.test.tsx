/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import type { GitStatus } from "@/api/workspace";
import type { UseGitActionsReturn } from "@/hooks/workspace";
import type { ParsedLoomStatus } from "@/types";

import { GitActionBar } from "./GitActionBar";

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

function makeActions(): UseGitActionsReturn {
  return {
    pull: vi.fn(),
    createPR: vi.fn(),
    reset: vi.fn(),
    updateTarget: vi.fn(),
    pullState: { isLoading: false, error: null },
    prState: { isLoading: false, error: null },
    resetState: { isLoading: false, error: null },
    targetState: { isLoading: false, error: null },
    anyLoading: false,
  };
}

const ready: ParsedLoomStatus = { type: "ready" };

describe("GitActionBar", () => {
  it("shows retained actions without direct push or sync controls", () => {
    render(
      <GitActionBar
        agentName="nova"
        gitStatus={makeGitStatus()}
        agentStatus={ready}
        actions={makeActions()}
      />,
    );

    expect(screen.getByText("Pull (2)")).toBeInTheDocument();
    expect(screen.getByText("Create PR")).toBeInTheDocument();
    expect(screen.getByText("Reset")).toBeInTheDocument();
    expect(screen.queryByText(/^Push/)).not.toBeInTheDocument();
    expect(screen.queryByText("Sync")).not.toBeInTheDocument();
  });

  it("pulls only when the worktree is behind", () => {
    const actions = makeActions();
    const { rerender } = render(
      <GitActionBar
        agentName="nova"
        gitStatus={makeGitStatus({ behind: 0 })}
        agentStatus={ready}
        actions={actions}
      />,
    );
    expect(screen.getByText("Pull")).toBeDisabled();

    rerender(
      <GitActionBar
        agentName="nova"
        gitStatus={makeGitStatus({ behind: 1 })}
        agentStatus={ready}
        actions={actions}
      />,
    );
    fireEvent.click(screen.getByText("Pull (1)"));
    expect(actions.pull).toHaveBeenCalledOnce();
  });

  it("creates a PR for the selected target", () => {
    const actions = makeActions();
    render(
      <GitActionBar
        agentName="nova"
        gitStatus={makeGitStatus({ target_branch: "v5" })}
        agentStatus={ready}
        actions={actions}
      />,
    );

    fireEvent.click(screen.getByText("Create PR"));
    const target = screen.getByRole("textbox", { name: "Target branch" });
    fireEvent.change(target, { target: { value: "release" } });
    fireEvent.click(screen.getByText("Create"));
    expect(actions.createPR).toHaveBeenCalledWith("release");
  });

  it("requires confirmation before reset", () => {
    const actions = makeActions();
    render(
      <GitActionBar
        agentName="nova"
        gitStatus={makeGitStatus()}
        agentStatus={ready}
        actions={actions}
      />,
    );

    fireEvent.click(screen.getByText("Reset"));
    expect(actions.reset).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("Confirm Reset"));
    expect(actions.reset).toHaveBeenCalledOnce();
  });
});
