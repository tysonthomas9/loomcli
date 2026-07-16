/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SplitDetailSummary component.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

import { SplitDetailSummary } from "../SplitDetailSummary";

vi.mock("../sections", () => ({
  EditableDescription: ({
    description,
    onSave,
  }: {
    description?: string;
    isEditable: boolean;
    onSave: (val: string) => Promise<void>;
  }) => (
    <div data-testid="editable-description">
      <span>{description ?? "No description"}</span>
      <button data-testid="save-description" onClick={() => onSave("new desc")}>
        Save
      </button>
    </div>
  ),
  DesignPanel: ({ content }: { content: string }) => (
    <div data-testid="design-panel">{content}</div>
  ),
}));

vi.mock("@/api", () => ({
  updateIssue: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});

import { updateIssue } from "@/api";

const mockUpdateIssue = vi.mocked(updateIssue);

function createIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    title: "Test Issue",
    description: "A test issue",
    status: "open",
    priority: 2,
    issue_type: "task",
    assignee: "nova",
    created_at: "2026-01-20T10:00:00Z",
    updated_at: "2026-01-20T10:00:00Z",
    labels: [],
    dependencies: [],
    blocked_by: [],
    comments: [],
    ...overrides,
  } as Issue;
}

describe("SplitDetailSummary", () => {
  const defaultProps = {
    issue: createIssue(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("rendering", () => {
    it("renders description section", () => {
      render(<SplitDetailSummary {...defaultProps} />);
      expect(screen.getByText("Description")).toBeInTheDocument();
      expect(screen.getByTestId("editable-description")).toBeInTheDocument();
    });

    it("renders editable description with issue description", () => {
      const issue = createIssue({ description: "My description" });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("My description")).toBeInTheDocument();
    });

    it("renders design panel when design is present", () => {
      const issue = createIssue({ design: "# Design doc" });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByTestId("design-section")).toBeInTheDocument();
      expect(screen.getByTestId("design-panel")).toHaveTextContent(
        "# Design doc",
      );
    });

    it("uses full-width layout when design is absent", () => {
      const issue = createIssue({ design: undefined });
      const { container } = render(
        <SplitDetailSummary {...defaultProps} issue={issue} />,
      );
      const detailContent = container.firstChild as HTMLElement;
      const innerDiv = detailContent?.firstChild as HTMLElement;
      expect(innerDiv?.className || "").not.toContain("detailColumns");
    });
  });

  describe("description save flow", () => {
    it("calls updateIssue and onIssueUpdate when description is saved", async () => {
      const updatedIssue = createIssue({ description: "new desc" });
      mockUpdateIssue.mockResolvedValue(updatedIssue);
      const onIssueUpdate = vi.fn();

      render(
        <SplitDetailSummary {...defaultProps} onIssueUpdate={onIssueUpdate} />,
      );

      fireEvent.click(screen.getByTestId("save-description"));

      await waitFor(() => {
        expect(mockUpdateIssue).toHaveBeenCalledWith("test-ws-id", "issue-1", {
          description: "new desc",
        });
      });

      await waitFor(() => {
        expect(onIssueUpdate).toHaveBeenCalledWith(updatedIssue);
      });
    });

    it("works without onIssueUpdate callback", async () => {
      const updatedIssue = createIssue({ description: "new desc" });
      mockUpdateIssue.mockResolvedValue(updatedIssue);

      render(
        <SplitDetailSummary {...defaultProps} onIssueUpdate={undefined} />,
      );

      fireEvent.click(screen.getByTestId("save-description"));

      await waitFor(() => {
        expect(mockUpdateIssue).toHaveBeenCalledWith("test-ws-id", "issue-1", {
          description: "new desc",
        });
      });
    });
  });

  describe("edge cases", () => {
    it("renders with undefined description", () => {
      const issue = createIssue({ description: undefined });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("No description")).toBeInTheDocument();
    });
  });
});
