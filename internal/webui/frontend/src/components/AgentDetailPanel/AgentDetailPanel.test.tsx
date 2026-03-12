/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentDetailPanel component.
 * Covers Path field rendering and OpenInEditor integration in the Agent Info section.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus, LoomTaskInfo } from "@/types";

import { AgentDetailPanel } from "./AgentDetailPanel";

// Mock the useAgentTerminalLogs hook to prevent WebSocket/API calls in tests
vi.mock("@/hooks", () => ({
  useAgentTerminalLogs: () => ({
    mode: "idle",
    chunks: [],
    state: "disconnected",
    error: null,
    resetVersion: 0,
    refresh: vi.fn(),
    resize: vi.fn(),
    sendInput: vi.fn(),
    loadOlderLogs: vi.fn(),
    hasMoreLines: false,
    isLoadingMore: false,
  }),
}));

// Mock LogViewer to avoid terminal rendering complexity
vi.mock("../LogViewer", () => ({
  LogViewer: () => <div data-testid="log-viewer-mock" />,
}));

// Mock OpenInEditor to avoid its hook dependencies (useEditors)
vi.mock("../OpenInEditor", () => ({
  OpenInEditor: ({ path }: { path: string }) => (
    <div data-testid="open-in-editor" data-path={path} />
  ),
}));

/** Helper to build a minimal agent object. */
function makeAgent(overrides: Partial<LoomAgentStatus> = {}): LoomAgentStatus {
  return {
    name: "falcon",
    branch: "webui/falcon",
    status: "ready",
    ahead: 0,
    behind: 0,
    ...overrides,
  };
}

/** Default props for the panel. */
function renderPanel(
  agentOverrides: Partial<LoomAgentStatus> = {},
  agentTasks: Record<string, LoomTaskInfo> = {},
) {
  const agent = makeAgent(agentOverrides);
  return render(
    <AgentDetailPanel
      isOpen={true}
      agentName={agent.name}
      agents={[agent]}
      agentTasks={agentTasks}
      onClose={vi.fn()}
    />,
  );
}

describe("AgentDetailPanel", () => {
  describe("path field in Agent Info section", () => {
    it("renders Path when agent has a path", () => {
      renderPanel({ path: "worktrees/cobalt" });

      expect(screen.getByText("Path")).toBeInTheDocument();
      expect(screen.getByText("worktrees/cobalt")).toBeInTheDocument();
    });

    it("does not render Path row when agent has no path", () => {
      renderPanel({ path: undefined });

      expect(screen.queryByText("Path")).not.toBeInTheDocument();
    });

    it("does not render Path row when path is empty string", () => {
      renderPanel({ path: "" });

      expect(screen.queryByText("Path")).not.toBeInTheDocument();
    });

    it("renders Path alongside other Agent Info fields", () => {
      renderPanel({
        path: "worktrees/falcon",
        branch: "feature-branch",
        status: "working: bd-123 (5m)",
      });

      // Path should be present
      expect(screen.getByText("Path")).toBeInTheDocument();
      expect(screen.getByText("worktrees/falcon")).toBeInTheDocument();

      // Other info fields should also be present in the Agent Info section
      expect(screen.getByText("Branch")).toBeInTheDocument();
      expect(screen.getByText("Status")).toBeInTheDocument();
      // Branch value appears in both metadata bar and Agent Info, so use getAllByText
      expect(
        screen.getAllByText("feature-branch").length,
      ).toBeGreaterThanOrEqual(1);
    });
  });

  describe("OpenInEditor in Agent Info section", () => {
    it("renders OpenInEditor when agent has worktree_path", () => {
      renderPanel({ worktree_path: "/home/user/worktrees/falcon" });

      const openInEditor = screen.getByTestId("open-in-editor");
      expect(openInEditor).toBeInTheDocument();
      expect(openInEditor).toHaveAttribute(
        "data-path",
        "/home/user/worktrees/falcon",
      );
    });

    it("does not render OpenInEditor when worktree_path is undefined", () => {
      renderPanel({ worktree_path: undefined });

      expect(screen.queryByTestId("open-in-editor")).not.toBeInTheDocument();
    });

    it("does not render OpenInEditor when worktree_path is empty string", () => {
      renderPanel({ worktree_path: "" });

      expect(screen.queryByTestId("open-in-editor")).not.toBeInTheDocument();
    });
  });
});
