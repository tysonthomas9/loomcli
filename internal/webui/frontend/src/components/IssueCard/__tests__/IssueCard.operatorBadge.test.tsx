/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the operator badge on IssueCard.
 * An issue carrying the reserved "operator" label is parked for a human: it
 * stays open, but no agent selects it, so the board must say so at a glance.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { IssueCard } from "../IssueCard";

vi.mock("@/hooks/workspace", async (importOriginal) => {
  const orig = await importOriginal<typeof import("@/hooks/workspace")>();
  return {
    ...orig,
    useWorkspaceContext: vi.fn(() => ({
      workspace: null,
      repos: [],
      groups: [],
      agents: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      getRepoByName: vi.fn(),
      getReposByGroup: vi.fn(() => []),
      getAgentByName: vi.fn(),
      activeWorkspaceName: null,
      setActiveWorkspace: vi.fn(),
      selectedRepoNames: new Set<string>(),
      activeRepos: [],
      activeRepoNames: [],
      isAllSelected: true,
      selectRepos: vi.fn(),
      selectAll: vi.fn(),
      toggleRepo: vi.fn(),
      sourceReposFilter: undefined,
      isMultiRepo: false,
    })),
  };
});

function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "test-issue-abc123",
    title: "Test Issue Title",
    status: "open",
    priority: 2,
    created_at: "2026-01-15T10:30:00Z",
    updated_at: "2026-01-15T10:30:00Z",
    ...overrides,
  };
}

describe("IssueCard operator badge", () => {
  it("renders the badge when the issue carries the operator label", () => {
    render(<IssueCard issue={createTestIssue({ labels: ["operator"] })} />);

    const badge = screen.getByTestId("issue-card-operator-badge");
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent("Operator");
    expect(badge).toHaveAccessibleName("Parked for an operator");
  });

  it("marks the card so the parked state is visible to assistive tech", () => {
    render(<IssueCard issue={createTestIssue({ labels: ["operator"] })} />);

    const card = screen.getByLabelText(/operator only/);
    expect(card).toHaveAttribute("data-operator-parked", "true");
  });

  it("does not render the badge without the label", () => {
    render(<IssueCard issue={createTestIssue({ labels: ["backend"] })} />);

    expect(
      screen.queryByTestId("issue-card-operator-badge"),
    ).not.toBeInTheDocument();
  });

  // Exact spelling only — fleet-db normalizes nothing, so a near miss is an
  // ordinary label and the task is still agent-selectable.
  it("does not render the badge for near-miss label spellings", () => {
    render(
      <IssueCard
        issue={createTestIssue({ labels: ["operator-notes", "Operator"] })}
      />,
    );

    expect(
      screen.queryByTestId("issue-card-operator-badge"),
    ).not.toBeInTheDocument();
  });
});
