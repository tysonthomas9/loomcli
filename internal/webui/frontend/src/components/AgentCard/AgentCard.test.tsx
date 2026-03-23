/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentCard component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { LoomAgentStatus } from "@/types";

// Mock useIssueDiffStat to control diff stat data in tests
const mockUseIssueDiffStat = vi.fn().mockReturnValue({
  data: null,
  isLoading: false,
  error: null,
  refetch: vi.fn(),
});
vi.mock("@/hooks/useIssueDiffStat", () => ({
  useIssueDiffStat: (...args: unknown[]) => mockUseIssueDiffStat(...args),
}));

import { AgentCard } from "./AgentCard";

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

describe("AgentCard", () => {
  describe("avatar", () => {
    it("renders the first letter of the agent name", () => {
      render(<AgentCard agent={makeAgent({ name: "nova" })} />);

      expect(screen.getByLabelText("nova avatar")).toHaveTextContent("n");
    });

    it("renders uppercase initial for uppercase name", () => {
      render(<AgentCard agent={makeAgent({ name: "Falcon" })} />);

      expect(screen.getByLabelText("Falcon avatar")).toHaveTextContent("F");
    });

    it("applies a background color style", () => {
      render(<AgentCard agent={makeAgent({ name: "ember" })} />);

      const avatar = screen.getByLabelText("ember avatar");
      expect(avatar.style.backgroundColor).toBeTruthy();
    });

    it("returns the same color for the same name (deterministic)", () => {
      const { unmount } = render(
        <AgentCard agent={makeAgent({ name: "atlas" })} />,
      );
      const color1 =
        screen.getByLabelText("atlas avatar").style.backgroundColor;
      unmount();

      render(<AgentCard agent={makeAgent({ name: "atlas" })} />);
      const color2 =
        screen.getByLabelText("atlas avatar").style.backgroundColor;

      expect(color1).toBe(color2);
    });

    it("different names can produce different colors", () => {
      const { unmount } = render(
        <AgentCard agent={makeAgent({ name: "aaa" })} />,
      );
      const color1 = screen.getByLabelText("aaa avatar").style.backgroundColor;
      unmount();

      render(<AgentCard agent={makeAgent({ name: "zzz" })} />);
      const color2 = screen.getByLabelText("zzz avatar").style.backgroundColor;

      // Not guaranteed to differ for all pairs, but these specific names should
      expect(color1).not.toBe(color2);
    });
  });

  describe("status dot", () => {
    it("renders a status dot element", () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      // The status dot is aria-hidden
      const dot = container.querySelector('[aria-hidden="true"]');
      expect(dot).toBeInTheDocument();
    });

    it("has a background color style", () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: "working: bd-123 (5m)" })} />,
      );

      const dot = container.querySelector('[aria-hidden="true"]');
      expect(dot).toBeInstanceOf(HTMLElement);
      expect((dot as HTMLElement).style.backgroundColor).toBeTruthy();
    });
  });

  describe("agent name", () => {
    it("displays the agent name", () => {
      render(<AgentCard agent={makeAgent({ name: "nova" })} />);

      expect(screen.getByText("nova")).toBeInTheDocument();
    });
  });

  describe("role label", () => {
    it("shows capitalized role when agent has role", () => {
      render(<AgentCard agent={makeAgent({ role: "plan" })} />);
      expect(screen.getByText("Plan")).toBeInTheDocument();
    });

    it('shows "Task" for task role', () => {
      render(<AgentCard agent={makeAgent({ role: "task" })} />);
      expect(screen.getByText("Task")).toBeInTheDocument();
    });

    it('shows "Agent" fallback when role is undefined', () => {
      render(<AgentCard agent={makeAgent()} />);
      expect(screen.getByText("Agent")).toBeInTheDocument();
    });

    it('shows "Agent" fallback when role is empty string', () => {
      render(<AgentCard agent={makeAgent({ role: "" })} />);
      expect(screen.getByText("Agent")).toBeInTheDocument();
    });
  });
  describe("status line", () => {
    it('shows "Ready" for ready status', () => {
      render(
        <AgentCard agent={makeAgent({ status: "ready", branch: "main" })} />,
      );

      expect(screen.getByText("Ready")).toBeInTheDocument();
    });

    it('shows "Idle" for idle status', () => {
      render(
        <AgentCard agent={makeAgent({ status: "idle", branch: "dev" })} />,
      );

      expect(screen.getByText("Idle")).toBeInTheDocument();
    });

    it('shows "Working" for working status', () => {
      render(
        <AgentCard agent={makeAgent({ status: "working", branch: "b" })} />,
      );

      expect(screen.getByText("Working")).toBeInTheDocument();
    });

    it('shows "Working" for working with task ID', () => {
      render(
        <AgentCard
          agent={makeAgent({ status: "working: bd-123 (5m)", branch: "b" })}
        />,
      );

      expect(screen.getByText("Working")).toBeInTheDocument();
    });

    it('shows "Planning" for planning status', () => {
      render(
        <AgentCard agent={makeAgent({ status: "planning", branch: "b" })} />,
      );

      expect(screen.getByText("Planning")).toBeInTheDocument();
    });

    it('shows "Planning" for planning with task ID', () => {
      render(
        <AgentCard
          agent={makeAgent({ status: "planning: bd-456 (2m)", branch: "b" })}
        />,
      );

      expect(screen.getByText("Planning")).toBeInTheDocument();
    });

    it('shows "Done" for done status', () => {
      render(<AgentCard agent={makeAgent({ status: "done", branch: "b" })} />);

      expect(screen.getByText("Done")).toBeInTheDocument();
    });

    it('shows "Review" for review status', () => {
      render(
        <AgentCard agent={makeAgent({ status: "review", branch: "b" })} />,
      );

      expect(screen.getByText("Review")).toBeInTheDocument();
    });

    it('shows "Error" for error status', () => {
      render(<AgentCard agent={makeAgent({ status: "error", branch: "b" })} />);

      expect(screen.getByText("Error")).toBeInTheDocument();
    });

    it('shows "Uncommitted changes" for dirty status', () => {
      render(<AgentCard agent={makeAgent({ status: "dirty", branch: "b" })} />);

      expect(screen.getByText("Uncommitted changes")).toBeInTheDocument();
    });

    it('shows "2 changes" for changes status', () => {
      render(
        <AgentCard agent={makeAgent({ status: "2 changes", branch: "b" })} />,
      );

      expect(screen.getByText("2 changes")).toBeInTheDocument();
    });

    it('shows "1 change" (singular) for single change', () => {
      render(
        <AgentCard agent={makeAgent({ status: "1 change", branch: "b" })} />,
      );

      expect(screen.getByText("1 change")).toBeInTheDocument();
    });
  });

  describe("error status data attribute", () => {
    it("sets data-error on status line when status is error", () => {
      render(<AgentCard agent={makeAgent({ status: "error", branch: "b" })} />);

      const statusLine = screen.getByText("Error");
      expect(statusLine).toHaveAttribute("data-error");
    });

    it("does not set data-error for non-error statuses", () => {
      render(<AgentCard agent={makeAgent({ status: "ready", branch: "b" })} />);

      const statusLine = screen.getByText("Ready");
      expect(statusLine).not.toHaveAttribute("data-error");
    });
  });

  describe("diff stats (line-level)", () => {
    it("shows +N lines added when issueId provided and diff stat has data", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: { branch: "feature", added: 366, removed: 12 },
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-1" />,
      );

      const diffAdded = container.querySelector('[class*="diffAdded"]');
      expect(diffAdded).toBeInTheDocument();
      expect(diffAdded).toHaveTextContent("+366");
    });

    it("shows -N lines removed when diff stat has removals", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: { branch: "feature", added: 10, removed: 42 },
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-1" />,
      );

      const diffRemoved = container.querySelector('[class*="diffRemoved"]');
      expect(diffRemoved).toBeInTheDocument();
      expect(diffRemoved).toHaveTextContent("-42");
    });

    it("shows only +N when removed is 0", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: { branch: "feature", added: 50, removed: 0 },
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-1" />,
      );

      expect(container.querySelector('[class*="diffAdded"]')).toHaveTextContent(
        "+50",
      );
      expect(
        container.querySelector('[class*="diffRemoved"]'),
      ).not.toBeInTheDocument();
    });

    it("shows only -N when added is 0", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: { branch: "feature", added: 0, removed: 25 },
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-1" />,
      );

      expect(
        container.querySelector('[class*="diffAdded"]'),
      ).not.toBeInTheDocument();
      expect(
        container.querySelector('[class*="diffRemoved"]'),
      ).toHaveTextContent("-25");
    });

    it("shows correct tooltip with line counts", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: { branch: "feature", added: 100, removed: 30 },
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      render(<AgentCard agent={makeAgent()} issueId="issue-1" />);

      expect(
        screen.getByTitle("100 lines added, 30 lines removed"),
      ).toBeInTheDocument();
    });

    it("does not show diff stats when issueId is undefined", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: null,
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(<AgentCard agent={makeAgent()} />);

      expect(
        container.querySelector('[class*="diffStats"]'),
      ).not.toBeInTheDocument();
    });

    it("does not show diff stats when hook is loading", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: null,
        isLoading: true,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-1" />,
      );

      expect(
        container.querySelector('[class*="diffStats"]'),
      ).not.toBeInTheDocument();
    });

    it("does not show diff stats on error", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: null,
        isLoading: false,
        error: new Error("fetch failed"),
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-1" />,
      );

      expect(
        container.querySelector('[class*="diffStats"]'),
      ).not.toBeInTheDocument();
    });
  });

  describe("data-status attribute", () => {
    it("sets data-status to the parsed status type", () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: "ready" })} />,
      );

      expect(container.firstChild).toHaveAttribute("data-status", "ready");
    });

    it("sets data-status to working for working status", () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: "working: bd-123 (5m)" })} />,
      );

      expect(container.firstChild).toHaveAttribute("data-status", "working");
    });

    it("sets data-status to changes for changes status", () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ status: "3 changes" })} />,
      );

      expect(container.firstChild).toHaveAttribute("data-status", "changes");
    });
  });

  describe("className prop", () => {
    it("applies additional className to root element", () => {
      const { container } = render(
        <AgentCard agent={makeAgent()} className="custom-class" />,
      );

      expect(container.firstChild).toHaveClass("custom-class");
    });

    it("works without className prop", () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      // Should render without error
      expect(container.firstChild).toBeInTheDocument();
    });
  });

  describe("onClick handler", () => {
    it("calls onClick when clicked", () => {
      const handleClick = vi.fn();
      render(<AgentCard agent={makeAgent()} onClick={handleClick} />);

      const card = screen.getByRole("button");
      fireEvent.click(card);

      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('sets role="button" when onClick is provided', () => {
      render(<AgentCard agent={makeAgent()} onClick={() => {}} />);

      expect(screen.getByRole("button")).toBeInTheDocument();
    });

    it("does not set role when onClick is not provided", () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      expect(
        container.querySelector('[role="button"]'),
      ).not.toBeInTheDocument();
    });

    it("sets tabIndex=0 when onClick is provided", () => {
      render(<AgentCard agent={makeAgent()} onClick={() => {}} />);

      expect(screen.getByRole("button")).toHaveAttribute("tabindex", "0");
    });

    it("does not set tabIndex when onClick is not provided", () => {
      const { container } = render(<AgentCard agent={makeAgent()} />);

      expect(container.firstChild).not.toHaveAttribute("tabindex");
    });

    it("calls onClick on Enter key", () => {
      const handleClick = vi.fn();
      render(<AgentCard agent={makeAgent()} onClick={handleClick} />);

      const card = screen.getByRole("button");
      fireEvent.keyDown(card, { key: "Enter" });

      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it("does not call onClick on other keys", () => {
      const handleClick = vi.fn();
      render(<AgentCard agent={makeAgent()} onClick={handleClick} />);

      const card = screen.getByRole("button");
      fireEvent.keyDown(card, { key: "a" });

      expect(handleClick).not.toHaveBeenCalled();
    });
  });

  describe("taskTitle prop", () => {
    it("uses taskTitle as title attribute on status line when provided", () => {
      render(
        <AgentCard
          agent={makeAgent({ status: "working: bd-123 (5m)", branch: "b" })}
          taskTitle="Fix the login bug"
        />,
      );

      expect(screen.getByTitle("Fix the login bug")).toBeInTheDocument();
    });

    it("uses status line text as title when taskTitle is not provided", () => {
      render(
        <AgentCard agent={makeAgent({ status: "ready", branch: "main" })} />,
      );

      expect(screen.getByTitle("Ready")).toBeInTheDocument();
    });
  });

  describe("repo badge", () => {
    it("renders RepoBadge when agent.repo is set", () => {
      render(<AgentCard agent={makeAgent({ repo: "api" })} />);

      expect(screen.getByLabelText("Repository: api")).toBeInTheDocument();
      expect(screen.getByText("api")).toBeInTheDocument();
    });

    it("does not render repo line when agent.repo is undefined", () => {
      render(<AgentCard agent={makeAgent({ repo: undefined })} />);

      expect(screen.queryByLabelText(/^Repository:/)).not.toBeInTheDocument();
    });
  });

  describe("cross_repo indicator", () => {
    it("renders cross_repo indicator when agent.cross_repo is true", () => {
      render(
        <AgentCard agent={makeAgent({ repo: "api", cross_repo: true })} />,
      );

      expect(screen.getByText("↔")).toBeInTheDocument();
    });

    it("does not render cross_repo indicator when agent.cross_repo is false", () => {
      render(
        <AgentCard agent={makeAgent({ repo: "api", cross_repo: false })} />,
      );

      expect(screen.queryByText("↔")).not.toBeInTheDocument();
    });

    it("does not render cross_repo indicator when agent.cross_repo is undefined", () => {
      render(<AgentCard agent={makeAgent({ repo: "api" })} />);

      expect(screen.queryByText("↔")).not.toBeInTheDocument();
    });

    it("cross_repo indicator has correct aria-label", () => {
      render(
        <AgentCard agent={makeAgent({ repo: "api", cross_repo: true })} />,
      );

      expect(
        screen.getByLabelText("Works across multiple repositories"),
      ).toBeInTheDocument();
    });
  });

  describe("edge cases", () => {
    it("handles empty name gracefully", () => {
      const { container } = render(
        <AgentCard agent={makeAgent({ name: "" })} />,
      );

      // aria-label will be " avatar" — verify the avatar element exists
      const avatar = container.querySelector('[aria-label=" avatar"]');
      expect(avatar).toBeInTheDocument();
    });

    it("handles unknown status string as ready", () => {
      const { container } = render(
        <AgentCard
          agent={makeAgent({ status: "something_unknown", branch: "b" })}
        />,
      );

      expect(container.firstChild).toHaveAttribute("data-status", "ready");
      expect(screen.getByText("Ready")).toBeInTheDocument();
    });

    it("handles large diff stat numbers", () => {
      mockUseIssueDiffStat.mockReturnValue({
        data: { branch: "feature", added: 12345, removed: 6789 },
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });

      const { container } = render(
        <AgentCard agent={makeAgent()} issueId="issue-big" />,
      );

      expect(container.querySelector('[class*="diffAdded"]')).toHaveTextContent(
        "+12345",
      );
      expect(
        container.querySelector('[class*="diffRemoved"]'),
      ).toHaveTextContent("-6789");
    });
  });
});
