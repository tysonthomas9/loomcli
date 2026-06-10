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
      const issue = createTestIssue({ id: "issue-abc123def456" });
      render(<IssueCard issue={issue} />);

      // Should preserve prefix and truncate: "issue-abc12..."
      expect(screen.getByText("issue-abc12...")).toBeInTheDocument();
    });

    it("renders short ID as-is", () => {
      const issue = createTestIssue({ id: "loom-xyz" });
      render(<IssueCard issue={issue} />);

      expect(screen.getByText("loom-xyz")).toBeInTheDocument();
    });

    it("does not render a visible priority badge (Aether V3 tickets)", () => {
      const issue = createTestIssue({ priority: 1 });
      render(<IssueCard issue={issue} />);

      expect(screen.queryByText("P1")).not.toBeInTheDocument();
    });

    it("renders with article element", () => {
      const issue = createTestIssue();
      const { container } = render(<IssueCard issue={issue} />);

      expect(container.querySelector("article")).toBeInTheDocument();
    });
  });

  // The visible priority badge was removed for the Aether V3 design (tickets
  // carry no priority chip); priority survives only as the card's
  // data-priority attribute for CSS hooks.
  describe("priority data attribute", () => {
    it.each([0, 1, 2, 3, 4] as const)(
      'card has data-priority="%i" and no P%i badge',
      (priority) => {
        const issue = createTestIssue({ priority });
        const { container } = render(<IssueCard issue={issue} />);

        expect(screen.queryByText(`P${priority}`)).not.toBeInTheDocument();
        expect(container.querySelector("article")).toHaveAttribute(
          "data-priority",
          String(priority),
        );
      },
    );

    it("defaults to data-priority 4 when priority is undefined", () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      const { container } = render(<IssueCard issue={issue} />);

      expect(container.querySelector("article")).toHaveAttribute(
        "data-priority",
        "4",
      );
    });

    it("defaults to data-priority 4 for out of range priority (negative)", () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: -1 });
      const { container } = render(<IssueCard issue={issue} />);

      expect(container.querySelector("article")).toHaveAttribute(
        "data-priority",
        "4",
      );
    });

    it("defaults to data-priority 4 for out of range priority (> 4)", () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 99 });
      const { container } = render(<IssueCard issue={issue} />);

      expect(container.querySelector("article")).toHaveAttribute(
        "data-priority",
        "4",
      );
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

    it("has no priority badge aria-label (badge removed in Aether V3)", () => {
      const issue = createTestIssue({ priority: 0 });
      render(<IssueCard issue={issue} />);

      expect(
        screen.queryByLabelText("Priority: P0 - Critical"),
      ).not.toBeInTheDocument();
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
      expect(screen.queryByText("P2")).not.toBeInTheDocument();
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
      expect(screen.queryByText("P0")).not.toBeInTheDocument();
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
      // Text-only badge — the ⏸ emoji was dropped for the Aether V3 design.
      expect(screen.queryByText("⏸")).not.toBeInTheDocument();
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
      // Badges are text-only (no emoji) per the Aether V3 design plan-badge.
      it("shows text-only Plan badge for plan review", () => {
        const issue = createTestIssue({
          title: "Design proposal",
          status: "review",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Plan review");
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent("Plan");
        expect(screen.queryByText("📝")).not.toBeInTheDocument();
      });

      it("shows text-only Code badge for code review", () => {
        const issue = createTestIssue({
          title: "Feature implementation",
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/10",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Code review");
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent("Code");
        expect(screen.queryByText("🔍")).not.toBeInTheDocument();
      });

      it("shows text-only Help badge for blocked issues with notes", () => {
        const issue = createTestIssue({
          title: "Needs assistance",
          status: "blocked",
          notes: "Need help with API integration",
        });
        render(<IssueCard issue={issue} />);

        const badge = screen.getByLabelText("Help review");
        expect(badge).toBeInTheDocument();
        expect(badge).toHaveTextContent("Help");
        expect(screen.queryByText("❓")).not.toBeInTheDocument();
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

  // The "Ready"/"Needs Plan" open-status badge was removed for the Aether V3
  // design — open-column tickets render plain (id / icon / title / footer).
  describe("open status badge removal (Aether V3)", () => {
    it("does not show a badge in the Open column even with a design", () => {
      const issue = createTestIssue({
        design: "Implementation plan for feature X",
      });
      render(<IssueCard issue={issue} columnId="ready" />);

      expect(screen.queryByText("Ready")).not.toBeInTheDocument();
      expect(screen.queryByText("✅")).not.toBeInTheDocument();
    });

    it("does not show a badge in the Open column without a design", () => {
      const issue = createTestIssue();
      render(<IssueCard issue={issue} columnId="ready" />);

      expect(screen.queryByText("Needs Plan")).not.toBeInTheDocument();
      expect(screen.queryByText("📋")).not.toBeInTheDocument();
    });

    it("does not show open status badge in other columns", () => {
      const issue = createTestIssue({ design: "Some design content" });
      render(<IssueCard issue={issue} columnId="in_progress" />);

      expect(screen.queryByText("Ready")).not.toBeInTheDocument();
      expect(screen.queryByText("Needs Plan")).not.toBeInTheDocument();
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
});
