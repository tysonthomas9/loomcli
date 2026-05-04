/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for cellRenderers renderCellContent function.
 * Tests the "repo" column rendering case.
 */

import { render } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { renderCellContent } from "../cellRenderers";

/**
 * Create a minimal test issue with required fields.
 */
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "loom-abc",
    title: "Test Issue",
    priority: 2,
    status: "open",
    issue_type: "task",
    assignee: "test-user",
    created_at: "2024-01-15T10:30:00Z",
    updated_at: "2024-01-15T12:00:00Z",
    ...overrides,
  };
}

/**
 * Helper to render a cell and return its container.
 */
function renderCell(columnId: string, value: unknown, issue: Issue) {
  const content = renderCellContent(columnId, value, issue);
  return render(<>{content}</>);
}

describe("renderCellContent", () => {
  describe("repo column", () => {
    it("renders repo name when value is present", () => {
      const issue = createMockIssue({ repo: "frontend" });
      const { container } = renderCell("repo", "frontend", issue);

      const span = container.querySelector(".issue-table__repo");
      expect(span).toBeInTheDocument();
      expect(span).toHaveTextContent("frontend");
    });

    it("renders em dash when value is null", () => {
      const issue = createMockIssue();
      const { container } = renderCell("repo", null, issue);

      const span = container.querySelector(".issue-table__repo");
      expect(span).toBeInTheDocument();
      expect(span).toHaveTextContent("\u2014");
    });

    it("renders em dash when value is undefined", () => {
      const issue = createMockIssue();
      const { container } = renderCell("repo", undefined, issue);

      const span = container.querySelector(".issue-table__repo");
      expect(span).toBeInTheDocument();
      expect(span).toHaveTextContent("\u2014");
    });

    it("renders repo name string for various repo names", () => {
      const issue = createMockIssue({ repo: "my-backend-api" });
      const { container } = renderCell("repo", "my-backend-api", issue);

      const span = container.querySelector(".issue-table__repo");
      expect(span).toHaveTextContent("my-backend-api");
    });

    it("has issue-table__repo class", () => {
      const issue = createMockIssue({ repo: "service" });
      const { container } = renderCell("repo", "service", issue);

      const span = container.querySelector(".issue-table__repo");
      expect(span).toBeInTheDocument();
    });

    it("renders empty string value as em dash", () => {
      const issue = createMockIssue();
      const { container } = renderCell("repo", "", issue);

      const span = container.querySelector(".issue-table__repo");
      expect(span).toBeInTheDocument();
      // Empty string is falsy, so should show em dash
      expect(span).toHaveTextContent("\u2014");
    });
  });
});
