/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SplitDetailSummary component.
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type {
  Issue,
  Priority,
  IssueType,
  LoomAgentStatus,
  LoomTaskInfo,
} from "@/types";

import { SplitDetailSummary } from "../SplitDetailSummary";

// Mock child components
vi.mock("../EditableDescription", () => ({
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
}));

vi.mock("../DesignPanel", () => ({
  DesignPanel: ({ content }: { content: string }) => (
    <div data-testid="design-panel">{content}</div>
  ),
}));

vi.mock("../PriorityDropdown", () => ({
  PriorityDropdown: ({
    priority,
    isSaving,
  }: {
    priority: Priority;
    onSave: (p: Priority) => Promise<void>;
    isSaving: boolean;
  }) => (
    <div data-testid="priority-dropdown" data-saving={isSaving}>
      Priority: {priority}
    </div>
  ),
}));

vi.mock("../TypeDropdown", () => ({
  TypeDropdown: ({
    type,
    isSaving,
  }: {
    type: IssueType;
    onSave: (t: IssueType) => Promise<void>;
    isSaving: boolean;
  }) => (
    <div data-testid="type-dropdown" data-saving={isSaving}>
      Type: {type}
    </div>
  ),
}));

vi.mock("../AssigneeDropdown", () => ({
  AssigneeDropdown: ({
    assignee,
    isSaving,
  }: {
    assignee: string;
    onSave: (a: string) => Promise<void>;
    isSaving: boolean;
    agents: LoomAgentStatus[];
    agentTasks: Record<string, LoomTaskInfo>;
  }) => (
    <div data-testid="assignee-dropdown" data-saving={isSaving}>
      Assignee: {assignee}
    </div>
  ),
}));

// Mock updateIssue API
vi.mock("@/api", () => ({
  updateIssue: vi.fn(),
}));

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
    isSavingPriority: false,
    isSavingType: false,
    isSavingAssignee: false,
    agents: [] as LoomAgentStatus[],
    agentTasks: {} as Record<string, LoomTaskInfo>,
    onPrioritySave: vi.fn().mockResolvedValue(undefined),
    onTypeSave: vi.fn().mockResolvedValue(undefined),
    onAssigneeSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("rendering", () => {
    it("renders all dropdowns", () => {
      render(<SplitDetailSummary {...defaultProps} />);
      expect(screen.getByTestId("priority-dropdown")).toBeInTheDocument();
      expect(screen.getByTestId("type-dropdown")).toBeInTheDocument();
      expect(screen.getByTestId("assignee-dropdown")).toBeInTheDocument();
    });

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

    it("passes correct priority to PriorityDropdown", () => {
      const issue = createIssue({ priority: 1 });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("Priority: 1")).toBeInTheDocument();
    });

    it("passes correct type to TypeDropdown", () => {
      const issue = createIssue({ issue_type: "bug" });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("Type: bug")).toBeInTheDocument();
    });

    it("passes correct assignee to AssigneeDropdown", () => {
      const issue = createIssue({ assignee: "falcon" });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("Assignee: falcon")).toBeInTheDocument();
    });
  });

  describe("saving states", () => {
    it("passes isSavingPriority to PriorityDropdown", () => {
      render(<SplitDetailSummary {...defaultProps} isSavingPriority={true} />);
      expect(screen.getByTestId("priority-dropdown")).toHaveAttribute(
        "data-saving",
        "true",
      );
    });

    it("passes isSavingType to TypeDropdown", () => {
      render(<SplitDetailSummary {...defaultProps} isSavingType={true} />);
      expect(screen.getByTestId("type-dropdown")).toHaveAttribute(
        "data-saving",
        "true",
      );
    });

    it("passes isSavingAssignee to AssigneeDropdown", () => {
      render(<SplitDetailSummary {...defaultProps} isSavingAssignee={true} />);
      expect(screen.getByTestId("assignee-dropdown")).toHaveAttribute(
        "data-saving",
        "true",
      );
    });

    it("does not show saving state when not saving", () => {
      render(<SplitDetailSummary {...defaultProps} />);
      expect(screen.getByTestId("priority-dropdown")).toHaveAttribute(
        "data-saving",
        "false",
      );
      expect(screen.getByTestId("type-dropdown")).toHaveAttribute(
        "data-saving",
        "false",
      );
      expect(screen.getByTestId("assignee-dropdown")).toHaveAttribute(
        "data-saving",
        "false",
      );
    });
  });

  describe("design panel", () => {
    it("renders design panel when issue has design field", () => {
      const issue = createIssue({ design: "Some design content" });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByTestId("design-section")).toBeInTheDocument();
      expect(screen.getByTestId("design-panel")).toBeInTheDocument();
      expect(screen.getByText("Some design content")).toBeInTheDocument();
    });

    it("does not render design panel when design is absent", () => {
      const issue = createIssue({ design: undefined });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.queryByTestId("design-section")).not.toBeInTheDocument();
      expect(screen.queryByTestId("design-panel")).not.toBeInTheDocument();
    });

    it("uses two-column layout when design exists", () => {
      const issue = createIssue({ design: "Design content" });
      const { container } = render(
        <SplitDetailSummary {...defaultProps} issue={issue} />,
      );
      // The parent div should have detailColumns class
      const detailContent = container.firstChild as HTMLElement;
      const columnsDiv = detailContent?.firstChild as HTMLElement;
      expect(columnsDiv?.className).toContain("detailColumns");
    });

    it("uses full-width layout when design is absent", () => {
      const issue = createIssue({ design: undefined });
      const { container } = render(
        <SplitDetailSummary {...defaultProps} issue={issue} />,
      );
      const detailContent = container.firstChild as HTMLElement;
      const innerDiv = detailContent?.firstChild as HTMLElement;
      // Should not have detailColumns class
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
        expect(mockUpdateIssue).toHaveBeenCalledWith("issue-1", {
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
        expect(mockUpdateIssue).toHaveBeenCalledWith("issue-1", {
          description: "new desc",
        });
      });
    });
  });

  describe("edge cases", () => {
    it("renders with empty assignee", () => {
      const issue = createIssue({ assignee: "" });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("Assignee:")).toBeInTheDocument();
    });

    it("renders with undefined description", () => {
      const issue = createIssue({ description: undefined });
      render(<SplitDetailSummary {...defaultProps} issue={issue} />);
      expect(screen.getByText("No description")).toBeInTheDocument();
    });
  });
});
