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

  it("renders and invokes the delete action when provided", () => {
    const onDelete = vi.fn();
    render(
      <IssueHeader issue={mockIssue} onClose={() => {}} onDelete={onDelete} />,
    );
    const button = screen.getByTestId("header-delete-button");
    expect(button).toHaveAccessibleName("Delete issue");
    fireEvent.click(button);
    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it("disables the delete action while deleting", () => {
    render(
      <IssueHeader
        issue={mockIssue}
        onClose={() => {}}
        onDelete={() => {}}
        isDeleting={true}
      />,
    );
    expect(screen.getByTestId("header-delete-button")).toBeDisabled();
    expect(screen.getByTestId("header-delete-button")).toHaveAccessibleName(
      "Deleting issue",
    );
  });

  it("applies custom className", () => {
    render(
      <IssueHeader issue={mockIssue} onClose={() => {}} className="custom" />,
    );
    expect(screen.getByTestId("issue-header")).toHaveClass("custom");
  });

  it("does not render the epic runner action by default", () => {
    render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
    expect(
      screen.queryByTestId("header-run-epic-button"),
    ).not.toBeInTheDocument();
  });

  it("renders the epic runner action when provided", () => {
    render(
      <IssueHeader issue={mockIssue} onClose={() => {}} onRunEpic={() => {}} />,
    );
    const button = screen.getByTestId("header-run-epic-button");
    expect(button).toBeInTheDocument();
    expect(button).toHaveTextContent("Run epic");
    expect(button).toHaveAccessibleName("Run epic workflow");
  });

  it("calls onRunEpic when the epic runner action is clicked", () => {
    const onRunEpic = vi.fn();
    render(
      <IssueHeader
        issue={mockIssue}
        onClose={() => {}}
        onRunEpic={onRunEpic}
      />,
    );
    fireEvent.click(screen.getByTestId("header-run-epic-button"));
    expect(onRunEpic).toHaveBeenCalledTimes(1);
  });

  it("disables the epic runner action while starting", () => {
    render(
      <IssueHeader
        issue={mockIssue}
        onClose={() => {}}
        onRunEpic={() => {}}
        isRunningEpic={true}
      />,
    );
    const button = screen.getByTestId("header-run-epic-button");
    expect(button).toBeDisabled();
    expect(button).toHaveTextContent("Starting");
    expect(button).toHaveAccessibleName("Starting epic runner");
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

  describe("PR links", () => {
    it("does not render PR links when prUrl and prNumber are not provided", () => {
      render(<IssueHeader issue={mockIssue} onClose={() => {}} />);
      expect(
        screen.queryByTestId("header-pr-view-link"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("header-pr-merge-link"),
      ).not.toBeInTheDocument();
    });

    it("does not render PR links when only prUrl is provided", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
        />,
      );
      expect(
        screen.queryByTestId("header-pr-view-link"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("header-pr-merge-link"),
      ).not.toBeInTheDocument();
    });

    it("does not render PR links when only prNumber is provided", () => {
      render(
        <IssueHeader issue={mockIssue} onClose={() => {}} prNumber="42" />,
      );
      expect(
        screen.queryByTestId("header-pr-view-link"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("header-pr-merge-link"),
      ).not.toBeInTheDocument();
    });

    it("renders view link with correct text when prUrl and prNumber provided", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const viewLink = screen.getByTestId("header-pr-view-link");
      expect(viewLink).toBeInTheDocument();
      expect(viewLink).toHaveTextContent("↗ #42");
    });

    it("renders merge link with correct text when prUrl and prNumber provided", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const mergeLink = screen.getByTestId("header-pr-merge-link");
      expect(mergeLink).toBeInTheDocument();
      expect(mergeLink).toHaveTextContent("→ merge #42");
    });

    it("view link has correct href, target, and rel attributes", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const viewLink = screen.getByTestId("header-pr-view-link");
      expect(viewLink).toHaveAttribute(
        "href",
        "https://github.com/owner/repo/pull/42",
      );
      expect(viewLink).toHaveAttribute("target", "_blank");
      expect(viewLink).toHaveAttribute("rel", "noopener noreferrer");
    });

    it("merge link has correct href, target, and rel attributes", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const mergeLink = screen.getByTestId("header-pr-merge-link");
      expect(mergeLink).toHaveAttribute(
        "href",
        "https://github.com/owner/repo/pull/42",
      );
      expect(mergeLink).toHaveAttribute("target", "_blank");
      expect(mergeLink).toHaveAttribute("rel", "noopener noreferrer");
    });

    it("view link has correct aria-label", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const viewLink = screen.getByTestId("header-pr-view-link");
      expect(viewLink).toHaveAttribute("aria-label", "View pull request #42");
    });

    it("merge link has correct aria-label", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const mergeLink = screen.getByTestId("header-pr-merge-link");
      expect(mergeLink).toHaveAttribute("aria-label", "Merge pull request #42");
    });

    it("view link calls stopPropagation on click", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const viewLink = screen.getByTestId("header-pr-view-link");
      const clickEvent = new MouseEvent("click", { bubbles: true });
      const stopPropagation = vi.spyOn(clickEvent, "stopPropagation");
      viewLink.dispatchEvent(clickEvent);
      expect(stopPropagation).toHaveBeenCalled();
    });

    it("merge link calls stopPropagation on click", () => {
      render(
        <IssueHeader
          issue={mockIssue}
          onClose={() => {}}
          prUrl="https://github.com/owner/repo/pull/42"
          prNumber="42"
        />,
      );
      const mergeLink = screen.getByTestId("header-pr-merge-link");
      const clickEvent = new MouseEvent("click", { bubbles: true });
      const stopPropagation = vi.spyOn(clickEvent, "stopPropagation");
      mergeLink.dispatchEvent(clickEvent);
      expect(stopPropagation).toHaveBeenCalled();
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
