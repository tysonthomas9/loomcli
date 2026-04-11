/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RepoBadge rendering within IssueCard footer.
 * Tests that RepoBadge appears when isMultiRepo is true and issue.repo is set,
 * and does not appear otherwise.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue } from "@/types";

import { IssueCard } from "../IssueCard";

// Mock useWorkspaceContext to control isMultiRepo
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

const mockUseWorkspaceContext = vi.mocked(useWorkspaceContext);

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

/**
 * Helper to set isMultiRepo value on the mock.
 */
function setIsMultiRepo(value: boolean) {
  mockUseWorkspaceContext.mockReturnValue({
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
    isMultiRepo: value,
  });
}

describe("IssueCard RepoBadge", () => {
  it("renders RepoBadge in footer when isMultiRepo is true and issue.repo exists", () => {
    setIsMultiRepo(true);
    const issue = createTestIssue({ repo: "frontend" });
    render(<IssueCard issue={issue} />);

    expect(screen.getByLabelText("Repository: frontend")).toBeInTheDocument();
    expect(screen.getByText("frontend")).toBeInTheDocument();
  });

  it("does not render RepoBadge when issue.repo is undefined", () => {
    setIsMultiRepo(true);
    const issue = createTestIssue();
    render(<IssueCard issue={issue} />);

    expect(screen.queryByLabelText(/Repository:/)).not.toBeInTheDocument();
  });

  it("does not render RepoBadge when isMultiRepo is false", () => {
    setIsMultiRepo(false);
    const issue = createTestIssue({ repo: "frontend" });
    render(<IssueCard issue={issue} />);

    expect(
      screen.queryByLabelText("Repository: frontend"),
    ).not.toBeInTheDocument();
  });

  it("does not render footer div when isMultiRepo is false", () => {
    setIsMultiRepo(false);
    const issue = createTestIssue({ repo: "backend" });
    const { container } = render(<IssueCard issue={issue} />);

    // cardFooter div should not be present
    const footer = container.querySelector("[class*='cardFooter']");
    expect(footer).not.toBeInTheDocument();
  });

  it("does not render footer div when issue.repo is undefined even if isMultiRepo", () => {
    setIsMultiRepo(true);
    const issue = createTestIssue();
    const { container } = render(<IssueCard issue={issue} />);

    const footer = container.querySelector("[class*='cardFooter']");
    expect(footer).not.toBeInTheDocument();
  });

  it("renders footer div with cardFooter class when both conditions are met", () => {
    setIsMultiRepo(true);
    const issue = createTestIssue({ repo: "api-service" });
    const { container } = render(<IssueCard issue={issue} />);

    const footer = container.querySelector("[class*='cardFooter']");
    expect(footer).toBeInTheDocument();
  });

  it("renders RepoBadge with correct repo name for different repos", () => {
    setIsMultiRepo(true);
    const issue = createTestIssue({ repo: "my-backend-service" });
    render(<IssueCard issue={issue} />);

    expect(
      screen.getByLabelText("Repository: my-backend-service"),
    ).toBeInTheDocument();
    expect(screen.getByText("my-backend-service")).toBeInTheDocument();
  });
});
