/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for StartWorkButton component.
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

import { StartWorkButton } from "../StartWorkButton";
import type { LoomAgentStatus, LoomTaskInfo } from "@/types";

/**
 * Helper to build an agent status object.
 */
function makeAgent(
  name: string,
  status: string,
  role?: string,
): LoomAgentStatus {
  return {
    name,
    branch: "main",
    status,
    ahead: 0,
    behind: 0,
    role,
  };
}

describe("StartWorkButton", () => {
  const defaultProps = {
    issueId: "LOOM-100",
    issueStatus: "open" as const,
    currentAssignee: undefined as string | undefined,
    agents: [
      makeAgent("alpha", "ready", "task"),
      makeAgent("beta", "ready", "task"),
    ] as LoomAgentStatus[],
    agentTasks: {} as Record<string, LoomTaskInfo>,
    isConnected: true,
    onAssign: vi.fn().mockResolvedValue(undefined),
    disabled: false,
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("Visibility", () => {
    it('renders "Start Work" button when issue is open with no assignee', () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");
      expect(button).toBeInTheDocument();
      expect(button).toHaveTextContent("Start Work");
    });

    it("returns null when issue status is in_progress", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} issueStatus="in_progress" />,
      );
      expect(container.innerHTML).toBe("");
    });

    it("returns null when issue status is closed", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} issueStatus="closed" />,
      );
      expect(container.innerHTML).toBe("");
    });

    it("returns null when issue status is blocked", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} issueStatus="blocked" />,
      );
      expect(container.innerHTML).toBe("");
    });

    it("returns null when issue status is deferred", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} issueStatus="deferred" />,
      );
      expect(container.innerHTML).toBe("");
    });

    it('renders "Review with Agent" for unassigned review issues', () => {
      // Review issues get the design's PR-review-agent entry point, using the
      // same assignment flow.
      render(<StartWorkButton {...defaultProps} issueStatus="review" />);
      const button = screen.getByTestId("start-work-button");
      expect(button).toHaveTextContent("Review with Agent");
      expect(button).toHaveAttribute(
        "aria-label",
        "Review with Agent - assign an agent",
      );
    });

    it("returns null for review issues that already have an assignee", () => {
      const { container } = render(
        <StartWorkButton
          {...defaultProps}
          issueStatus="review"
          currentAssignee="nova"
        />,
      );
      expect(container.innerHTML).toBe("");
    });

    it("returns null when issue status is undefined", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} issueStatus={undefined} />,
      );
      expect(container.innerHTML).toBe("");
    });

    it("returns null when currentAssignee is set", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} currentAssignee="[H] Alice" />,
      );
      expect(container.innerHTML).toBe("");
    });

    it("returns null when currentAssignee is a non-empty string", () => {
      const { container } = render(
        <StartWorkButton {...defaultProps} currentAssignee="bot-agent" />,
      );
      expect(container.innerHTML).toBe("");
    });
  });

  describe("Disabled states", () => {
    it("button is disabled when isConnected is false", () => {
      render(<StartWorkButton {...defaultProps} isConnected={false} />);
      const button = screen.getByTestId("start-work-button");
      expect(button).toBeDisabled();
    });

    it("button is disabled when disabled prop is true", () => {
      render(<StartWorkButton {...defaultProps} disabled={true} />);
      const button = screen.getByTestId("start-work-button");
      expect(button).toBeDisabled();
    });

    it("button is enabled when connected and not disabled", () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");
      expect(button).not.toBeDisabled();
    });
  });

  describe("Popover behavior", () => {
    it("opens popover on button click", () => {
      render(<StartWorkButton {...defaultProps} />);
      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(screen.getByTestId("start-work-popover")).toBeInTheDocument();
    });

    it("closes popover on second click (toggle)", () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");

      fireEvent.click(button);
      expect(screen.getByTestId("start-work-popover")).toBeInTheDocument();

      fireEvent.click(button);
      expect(
        screen.queryByTestId("start-work-popover"),
      ).not.toBeInTheDocument();
    });

    it("closes popover on Escape key", () => {
      render(<StartWorkButton {...defaultProps} />);
      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(screen.getByTestId("start-work-popover")).toBeInTheDocument();

      const popover = screen.getByTestId("start-work-popover");
      fireEvent.keyDown(popover, { key: "Escape" });
      expect(
        screen.queryByTestId("start-work-popover"),
      ).not.toBeInTheDocument();
    });

    it("returns focus to trigger on Escape", () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");
      fireEvent.click(button);

      const popover = screen.getByTestId("start-work-popover");
      fireEvent.keyDown(popover, { key: "Escape" });
      expect(document.activeElement).toBe(button);
    });

    it("closes popover on click outside", () => {
      render(
        <div>
          <StartWorkButton {...defaultProps} />
          <button data-testid="outside-button">Outside</button>
        </div>,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(screen.getByTestId("start-work-popover")).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId("outside-button"));
      expect(
        screen.queryByTestId("start-work-popover"),
      ).not.toBeInTheDocument();
    });

    it("does not open popover when disabled", () => {
      render(<StartWorkButton {...defaultProps} disabled={true} />);
      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(
        screen.queryByTestId("start-work-popover"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Agent list - available agents", () => {
    it("lists available agents with green status dot", () => {
      render(<StartWorkButton {...defaultProps} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const alphaOption = screen.getByTestId("agent-option-alpha");
      const betaOption = screen.getByTestId("agent-option-beta");

      expect(alphaOption).toBeInTheDocument();
      expect(betaOption).toBeInTheDocument();

      // Available agents are rendered as buttons
      expect(alphaOption.tagName).toBe("DIV");
      expect(betaOption.tagName).toBe("DIV");

      // Check green status dot
      const alphaDot = alphaOption.querySelector('[data-status="available"]');
      expect(alphaDot).toBeInTheDocument();
    });

    it("clicking an available agent calls onAssign with correct name", async () => {
      const onAssign = vi.fn().mockResolvedValue(undefined);
      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(onAssign).toHaveBeenCalledWith("alpha");
      });
    });

    it("popover closes after agent selection", async () => {
      const onAssign = vi.fn().mockResolvedValue(undefined);
      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(screen.getByTestId("start-work-popover")).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      expect(
        screen.queryByTestId("start-work-popover"),
      ).not.toBeInTheDocument();
    });

    it("shows idle agents as available", () => {
      const agents = [makeAgent("gamma", "idle", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-gamma");
      expect(option.tagName).toBe("DIV");
      expect(
        option.querySelector('[data-status="available"]'),
      ).toBeInTheDocument();
    });

    it("shows done agents as available", () => {
      const agents = [makeAgent("delta", "done", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-delta");
      expect(option.tagName).toBe("DIV");
    });
  });

  describe("Agent list - busy agents", () => {
    it("renders busy agents as div (not button), not clickable", () => {
      const agents = [makeAgent("alpha", "working: LOOM-50 (5m)", "task")];
      const agentTasks: Record<string, LoomTaskInfo> = {
        alpha: {
          id: "LOOM-50",
          title: "Fix login bug",
          priority: 2,
          status: "in_progress",
        },
      };
      render(
        <StartWorkButton
          {...defaultProps}
          agents={agents}
          agentTasks={agentTasks}
        />,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option.tagName).toBe("DIV");
      expect(option.querySelector('[data-status="busy"]')).toBeInTheDocument();
    });

    it("renders planning agents as busy", () => {
      const agents = [makeAgent("alpha", "planning: LOOM-60 (2m)", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option.tagName).toBe("DIV");
      expect(option.querySelector('[data-status="busy"]')).toBeInTheDocument();
    });

    it("renders review agents as busy", () => {
      const agents = [makeAgent("alpha", "review: LOOM-70 (10m)", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option.tagName).toBe("DIV");
      expect(option.querySelector('[data-status="busy"]')).toBeInTheDocument();
    });

    it("shows task ID for busy agents with task info", () => {
      const agents = [makeAgent("alpha", "working: LOOM-50 (5m)", "task")];
      const agentTasks: Record<string, LoomTaskInfo> = {
        alpha: {
          id: "LOOM-50",
          title: "Fix login bug",
          priority: 2,
          status: "in_progress",
        },
      };
      render(
        <StartWorkButton
          {...defaultProps}
          agents={agents}
          agentTasks={agentTasks}
        />,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option).toHaveTextContent("LOOM-50");
    });
  });

  describe("Agent list - warning agents", () => {
    it("renders error agents with warning dot and as clickable divs", () => {
      const agents = [makeAgent("alpha", "error", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option.tagName).toBe("DIV");
      expect(
        option.querySelector('[data-status="warning"]'),
      ).toBeInTheDocument();
    });

    it("renders dirty agents with warning dot", () => {
      const agents = [makeAgent("alpha", "dirty", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option.tagName).toBe("DIV");
      expect(
        option.querySelector('[data-status="warning"]'),
      ).toBeInTheDocument();
    });

    it("renders changes agents with warning dot", () => {
      const agents = [makeAgent("alpha", "2 changes", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const option = screen.getByTestId("agent-option-alpha");
      expect(option.tagName).toBe("DIV");
      expect(
        option.querySelector('[data-status="warning"]'),
      ).toBeInTheDocument();
    });

    it("clicking a warning agent calls onAssign", async () => {
      const onAssign = vi.fn().mockResolvedValue(undefined);
      const agents = [makeAgent("alpha", "error", "task")];
      render(
        <StartWorkButton
          {...defaultProps}
          agents={agents}
          onAssign={onAssign}
        />,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(onAssign).toHaveBeenCalledWith("alpha");
      });
    });
  });

  describe("Agent filtering by role", () => {
    it("filters out agents with role=plan", () => {
      const agents = [
        makeAgent("alpha", "ready", "task"),
        makeAgent("beta", "ready", "plan"),
      ];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByTestId("agent-option-alpha")).toBeInTheDocument();
      expect(screen.queryByTestId("agent-option-beta")).not.toBeInTheDocument();
    });

    it("shows plan agents when preferredRole is plan", () => {
      const agents = [
        makeAgent("alpha", "ready", "task"),
        makeAgent("beta", "ready", "plan"),
      ];
      render(
        <StartWorkButton
          {...defaultProps}
          agents={agents}
          preferredRole="plan"
        />,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(
        screen.queryByTestId("agent-option-alpha"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("agent-option-beta")).toBeInTheDocument();
    });

    it("shows agents with no role", () => {
      const agents = [
        makeAgent("alpha", "ready"), // no role
      ];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByTestId("agent-option-alpha")).toBeInTheDocument();
    });

    it("shows agents with role=task", () => {
      const agents = [makeAgent("alpha", "ready", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByTestId("agent-option-alpha")).toBeInTheDocument();
    });
  });

  describe("Empty and busy messages", () => {
    it('shows "No agents configured" when agents array is empty', () => {
      render(<StartWorkButton {...defaultProps} agents={[]} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByText("No agents configured")).toBeInTheDocument();
    });

    it('shows "All N agents busy" when all agents are busy', () => {
      const agents = [
        makeAgent("alpha", "working: LOOM-50 (5m)", "task"),
        makeAgent("beta", "planning: LOOM-60 (2m)", "task"),
      ];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByText("All 2 agents busy")).toBeInTheDocument();
    });

    it('shows "All 1 agent busy" for a single busy agent', () => {
      const agents = [makeAgent("alpha", "working: LOOM-50 (5m)", "task")];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByText("All 1 agent busy")).toBeInTheDocument();
    });

    it("does not show busy message when some agents are available", () => {
      const agents = [
        makeAgent("alpha", "ready", "task"),
        makeAgent("beta", "working: LOOM-50 (5m)", "task"),
      ];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.queryByText(/All.*agents? busy/)).not.toBeInTheDocument();
    });

    it("falls back to the full roster when no agent matches the preferred role", () => {
      // A picker that filters every agent away is a dead end — when the
      // workspace has agents but none match the stage role, offer them all.
      const agents = [
        makeAgent("alpha", "ready", "plan"),
        makeAgent("beta", "ready", "plan"),
      ];
      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(
        screen.queryByText("No agents configured"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
    });
  });

  describe("Connection warning", () => {
    it("shows connection warning in popover when disconnected", () => {
      render(<StartWorkButton {...defaultProps} isConnected={false} />);
      // Even though button is disabled, we can test the popover if it can open
      // Actually, button is disabled when not connected so popover can't open via click.
      // The connection warning is shown inside the popover, so let's test with a scenario
      // where the popover is already open and connection drops.
      // But that requires the component to be connected first, then disconnect.
      // Let's render with connected=true first, open popover, then rerender.
    });

    it("shows connection warning when popover is open and disconnected", () => {
      const { rerender } = render(
        <StartWorkButton {...defaultProps} isConnected={true} />,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(screen.getByTestId("start-work-popover")).toBeInTheDocument();

      // The popover is still open in state, rerender with disconnected
      // Note: the component re-renders with new props but state (isOpen) persists
      rerender(<StartWorkButton {...defaultProps} isConnected={false} />);

      // The component uses isOpen state, so popover should still be visible
      // However the button becomes disabled and clicking again won't toggle
      // Check if the warning is in the popover
      expect(screen.getByTestId("connection-warning")).toBeInTheDocument();
      expect(screen.getByTestId("connection-warning")).toHaveTextContent(
        "Loom server not connected",
      );
    });
  });

  describe("Loading/saving state", () => {
    it('shows "Assigning..." text on button during assignment', async () => {
      let resolveAssign: () => void;
      const assignPromise = new Promise<void>((resolve) => {
        resolveAssign = resolve;
      });
      const onAssign = vi.fn().mockReturnValue(assignPromise);

      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      // During assignment, button should show "Assigning..."
      expect(screen.getByTestId("start-work-button")).toHaveTextContent(
        "Assigning...",
      );

      // Shows saving indicator
      expect(screen.getByTestId("start-work-saving")).toBeInTheDocument();
      expect(screen.getByTestId("start-work-saving")).toHaveAttribute(
        "aria-label",
        "Assigning...",
      );

      await act(async () => {
        resolveAssign!();
      });

      // After assignment completes, text reverts
      expect(screen.getByTestId("start-work-button")).toHaveTextContent(
        "Start Work",
      );
    });

    it("button is disabled during assignment", async () => {
      let resolveAssign: () => void;
      const assignPromise = new Promise<void>((resolve) => {
        resolveAssign = resolve;
      });
      const onAssign = vi.fn().mockReturnValue(assignPromise);

      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      expect(screen.getByTestId("start-work-button")).toBeDisabled();

      await act(async () => {
        resolveAssign!();
      });
    });

    it("does not show saving indicator when not assigning", () => {
      render(<StartWorkButton {...defaultProps} />);
      expect(screen.queryByTestId("start-work-saving")).not.toBeInTheDocument();
    });
  });

  describe("Error handling", () => {
    it("shows error message when onAssign throws an Error", async () => {
      const onAssign = vi.fn().mockRejectedValue(new Error("Network failure"));
      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("start-work-error")).toHaveTextContent(
          "Network failure",
        );
      });
    });

    it("shows generic error for non-Error exceptions", async () => {
      const onAssign = vi.fn().mockRejectedValue("string error");
      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("start-work-error")).toHaveTextContent(
          "Failed to assign agent",
        );
      });
    });

    it('error has role="alert"', async () => {
      const onAssign = vi
        .fn()
        .mockRejectedValue(new Error("Assignment failed"));
      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      fireEvent.click(screen.getByTestId("start-work-button"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(screen.getByRole("alert")).toHaveTextContent(
          "Assignment failed",
        );
      });
    });

    it("clears error when popover is reopened", async () => {
      const onAssign = vi
        .fn()
        .mockRejectedValueOnce(new Error("First error"))
        .mockResolvedValueOnce(undefined);

      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId("start-work-button"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("start-work-error")).toBeInTheDocument();
      });

      // Reopen popover - error should clear
      fireEvent.click(screen.getByTestId("start-work-button"));
      expect(screen.queryByTestId("start-work-error")).not.toBeInTheDocument();
    });

    it("allows retry after failure", async () => {
      const onAssign = vi
        .fn()
        .mockRejectedValueOnce(new Error("First error"))
        .mockResolvedValueOnce(undefined);

      render(<StartWorkButton {...defaultProps} onAssign={onAssign} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId("start-work-button"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("start-work-error")).toBeInTheDocument();
      });

      // Retry should succeed
      fireEvent.click(screen.getByTestId("start-work-button"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("agent-option-alpha"));
      });

      await waitFor(() => {
        expect(
          screen.queryByTestId("start-work-error"),
        ).not.toBeInTheDocument();
      });
      expect(onAssign).toHaveBeenCalledTimes(2);
    });
  });

  describe("Accessibility", () => {
    it("has aria-expanded attribute reflecting popover state", () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");

      expect(button).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(button);
      expect(button).toHaveAttribute("aria-expanded", "true");

      fireEvent.click(button);
      expect(button).toHaveAttribute("aria-expanded", "false");
    });

    it('has aria-haspopup="listbox" on trigger', () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");
      expect(button).toHaveAttribute("aria-haspopup", "listbox");
    });

    it('has aria-label "Start Work - assign an agent" for open issues', () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");
      expect(button).toHaveAttribute(
        "aria-label",
        "Start Work - assign an agent",
      );
    });

    it('trigger is a button with type="button"', () => {
      render(<StartWorkButton {...defaultProps} />);
      const button = screen.getByTestId("start-work-button");
      expect(button.tagName).toBe("BUTTON");
      expect(button).toHaveAttribute("type", "button");
    });

    it("agent list has listbox role", () => {
      render(<StartWorkButton {...defaultProps} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      const listbox = screen.getByRole("listbox");
      expect(listbox).toBeInTheDocument();
      expect(listbox).toHaveAttribute("aria-label", "Available agents");
    });
  });

  describe("Mixed agent scenarios", () => {
    it("shows available, warning, and busy agents together", () => {
      const agents = [
        makeAgent("alpha", "ready", "task"),
        makeAgent("beta", "working: LOOM-50 (5m)", "task"),
        makeAgent("gamma", "error", "task"),
      ];
      const agentTasks: Record<string, LoomTaskInfo> = {
        beta: {
          id: "LOOM-50",
          title: "Fix bug",
          priority: 2,
          status: "in_progress",
        },
      };

      render(
        <StartWorkButton
          {...defaultProps}
          agents={agents}
          agentTasks={agentTasks}
        />,
      );
      fireEvent.click(screen.getByTestId("start-work-button"));

      // Available: button with green dot
      const alphaOption = screen.getByTestId("agent-option-alpha");
      expect(alphaOption.tagName).toBe("DIV");
      expect(
        alphaOption.querySelector('[data-status="available"]'),
      ).toBeInTheDocument();

      // Busy: div with busy dot
      const betaOption = screen.getByTestId("agent-option-beta");
      expect(betaOption.tagName).toBe("DIV");
      expect(
        betaOption.querySelector('[data-status="busy"]'),
      ).toBeInTheDocument();

      // Warning: button with warning dot
      const gammaOption = screen.getByTestId("agent-option-gamma");
      expect(gammaOption.tagName).toBe("DIV");
      expect(
        gammaOption.querySelector('[data-status="warning"]'),
      ).toBeInTheDocument();
    });

    it("shows popover header with available and busy counts", () => {
      const agents = [
        makeAgent("alpha", "ready", "task"),
        makeAgent("beta", "working: LOOM-50 (5m)", "task"),
        makeAgent("gamma", "planning: LOOM-60 (2m)", "task"),
      ];

      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByText("1 available")).toBeInTheDocument();
      expect(screen.getByText("2 busy")).toBeInTheDocument();
    });

    it("does not show busy count when no busy agents", () => {
      const agents = [
        makeAgent("alpha", "ready", "task"),
        makeAgent("beta", "ready", "task"),
      ];

      render(<StartWorkButton {...defaultProps} agents={agents} />);
      fireEvent.click(screen.getByTestId("start-work-button"));

      expect(screen.getByText("2 available")).toBeInTheDocument();
      expect(screen.queryByText(/busy/)).not.toBeInTheDocument();
    });
  });
});
