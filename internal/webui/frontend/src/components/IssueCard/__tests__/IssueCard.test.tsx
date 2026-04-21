/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueCard component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { BlockerRef, Issue } from "@/types";

import { IssueCard } from "../IssueCard";
import styles from "../IssueCard.module.css";

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-issue-abc123",
    title: "Test Issue Title",
    priority: 2,
    created_at: "2024-01-15T10:30:00Z",
    updated_at: "2024-01-15T10:30:00Z",
    ...overrides,
  };
}

describe("IssueCard", () => {
  describe("rendering", () => {
    it("renders issue title", () => {
      const issue = createTestIssue({ title: "My Issue Title" });
      render(<IssueCard issue={issue} />);

      expect(
        screen.getByRole("heading", { name: "My Issue Title" }),
      ).toBeInTheDocument();
    });

    it("renders issue ID (shortened with prefix preserved)", () => {
      const issue = createTestIssue({ id: "beads-abc123def456" });
      render(<IssueCard issue={issue} />);

      // Should preserve prefix and truncate: "beads-abc12..."
      expect(screen.getByText("beads-abc12...")).toBeInTheDocument();
    });

    it("renders short ID as-is", () => {
      const issue = createTestIssue({ id: "bd-xyz" });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("bd-xyz")).toBeInTheDocument();
    });

    it("renders priority badge with correct text", () => {
      const issue = createTestIssue({ priority: 1 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("P1")).toBeInTheDocument();
    });

    it("renders with article element", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      expect(container.querySelector("article")).toBeInTheDocument();
    });
  });

  describe("priority display", () => {
    it.each([0, 1, 2, 3, 4] as const)("renders P%i correctly", (priority) => {
      const issue = createTestIssue({ priority });
      const { container } = render(<IssueCard issue={issue} />);

      expect(screen.getByText(`P${priority}`)).toBeInTheDocument();
      expect(container.querySelector("[data-priority]")).toHaveAttribute(
        "data-priority",
        String(priority),
      );
    });

    /**
     * P2 priority badge contrast fix verification.
     * The CSS uses data-priority="2" to apply dark text color for WCAG AA contrast
     * on the yellow background. This test ensures the attribute is correctly set.
     */
    it('P2 priority badge has data-priority="2" for CSS contrast styling', () => {
      const issue = createTestIssue({ priority: 2 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText("P2");
      expect(priorityBadge).toHaveAttribute("data-priority", "2");
    });

    it("defaults to P4 when priority is undefined", () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("P4")).toBeInTheDocument();
    });

    it("defaults to P4 for out of range priority (negative)", () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: -1 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("P4")).toBeInTheDocument();
    });

    it("defaults to P4 for out of range priority (> 4)", () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 5 });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("P4")).toBeInTheDocument();
    });
  });

  describe("priority badge styling", () => {
    it.each([0, 1, 2, 3, 4] as const)(
      "applies priority%i class to priority badge for priority %i",
      (priority) => {
        const issue = createTestIssue({ priority });
        render(<IssueCard issue={issue} />);

        const priorityBadge = screen.getByText(`P${priority}`);
        // CSS Modules hashes class names, so we check for the pattern
        expect(priorityBadge.className).toMatch(
          new RegExp(`priority${priority}`),
        );
      },
    );

    it("applies both priorityBadge base class and priority-specific class", () => {
      const issue = createTestIssue({ priority: 2 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText("P2");
      // Should have both the base priorityBadge class and priority2 class
      expect(priorityBadge.className).toMatch(/priorityBadge/);
      expect(priorityBadge.className).toMatch(/priority2/);
    });

    it("priority badge has data-priority attribute for backwards compatibility", () => {
      const issue = createTestIssue({ priority: 1 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText("P1");
      expect(priorityBadge).toHaveAttribute("data-priority", "1");
    });

    it.each([0, 1, 2, 3, 4] as const)(
      'priority badge has data-priority="%i" attribute',
      (priority) => {
        const issue = createTestIssue({ priority });
        render(<IssueCard issue={issue} />);

        const priorityBadge = screen.getByText(`P${priority}`);
        expect(priorityBadge).toHaveAttribute(
          "data-priority",
          String(priority),
        );
      },
    );

    it("applies priority4 class when priority is undefined (default)", () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText("P4");
      expect(priorityBadge.className).toMatch(/priority4/);
      expect(priorityBadge).toHaveAttribute("data-priority", "4");
    });

    it("applies priority4 class for out of range priority", () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 99 });
      render(<IssueCard issue={issue} />);

      const priorityBadge = screen.getByText("P4");
      expect(priorityBadge.className).toMatch(/priority4/);
      expect(priorityBadge).toHaveAttribute("data-priority", "4");
    });
  });

  describe("onClick interaction", () => {
    it("calls onClick when card is clicked", () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.click(screen.getByRole("button"));
      expect(handleClick).toHaveBeenCalledWith(issue);
      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it("does not crash when onClick is not provided", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} />);

      // Should not throw when clicked
      const article = document.querySelector("article");
      expect(() => fireEvent.click(article!)).not.toThrow();
    });

    it("calls onClick on Enter key", () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.keyDown(screen.getByRole("button"), { key: "Enter" });
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it("calls onClick on Space key", () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.keyDown(screen.getByRole("button"), { key: " " });
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it("does not call onClick on other keys", () => {
      const issue = createTestIssue();
      const handleClick = vi.fn();
      render(<IssueCard issue={issue} onClick={handleClick} />);

      fireEvent.keyDown(screen.getByRole("button"), { key: "Tab" });
      fireEvent.keyDown(screen.getByRole("button"), { key: "Escape" });
      expect(handleClick).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it("has aria-label with issue title", () => {
      const issue = createTestIssue({ title: "Test Accessibility" });
      render(<IssueCard issue={issue} />);

      expect(
        screen.getByLabelText("Issue: Test Accessibility"),
      ).toBeInTheDocument();
    });

    it("has button role when onClick is provided", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} onClick={() => {}} />);

      expect(screen.getByRole("button")).toBeInTheDocument();
    });

    it("does not have button role when onClick is not provided", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} />);

      expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("is keyboard focusable when onClick is provided", () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} onClick={() => {}} />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("tabIndex", "0");
    });

    it("is not keyboard focusable when onClick is not provided", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("tabIndex");
    });

    it("priority badge has aria-label", () => {
      const issue = createTestIssue({ priority: 0 });
      render(<IssueCard issue={issue} />);

      expect(
        screen.getByLabelText("Priority: P0 - Critical"),
      ).toBeInTheDocument();
    });
  });

  describe("props", () => {
    it("applies className prop to root element", () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} className="custom-class" />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveClass("custom-class");
    });

    it("data-priority attribute matches issue priority", () => {
      const issue = createTestIssue({ priority: 3 });
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-priority", "3");
    });

    it("renders data-column attribute with columnId prop value", () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} columnId="in_progress" />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-column", "in_progress");
    });

    it('renders data-column attribute with "review" columnId', () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} columnId="review" />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-column", "review");
    });

    it('renders data-column attribute with "done" columnId', () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} columnId="done" />);

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-column", "done");
    });

    it("data-column attribute is undefined when no columnId is provided", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-column");
    });
  });

  describe("edge cases", () => {
    it('renders "Untitled" for missing title', () => {
      const issue = createTestIssue({ title: "" });
      render(<IssueCard issue={issue} />);

      expect(
        screen.getByRole("heading", { name: "Untitled" }),
      ).toBeInTheDocument();
    });

    it('renders "unknown" for missing ID', () => {
      const issue = createTestIssue({ id: "" });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("unknown")).toBeInTheDocument();
    });

    it("handles very long title", () => {
      const longTitle = "A".repeat(200);
      const issue = createTestIssue({ title: longTitle });
      render(<IssueCard issue={issue} />);

      // Should still render, truncation is handled by CSS
      expect(
        screen.getByRole("heading", { name: longTitle }),
      ).toBeInTheDocument();
    });

    it("renders with minimal issue props", () => {
      // Only required fields
      const issue: Issue = {
        id: "min-id",
        title: "Minimal",
        priority: 2,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      };
      render(<IssueCard issue={issue} />);

      expect(
        screen.getByRole("heading", { name: "Minimal" }),
      ).toBeInTheDocument();
      expect(screen.getByText("min-id")).toBeInTheDocument();
      expect(screen.getByText("P2")).toBeInTheDocument();
    });

    it("renders with full issue props", () => {
      const issue = createTestIssue({
        id: "full-issue-id",
        title: "Full Issue",
        priority: 0,
        status: "open",
        description: "A description",
        assignee: "user",
        labels: ["bug", "urgent"],
      });
      render(<IssueCard issue={issue} />);

      expect(
        screen.getByRole("heading", { name: "Full Issue" }),
      ).toBeInTheDocument();
      expect(screen.getByText("P0")).toBeInTheDocument();
      expect(screen.getByTestId("issue-card-labels")).toBeInTheDocument();
      expect(screen.getAllByTestId("issue-card-label")).toHaveLength(2);
    });
  });

  describe("blocked badge display", () => {
    it("renders BlockedBadge when blockedByCount > 0", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={3} />);

      expect(screen.getByLabelText("Blocked by 3 issues")).toBeInTheDocument();
    });

    it("does not render BlockedBadge when blockedByCount is 0", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={0} />);

      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it("does not render BlockedBadge when blockedByCount is undefined", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} />);

      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it("passes blockedBy array to BlockedBadge", () => {
      const issue = createTestIssue();
      const blockers = ["blocker-1", "blocker-2"];
      render(
        <IssueCard issue={issue} blockedByCount={2} blockedBy={blockers} />,
      );

      // Hover to show tooltip
      const badge = screen.getByLabelText("Blocked by 2 issues");
      fireEvent.mouseEnter(badge);

      expect(screen.getByText("blocker-1")).toBeInTheDocument();
      expect(screen.getByText("blocker-2")).toBeInTheDocument();
    });

    it("sets data-blocked attribute to true when blocked", () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} blockedByCount={1} />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-blocked", "true");
    });

    it("does not set data-blocked attribute when not blocked", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-blocked");
    });

    it("does not set data-blocked when blockedByCount is 0", () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} blockedByCount={0} />,
      );

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-blocked");
    });

    it("aria-label includes (blocked) when issue is blocked", () => {
      const issue = createTestIssue({ title: "Blocked Issue" });
      render(<IssueCard issue={issue} blockedByCount={1} />);

      expect(
        screen.getByLabelText("Issue: Blocked Issue (blocked)"),
      ).toBeInTheDocument();
    });

    it("aria-label does not include (blocked) when not blocked", () => {
      const issue = createTestIssue({ title: "Normal Issue" });
      render(<IssueCard issue={issue} />);

      expect(screen.getByLabelText("Issue: Normal Issue")).toBeInTheDocument();
      expect(screen.queryByLabelText(/blocked/)).not.toBeInTheDocument();
    });

    it("renders BlockedBadge with blockedByCount of 1", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={1} />);

      expect(screen.getByLabelText("Blocked by 1 issue")).toBeInTheDocument();
      expect(screen.getByText("1")).toBeInTheDocument();
    });

    it("renders BlockedBadge with large blockedByCount", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={99} />);

      expect(screen.getByLabelText("Blocked by 99 issues")).toBeInTheDocument();
      expect(screen.getByText("99")).toBeInTheDocument();
    });

    it("renders BlockedBadge without blockedBy array", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} blockedByCount={5} />);

      // Badge should still render
      expect(screen.getByLabelText("Blocked by 5 issues")).toBeInTheDocument();

      // Tooltip should not show when hovering (no blockers to display)
      const badge = screen.getByLabelText("Blocked by 5 issues");
      fireEvent.mouseEnter(badge);
      expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
    });

    it('passes blockedByDetails to BlockedBadge and shows "id: title" format', () => {
      const issue = createTestIssue();
      const blockers = ["BLOCK-1", "BLOCK-2"];
      const details: BlockerRef[] = [
        { id: "BLOCK-1", title: "Fix auth service", priority: 1 },
        { id: "BLOCK-2", title: "Database migration", priority: 2 },
      ];
      render(
        <IssueCard
          issue={issue}
          blockedByCount={2}
          blockedBy={blockers}
          blockedByDetails={details}
        />,
      );

      // Hover to show tooltip
      const badge = screen.getByLabelText("Blocked by 2 issues");
      fireEvent.mouseEnter(badge);

      // Should show "id: title" format from issueDetails, not plain IDs
      expect(screen.getByText("BLOCK-1: Fix auth service")).toBeInTheDocument();
      expect(
        screen.getByText("BLOCK-2: Database migration"),
      ).toBeInTheDocument();
    });
  });

  describe("isBacklog prop", () => {
    it('renders with data-in-backlog="true" when isBacklog is true', () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} isBacklog={true} />,
      );

      const article = container.querySelector("article");
      expect(article).toHaveAttribute("data-in-backlog", "true");
    });

    it("does not render data-in-backlog attribute when isBacklog is false", () => {
      const issue = createTestIssue();
      const { container } = render(
        <IssueCard issue={issue} isBacklog={false} />,
      );

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-in-backlog");
    });

    it("does not render data-in-backlog attribute when isBacklog is undefined", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector("article");
      expect(article).not.toHaveAttribute("data-in-backlog");
    });

    it("includes (backlog) in aria-label when isBacklog is true", () => {
      const issue = createTestIssue({ title: "Backlog Issue" });
      render(<IssueCard issue={issue} isBacklog={true} />);

      expect(
        screen.getByLabelText("Issue: Backlog Issue (backlog)"),
      ).toBeInTheDocument();
    });

    it("aria-label does not include (backlog) when isBacklog is false", () => {
      const issue = createTestIssue({ title: "Normal Issue" });
      render(<IssueCard issue={issue} isBacklog={false} />);

      expect(screen.getByLabelText("Issue: Normal Issue")).toBeInTheDocument();
    });

    it("aria-label includes both (blocked) and (backlog) when both are true", () => {
      const issue = createTestIssue({ title: "Complex Issue" });
      render(<IssueCard issue={issue} blockedByCount={1} isBacklog={true} />);

      expect(
        screen.getByLabelText("Issue: Complex Issue (blocked) (backlog)"),
      ).toBeInTheDocument();
    });
  });

  describe("deferred badge", () => {
    it('renders deferred badge when issue status is "deferred"', () => {
      const issue = createTestIssue({ status: "deferred" });
      render(<IssueCard issue={issue} />);

      const badge = screen.getByLabelText("Deferred");
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveTextContent("Deferred");
      expect(screen.getByText("⏸")).toBeInTheDocument();
    });

    it("does not render deferred badge for non-deferred status", () => {
      const issue = createTestIssue({ status: "open" });
      render(<IssueCard issue={issue} />);

      expect(screen.queryByLabelText("Deferred")).not.toBeInTheDocument();
    });

    it('deferred badge has aria-label="Deferred"', () => {
      const issue = createTestIssue({ status: "deferred" });
      render(<IssueCard issue={issue} />);

      expect(screen.getByLabelText("Deferred")).toBeInTheDocument();
    });

    it("deferred badge icon has aria-hidden", () => {
      const issue = createTestIssue({ status: "deferred" });
      render(<IssueCard issue={issue} />);

      const icon = screen.getByText("⏸");
      expect(icon).toHaveAttribute("aria-hidden", "true");
    });
  });

  describe("review type badge", () => {
    describe("getReviewType logic", () => {
      it("returns plan when status is review with no PR external_ref", () => {
        const issue = createTestIssue({
          title: "Design auth flow",
          status: "review",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText("Plan")).toBeInTheDocument();
        expect(screen.getByLabelText("Plan review")).toBeInTheDocument();
      });

      it("returns code when status is review with PR external_ref", () => {
        const issue = createTestIssue({
          title: "Implement feature X",
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/42",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText("Code")).toBeInTheDocument();
        expect(screen.getByLabelText("Code review")).toBeInTheDocument();
      });

      it("returns help when status is blocked AND notes field is populated", () => {
        const issue = createTestIssue({
          title: "Task needing help",
          status: "blocked",
          notes: "Stuck on database migration issue",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText("Help")).toBeInTheDocument();
        expect(screen.getByLabelText("Help review")).toBeInTheDocument();
      });

      it("returns null when none of the conditions are met", () => {
        const issue = createTestIssue({
          title: "Regular task",
          status: "in_progress",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.queryByText("Plan")).not.toBeInTheDocument();
        expect(screen.queryByText("Code")).not.toBeInTheDocument();
        expect(screen.queryByText("Help")).not.toBeInTheDocument();
      });

      it("returns null for blocked status without notes", () => {
        const issue = createTestIssue({
          title: "Blocked task without notes",
          status: "blocked",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.queryByText("Help")).not.toBeInTheDocument();
      });

      it("returns plan when status is review with non-PR external_ref", () => {
        const issue = createTestIssue({
          title: "Task",
          status: "review",
          external_ref: "JIRA-123",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.getByText("Plan")).toBeInTheDocument();
        expect(screen.queryByText("Code")).not.toBeInTheDocument();
      });
    });

    describe("badge rendering", () => {
      it("shows Plan badge with icon for plan review", () => {
        const issue = createTestIssue({
          title: "Design proposal",
          status: "review",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Plan review");
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent("Plan");
        expect(screen.getByText("📝")).toBeInTheDocument();
      });

      it("shows Code badge with icon for code review", () => {
        const issue = createTestIssue({
          title: "Feature implementation",
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/10",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Code review");
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent("Code");
        expect(screen.getByText("🔍")).toBeInTheDocument();
      });

      it("shows Help badge with icon for blocked issues with notes", () => {
        const issue = createTestIssue({
          title: "Needs assistance",
          status: "blocked",
          notes: "Need help with API integration",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Help review");
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent("Help");
        expect(screen.getByText("❓")).toBeInTheDocument();
      });

      it("does not show badge for regular issues", () => {
        const issue = createTestIssue({
          title: "Normal task",
          status: "open",
        });
        render(<IssueCard issue={issue} />);

        expect(screen.queryByLabelText("Plan review")).not.toBeInTheDocument();
        expect(screen.queryByLabelText("Code review")).not.toBeInTheDocument();
        expect(screen.queryByLabelText("Help review")).not.toBeInTheDocument();
      });

      it("badge icon has aria-hidden attribute", () => {
        const issue = createTestIssue({ title: "Feature", status: "review" });
        render(<IssueCard issue={issue} />);

        const icon = screen.getByText("📝");
        expect(icon).toHaveAttribute("aria-hidden", "true");
      });

      it("applies reviewPlan class to Plan badge", () => {
        const issue = createTestIssue({ title: "Plan item", status: "review" });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Plan review");
        expect(badge.className).toMatch(/reviewPlan/);
      });

      it("applies reviewCode class to Code badge", () => {
        const issue = createTestIssue({
          title: "Code item",
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/5",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Code review");
        expect(badge.className).toMatch(/reviewCode/);
      });

      it("applies reviewHelp class to Help badge", () => {
        const issue = createTestIssue({
          title: "Help item",
          status: "blocked",
          notes: "Need assistance",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Help review");
        expect(badge.className).toMatch(/reviewHelp/);
      });
    });

    describe("PR link", () => {
      it("shows PR link for code reviews", () => {
        const issue = createTestIssue({
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/42",
        });
        render(<IssueCard issue={issue} />);

        const link = screen.getByLabelText("View pull request");
        expect(link).toHaveAttribute(
          "href",
          "https://github.com/owner/repo/pull/42",
        );
        expect(link).toHaveAttribute("target", "_blank");
      });

      it("does not show PR link for plan reviews", () => {
        const issue = createTestIssue({ status: "review" });
        render(<IssueCard issue={issue} />);

        expect(
          screen.queryByLabelText("View pull request"),
        ).not.toBeInTheDocument();
      });

      it("PR link click does not trigger card onClick", () => {
        const issue = createTestIssue({
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/42",
        });
        const onClick = vi.fn();
        render(<IssueCard issue={issue} onClick={onClick} />);

        const link = screen.getByLabelText("View pull request");
        fireEvent.click(link);

        expect(onClick).not.toHaveBeenCalled();
      });
    });

    describe("edge cases", () => {
      it("handles undefined title gracefully", () => {
        // @ts-expect-error Testing undefined title
        const issue = createTestIssue({ title: undefined });
        render(<IssueCard issue={issue} />);

        // Should not show any review badge
        expect(screen.queryByLabelText(/review/)).not.toBeInTheDocument();
      });

      it("handles empty notes field for blocked status", () => {
        const issue = createTestIssue({
          title: "Blocked issue",
          status: "blocked",
          notes: "",
        });
        render(<IssueCard issue={issue} />);

        // Empty string notes should not trigger Help badge
        expect(screen.queryByText("Help")).not.toBeInTheDocument();
      });
    });
  });

  describe("open status badge", () => {
    it('shows "Ready" badge when card is in Open column with design', () => {
      const issue = createTestIssue({
        design: "Implementation plan for feature X",
      });
      render(<IssueCard issue={issue} columnId="ready" />);

      expect(screen.getByText("Ready")).toBeInTheDocument();
      expect(screen.getByText("✅")).toBeInTheDocument();
    });

    it('shows "Needs Plan" badge when card is in Open column without design', () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="ready" />);

      expect(screen.getByText("Needs Plan")).toBeInTheDocument();
      expect(screen.getByText("📋")).toBeInTheDocument();
    });

    it("does not show open status badge in other columns", () => {
      const issue = createTestIssue({ design: "Some design content" });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(screen.queryByText("Ready")).not.toBeInTheDocument();
      expect(screen.queryByText("Needs Plan")).not.toBeInTheDocument();
    });

    it("does not show open status badge when no columnId is provided", () => {
      const issue = createTestIssue({ design: "Some design content" });
      render(<IssueCard issue={issue} />);

      expect(screen.queryByText("Ready")).not.toBeInTheDocument();
      expect(screen.queryByText("Needs Plan")).not.toBeInTheDocument();
    });

    it("Ready badge has correct aria-label", () => {
      const issue = createTestIssue({ design: "Design document" });
      render(<IssueCard issue={issue} columnId="ready" />);

      expect(screen.getByLabelText("Ready")).toBeInTheDocument();
    });

    it("Needs Plan badge has correct aria-label", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="ready" />);

      expect(screen.getByLabelText("Needs Plan")).toBeInTheDocument();
    });

    it("badge icon has aria-hidden attribute", () => {
      const issue = createTestIssue({ design: "Design doc" });
      render(<IssueCard issue={issue} columnId="ready" />);

      const icon = screen.getByText("✅");
      expect(icon).toHaveAttribute("aria-hidden", "true");
    });

    it("applies openReady class to Ready badge", () => {
      const issue = createTestIssue({ design: "Design doc" });
      render(<IssueCard issue={issue} columnId="ready" />);

      const badge = screen.getByLabelText("Ready");
      expect(badge.className).toMatch(/openReady/);
    });

    it("applies openNeedsPlan class to Needs Plan badge", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="ready" />);

      const badge = screen.getByLabelText("Needs Plan");
      expect(badge.className).toMatch(/openNeedsPlan/);
    });
  });

  describe("issue ID tooltip", () => {
    it("ID span has title attribute with full issue ID for hover tooltip", () => {
      const issue = createTestIssue({ id: "loomcli-af78e9a2.1.2" });
      const { container } = render(<IssueCard issue={issue} />);

      const idSpan = container.querySelector(`.${styles.id}`);
      expect(idSpan).toHaveAttribute("title", "loomcli-af78e9a2.1.2");
    });

    it("title attribute shows full ID even when display text is truncated", () => {
      const longId = "some-very-long-issue-id-12345";
      const issue = createTestIssue({ id: longId });
      const { container } = render(<IssueCard issue={issue} />);

      const idSpan = container.querySelector(`.${styles.id}`);
      // Display text is truncated
      expect(idSpan).toHaveTextContent("some-very-...");
      // But title shows the full ID
      expect(idSpan).toHaveAttribute("title", longId);
    });

    it("title attribute matches display text for short IDs", () => {
      const issue = createTestIssue({ id: "loomcli-pso6j" });
      const { container } = render(<IssueCard issue={issue} />);

      const idSpan = container.querySelector(`.${styles.id}`);
      expect(idSpan).toHaveTextContent("loomcli-pso6j");
      expect(idSpan).toHaveAttribute("title", "loomcli-pso6j");
    });
  });

  describe("CSS module classes", () => {
    it("renders card with issueCard class from CSS module", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const article = container.querySelector("article");
      // CSS Modules hashes class names, so we check for the pattern
      expect(article?.className).toMatch(/issueCard/);
    });

    it("selected class exists in CSS module styles object", () => {
      // Verify that the .selected class is defined in the CSS module
      // This ensures the CSS module exports the selected class that can be applied
      expect(styles.selected).toBeDefined();
      expect(styles.selected).toBeTruthy();
    });
  });

  describe("review column interactions", () => {
    it("does not render inline approve/reject buttons in review column", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="review" onClick={vi.fn()} />);

      expect(screen.queryByTestId("approve-button")).not.toBeInTheDocument();
      expect(screen.queryByTestId("reject-button")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Approve")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Reject")).not.toBeInTheDocument();
    });

    it("still opens detail flow by clicking the review card", () => {
      const issue = createTestIssue({ id: "review-card-click-123" });
      const onClick = vi.fn();
      render(<IssueCard issue={issue} columnId="review" onClick={onClick} />);

      fireEvent.click(screen.getByRole("button"));

      expect(onClick).toHaveBeenCalledWith(issue);
      expect(onClick).toHaveBeenCalledTimes(1);
    });
  });

  describe("assignee badge", () => {
    it("renders assignee badge when issue has assignee and columnId is open", () => {
      const issue = createTestIssue({ assignee: "alice" });
      render(<IssueCard issue={issue} columnId="open" />);

      const badge = screen.getByTestId("issue-card-assignee");
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveTextContent("A");
    });

    it("renders assignee badge with tooltip showing assignee name", () => {
      const issue = createTestIssue({ assignee: "alice" });
      render(<IssueCard issue={issue} columnId="open" />);

      const badge = screen.getByTestId("issue-card-assignee");
      expect(badge).toHaveAttribute("title", "Assignee: alice");
    });

    it("assignee badge has aria-label for screen readers", () => {
      const issue = createTestIssue({ assignee: "alice" });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getByLabelText("Assignee: alice")).toBeInTheDocument();
    });

    it("does not render assignee badge in in_progress column (AgentRow handles it)", () => {
      const issue = createTestIssue({ assignee: "nova" });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(
        screen.queryByTestId("issue-card-assignee"),
      ).not.toBeInTheDocument();
    });

    it("does not render assignee badge in review column (AgentRow handles it)", () => {
      const issue = createTestIssue({ assignee: "nova" });
      render(<IssueCard issue={issue} columnId="review" />);

      expect(
        screen.queryByTestId("issue-card-assignee"),
      ).not.toBeInTheDocument();
    });

    it("renders assignee badge in done column", () => {
      const issue = createTestIssue({ assignee: "bob" });
      render(<IssueCard issue={issue} columnId="done" />);

      const badge = screen.getByTestId("issue-card-assignee");
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveTextContent("B");
    });

    it("renders assignee badge in blocked column", () => {
      const issue = createTestIssue({ assignee: "charlie" });
      render(<IssueCard issue={issue} columnId="blocked" />);

      const badge = screen.getByTestId("issue-card-assignee");
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveTextContent("C");
    });

    it("does not render assignee badge when assignee is undefined", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="open" />);

      expect(
        screen.queryByTestId("issue-card-assignee"),
      ).not.toBeInTheDocument();
    });

    it("does not render assignee badge when assignee is empty string", () => {
      const issue = createTestIssue({ assignee: "" });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(
        screen.queryByTestId("issue-card-assignee"),
      ).not.toBeInTheDocument();
    });

    it("strips [H] prefix from assignee display", () => {
      const issue = createTestIssue({ assignee: "[H] Alice" });
      render(<IssueCard issue={issue} columnId="open" />);

      const badge = screen.getByTestId("issue-card-assignee");
      expect(badge).toHaveTextContent("A");
      expect(badge).toHaveAttribute("title", "Assignee: Alice");
    });

    it("renders both owner badge and assignee badge side by side", () => {
      const issue = createTestIssue({
        owner: "val@test",
        assignee: "alice",
      });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getByTestId("issue-card-owner")).toBeInTheDocument();
      expect(screen.getByTestId("issue-card-assignee")).toBeInTheDocument();
    });

    it("renders only owner badge when no assignee", () => {
      const issue = createTestIssue({ owner: "val@test" });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getByTestId("issue-card-owner")).toBeInTheDocument();
      expect(
        screen.queryByTestId("issue-card-assignee"),
      ).not.toBeInTheDocument();
    });

    it("renders only assignee badge when no owner", () => {
      const issue = createTestIssue({ assignee: "alice" });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.queryByTestId("issue-card-owner")).not.toBeInTheDocument();
      expect(screen.getByTestId("issue-card-assignee")).toBeInTheDocument();
    });

    it("applies a colored background style to the assignee badge", () => {
      const issue = createTestIssue({ assignee: "alice" });
      render(<IssueCard issue={issue} columnId="open" />);

      const badge = screen.getByTestId("issue-card-assignee");
      // backgroundColor inline style is set from getAvatarColor — just verify it's present
      expect(badge.style.backgroundColor).not.toBe("");
    });

    it("renders multi-word assignee initials (max 2 chars)", () => {
      const issue = createTestIssue({ assignee: "Alice Smith" });
      render(<IssueCard issue={issue} columnId="open" />);

      const badge = screen.getByTestId("issue-card-assignee");
      expect(badge).toHaveTextContent("AS");
    });
  });

  describe("TypeIcon integration", () => {
    it('renders TypeIcon with data-type="bug" for issue_type="bug"', () => {
      const issue = createTestIssue({ issue_type: "bug" });
      const { container } = render(<IssueCard issue={issue} />);

      const icon = container.querySelector("svg[data-type]");
      expect(icon).toBeInTheDocument();
      expect(icon).toHaveAttribute("data-type", "bug");
    });

    it('renders TypeIcon with data-type="feature" for issue_type="feature"', () => {
      const issue = createTestIssue({ issue_type: "feature" });
      const { container } = render(<IssueCard issue={issue} />);

      const icon = container.querySelector("svg[data-type]");
      expect(icon).toBeInTheDocument();
      expect(icon).toHaveAttribute("data-type", "feature");
    });

    it("renders no TypeIcon when issue_type is undefined", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      const icon = container.querySelector("svg[data-type]");
      expect(icon).not.toBeInTheDocument();
    });

    it("renders no TypeIcon for unknown issue_type", () => {
      const issue = createTestIssue({ issue_type: "custom" });
      const { container } = render(<IssueCard issue={issue} />);

      const icon = container.querySelector("svg[data-type]");
      expect(icon).not.toBeInTheDocument();
    });
  });

  describe("label pills", () => {
    it("renders label pills when issue has labels", () => {
      const issue = createTestIssue({ labels: ["bug", "urgent"] });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getByTestId("issue-card-labels")).toBeInTheDocument();
      expect(screen.getAllByTestId("issue-card-label")).toHaveLength(2);
      expect(screen.getByText("bug")).toBeInTheDocument();
      expect(screen.getByText("urgent")).toBeInTheDocument();
    });

    it("does not render labels container when labels is undefined", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.queryByTestId("issue-card-labels")).not.toBeInTheDocument();
    });

    it("does not render labels container when labels is empty array", () => {
      const issue = createTestIssue({ labels: [] });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.queryByTestId("issue-card-labels")).not.toBeInTheDocument();
    });

    it("shows at most 3 label pills with overflow count", () => {
      const issue = createTestIssue({ labels: ["a", "b", "c", "d", "e"] });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getAllByTestId("issue-card-label")).toHaveLength(3);
      const overflow = screen.getByTestId("issue-card-labels-overflow");
      expect(overflow).toBeInTheDocument();
      expect(overflow).toHaveTextContent("+2");
    });

    it("does not show overflow indicator when exactly 3 labels", () => {
      const issue = createTestIssue({ labels: ["a", "b", "c"] });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getAllByTestId("issue-card-label")).toHaveLength(3);
      expect(
        screen.queryByTestId("issue-card-labels-overflow"),
      ).not.toBeInTheDocument();
    });

    it("renders single label without overflow", () => {
      const issue = createTestIssue({ labels: ["solo"] });
      render(<IssueCard issue={issue} columnId="open" />);

      expect(screen.getAllByTestId("issue-card-label")).toHaveLength(1);
      expect(screen.getByText("solo")).toBeInTheDocument();
      expect(
        screen.queryByTestId("issue-card-labels-overflow"),
      ).not.toBeInTheDocument();
    });

    it.each(["open", "in_progress", "review", "blocked", "done", "backlog"])(
      "labels render in %s column",
      (columnId) => {
        const issue = createTestIssue({ labels: ["tag"] });
        render(<IssueCard issue={issue} columnId={columnId} />);

        expect(screen.getByTestId("issue-card-labels")).toBeInTheDocument();
      },
    );
  });
});
