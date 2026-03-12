/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueHeader component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { IssueHeader } from "../IssueHeader";

const mockIssue: Issue = {
  id: "test-123",
  title: "Test Issue Title",
  status: "in_progress",
  priority: 2,
  created_at: "2026-01-23T00:00:00Z",
  updated_at: "2026-01-23T00:00:00Z",
};

describe("IssueHeader", () => {
  it("renders issue ID", () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByTestId("issue-id")).toHaveTextContent("test-123");
  });

  it("renders issue title", () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByTestId("issue-title")).toHaveTextContent(
      "Test Issue Title",
    );
  });

  it("renders status badge with formatted text", () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    const badge = screen.getByTestId("issue-status-badge");
    expect(badge).toHaveTextContent("In Progress");
    expect(badge).toHaveAttribute("data-status", "in_progress");
  });

  it('renders status badge with role="status"', () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it('defaults to "Open" when status is undefined', () => {
    const issueNoStatus = { ...mockIssue, status: undefined };
    render(<IssueHeader issue={issueNoStatus} onClose={() => {}} />);
    expect(screen.getByTestId("issue-status-badge")).toHaveTextContent("Open");
  });

  it("calls onClose when close button clicked", () => {
    const onClose = vi.fn();
    render(<IssueHeader issue={mockIssue} onClose={onClose} />);
    fireEvent.click(screen.getByTestId("header-close-button"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("close button has accessible label", () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(screen.getByLabelText("Close panel")).toBeInTheDocument();
  });

  it("applies custom className", () => {
    render(
      <IssueHeader issue={mockIssue} onClose={() => {}} className="custom" />,
    );
    expect(screen.getByTestId("issue-header")).toHaveClass("custom");
  });

  it("renders open status with correct data attribute", () => {
    const openIssue = { ...mockIssue, status: "open" };
    render(<IssueHeader issue={openIssue} onClose={() => {}} />);
    const badge = screen.getByTestId("issue-status-badge");
    expect(badge).toHaveTextContent("Open");
    expect(badge).toHaveAttribute("data-status", "open");
  });

  it("renders closed status with correct data attribute", () => {
    const closedIssue = { ...mockIssue, status: "closed" };
    render(<IssueHeader issue={closedIssue} onClose={() => {}} />);
    const badge = screen.getByTestId("issue-status-badge");
    expect(badge).toHaveTextContent("Closed");
    expect(badge).toHaveAttribute("data-status", "closed");
  });

  it("renders blocked status with correct data attribute", () => {
    const blockedIssue = { ...mockIssue, status: "blocked" };
    render(<IssueHeader issue={blockedIssue} onClose={() => {}} />);
    const badge = screen.getByTestId("issue-status-badge");
    expect(badge).toHaveTextContent("Blocked");
    expect(badge).toHaveAttribute("data-status", "blocked");
  });

  describe("priority badge", () => {
    it("shows priority badge when showPriority is true", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          showPriority={true}
        />,
      );
      expect(screen.getByTestId("header-priority-badge")).toBeInTheDocument();
    });

    it("does not show priority badge when showPriority is false", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          showPriority={false}
        />,
      );
      expect(
        screen.queryByTestId("header-priority-badge"),
      ).not.toBeInTheDocument();
    });

    it("does not show priority badge when showPriority is not provided", () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
      expect(
        screen.queryByTestId("header-priority-badge"),
      ).not.toBeInTheDocument();
    });

    it("displays correct priority label for P0", () => {
      const p0Issue = { ...mockIssue, priority: 0 };
      render(
        <IssueHeader issue={p0Issue} onClose={() => {}} showPriority={true} />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveTextContent("P0");
      expect(badge).toHaveAttribute("data-priority", "0");
    });

    it("displays correct priority label for P1", () => {
      const p1Issue = { ...mockIssue, priority: 1 };
      render(
        <IssueHeader issue={p1Issue} onClose={() => {}} showPriority={true} />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveTextContent("P1");
      expect(badge).toHaveAttribute("data-priority", "1");
    });

    it("displays correct priority label for P2", () => {
      const p2Issue = { ...mockIssue, priority: 2 };
      render(
        <IssueHeader issue={p2Issue} onClose={() => {}} showPriority={true} />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveTextContent("P2");
      expect(badge).toHaveAttribute("data-priority", "2");
    });

    it("displays correct priority label for P3", () => {
      const p3Issue = { ...mockIssue, priority: 3 };
      render(
        <IssueHeader issue={p3Issue} onClose={() => {}} showPriority={true} />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveTextContent("P3");
      expect(badge).toHaveAttribute("data-priority", "3");
    });

    it("displays correct priority label for P4", () => {
      const p4Issue = { ...mockIssue, priority: 4 };
      render(
        <IssueHeader issue={p4Issue} onClose={() => {}} showPriority={true} />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveTextContent("P4");
      expect(badge).toHaveAttribute("data-priority", "4");
    });

    it("has accessible aria-label with full priority description", () => {
      const p0Issue = { ...mockIssue, priority: 0 };
      render(
        <IssueHeader issue={p0Issue} onClose={() => {}} showPriority={true} />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveAttribute("aria-label", "Priority: P0 - Critical");
    });

    it("calls onPriorityClick when priority badge is clicked", () => {
      const onPriorityClick = vi.fn();
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          showPriority={true}
          onPriorityClick={onPriorityClick}
        />,
      );
      fireEvent.click(screen.getByTestId("header-priority-badge"));
      expect(onPriorityClick).toHaveBeenCalledTimes(1);
    });

    it("defaults to P2 for unknown priority values", () => {
      const unknownPriorityIssue = { ...mockIssue, priority: 99 };
      render(
        <IssueHeader
          issue={unknownPriorityIssue}
          onClose={() => {}}
          showPriority={true}
        />,
      );
      const badge = screen.getByTestId("header-priority-badge");
      expect(badge).toHaveTextContent("P2");
    });
  });

  describe("sticky mode", () => {
    it("applies sticky class when sticky prop is true", () => {
      render(
        <IssueHeader issue={mockIssue} onClose={() => {}} sticky={true} />,
      );
      const header = screen.getByTestId("issue-header");
      // CSS modules mangle class names, so check for pattern containing 'sticky'
      expect(header.className).toMatch(/sticky/i);
    });

    it("does not apply sticky class when sticky prop is false", () => {
      render(
        <IssueHeader issue={mockIssue} onClose={() => {}} sticky={false} />,
      );
      const header = screen.getByTestId("issue-header");
      expect(header.className).not.toMatch(/_sticky_/);
    });

    it("does not apply sticky class when sticky prop is not provided", () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
      const header = screen.getByTestId("issue-header");
      expect(header.className).not.toMatch(/_sticky_/);
    });
  });
});
