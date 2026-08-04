/**
 * @vitest-environment jsdom
 */

/**
 * IssueCard no longer renders an inline AgentRow (Aether V3 alignment):
 * design tickets carry only id / type icon / badge / title / footer avatar —
 * live agent activity lives in the issue detail panel and the epic header.
 * These tests pin that the agent row stays gone across the column states
 * that used to show it.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { IssueCard } from "../IssueCard";

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

describe("IssueCard agent-row removal (Aether V3)", () => {
  it.each(["in_progress", "review", "open", "done", "backlog", "blocked"])(
    'does not render an agent row for columnId="%s" even with an assignee',
    (columnId) => {
      const issue = createTestIssue({ assignee: "nova" });
      const { container } = render(
        <IssueCard issue={issue} columnId={columnId} />,
      );

      const agentRow = container.querySelector('[class*="agentRow"]');
      expect(agentRow).not.toBeInTheDocument();
      // No live-activity strings either.
      expect(
        screen.queryByText("Submitted for review"),
      ).not.toBeInTheDocument();
      expect(screen.queryByText("agent missing")).not.toBeInTheDocument();
    },
  );

  it("still renders the title on assigned in-progress cards", () => {
    const issue = createTestIssue({
      title: "Important Task",
      assignee: "nova",
    });
    render(<IssueCard issue={issue} columnId="in_progress" />);

    expect(
      screen.getByRole("heading", { name: "Important Task" }),
    ).toBeInTheDocument();
  });

  it("still renders the blocked badge on assigned cards", () => {
    const issue = createTestIssue({ assignee: "nova" });
    render(
      <IssueCard issue={issue} columnId="in_progress" blockedByCount={2} />,
    );

    expect(screen.getByLabelText("Blocked by 2 issues")).toBeInTheDocument();
  });
});
