/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentStatusBadge component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";

import { AgentStatusBadge } from "../AgentStatusBadge";
import type { LoomAgentStatus } from "@/types";

// Mutable mock agents array — tests configure via mockGetAgentByName helper
let mockAgents: LoomAgentStatus[] = [];

// Mock zustand's useStore — apply selector to mock agent store state
vi.mock("zustand", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useStore: (_store: unknown, selector: (s: any) => unknown) =>
    selector({ agents: mockAgents }),
}));

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useAgentStoreInstance: () => ({}) };
});

// Compatibility helper — tests call mockGetAgentByName.mockReturnValue(agent)
const mockGetAgentByName = {
  mockReturnValue(agent: LoomAgentStatus | undefined) {
    mockAgents = agent ? [agent] : [];
  },
};

// Mock fetchGitStatus
vi.mock("@/api/workspace", () => ({
  fetchGitStatus: vi.fn().mockRejectedValue(new Error("not available")),
}));

/**
 * Helper to build an agent status object.
 */
function makeAgent(name: string, status: string): LoomAgentStatus {
  return {
    name,
    branch: `worktrees/${name}`,
    status,
    ahead: 0,
    behind: 0,
  };
}

describe("AgentStatusBadge", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockAgents = [];
  });

  describe("rendering", () => {
    it("renders badge with correct status for working agent", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );

      render(<AgentStatusBadge agentName="nova" />);

      const badge = screen.getByTestId("agent-status-badge");
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveAttribute("data-status", "working");
      expect(screen.getByText("Working")).toBeInTheDocument();
    });

    it("renders badge with planning status", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "planning: LOOM-200 (2m)"),
      );

      render(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("Planning")).toBeInTheDocument();
      expect(screen.getByTestId("agent-status-badge")).toHaveAttribute(
        "data-status",
        "planning",
      );
    });

    it("renders badge with error status", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "error: something failed"),
      );

      render(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("Error")).toBeInTheDocument();
      expect(screen.getByTestId("agent-status-badge")).toHaveAttribute(
        "data-status",
        "error",
      );
    });

    it("renders badge with done status", () => {
      mockGetAgentByName.mockReturnValue(makeAgent("nova", "done"));

      render(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("Done")).toBeInTheDocument();
    });

    it("renders badge with ready status", () => {
      mockGetAgentByName.mockReturnValue(makeAgent("nova", "ready"));

      render(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("Ready")).toBeInTheDocument();
    });

    it("shows duration when available", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );

      render(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("5m")).toBeInTheDocument();
    });

    it("does not show duration when not available", () => {
      mockGetAgentByName.mockReturnValue(makeAgent("nova", "ready"));

      render(<AgentStatusBadge agentName="nova" />);

      // Should not have any duration element
      const badge = screen.getByTestId("agent-status-badge");
      expect(badge.querySelectorAll("[class*='duration']")).toHaveLength(0);
    });
  });

  describe("agent not found", () => {
    it("does not render when agent not found", () => {
      mockGetAgentByName.mockReturnValue(undefined);

      const { container } = render(
        <AgentStatusBadge agentName="unknown-agent" />,
      );

      expect(container.firstChild).toBeNull();
    });

    it("does not render when loom server disconnected and agent not found", () => {
      mockGetAgentByName.mockReturnValue(undefined);

      const { container } = render(<AgentStatusBadge agentName="nova" />);

      expect(container.firstChild).toBeNull();
    });
  });

  describe("interaction", () => {
    it("calls onOpenTerminal when clicked", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );
      const onOpenTerminal = vi.fn();

      render(
        <AgentStatusBadge agentName="nova" onOpenTerminal={onOpenTerminal} />,
      );

      fireEvent.click(screen.getByTestId("agent-status-badge"));
      expect(onOpenTerminal).toHaveBeenCalledWith("nova");
    });

    it("calls onOpenTerminal on Enter key", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );
      const onOpenTerminal = vi.fn();

      render(
        <AgentStatusBadge agentName="nova" onOpenTerminal={onOpenTerminal} />,
      );

      fireEvent.keyDown(screen.getByTestId("agent-status-badge"), {
        key: "Enter",
      });
      expect(onOpenTerminal).toHaveBeenCalledWith("nova");
    });

    it("calls onOpenTerminal on Space key", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );
      const onOpenTerminal = vi.fn();

      render(
        <AgentStatusBadge agentName="nova" onOpenTerminal={onOpenTerminal} />,
      );

      fireEvent.keyDown(screen.getByTestId("agent-status-badge"), {
        key: " ",
      });
      expect(onOpenTerminal).toHaveBeenCalledWith("nova");
    });

    it("does not crash when onOpenTerminal is not provided", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );

      render(<AgentStatusBadge agentName="nova" />);

      // Should not throw
      fireEvent.click(screen.getByTestId("agent-status-badge"));
    });
  });

  describe("accessibility", () => {
    it("has correct role and aria-label", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );

      render(<AgentStatusBadge agentName="nova" />);

      const badge = screen.getByTestId("agent-status-badge");
      expect(badge).toHaveAttribute("role", "button");
      expect(badge).toHaveAttribute("tabIndex", "0");
      expect(badge.getAttribute("aria-label")).toContain("Agent nova");
      expect(badge.getAttribute("aria-label")).toContain("Working");
      expect(badge.getAttribute("aria-label")).toContain("5m");
    });

    it("aria-label omits duration when not available", () => {
      mockGetAgentByName.mockReturnValue(makeAgent("nova", "ready"));

      render(<AgentStatusBadge agentName="nova" />);

      const badge = screen.getByTestId("agent-status-badge");
      expect(badge.getAttribute("aria-label")).toContain("Agent nova");
      expect(badge.getAttribute("aria-label")).toContain("Ready");
      expect(badge.getAttribute("aria-label")).not.toContain("(");
    });
  });

  describe("status updates", () => {
    it("updates when agent status changes", () => {
      mockGetAgentByName.mockReturnValue(
        makeAgent("nova", "working: LOOM-100 (5m)"),
      );

      const { rerender } = render(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("Working")).toBeInTheDocument();

      // Simulate status change
      mockGetAgentByName.mockReturnValue(makeAgent("nova", "done"));

      rerender(<AgentStatusBadge agentName="nova" />);

      expect(screen.getByText("Done")).toBeInTheDocument();
    });
  });
});
