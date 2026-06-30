/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";
import type { Issue } from "@/types";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "list" as const };
const handleIssueClick = vi.fn();
const runEpic = vi.fn();
const mockActions = {
  ...NO_WORKSPACE_VIEW_ACTIONS,
  handleIssueClick,
  showToast: vi.fn(),
};

vi.mock("@/hooks/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/workspace")>();
  return {
    ...actual,
    useRunEpicWorkflow: () => ({
      runEpic,
      isRunningEpic: () => false,
    }),
  };
});

vi.mock("@/contexts/WorkspaceViewContext", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/contexts/WorkspaceViewContext")>();
  return {
    ...actual,
    useWorkspaceViewData: () => mockData,
    useWorkspaceViewActions: () => mockActions,
  };
});

vi.mock("zustand", () => ({
  useStore: (
    _store: unknown,
    selector: (s: { agents: unknown[] }) => unknown,
  ) => selector({ agents: [] }),
}));

vi.mock("@/hooks/common", () => ({
  useAgentStoreInstance: () => ({}),
}));

function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: `issue-${Math.random().toString(36).slice(2, 9)}`,
    title: "Test Issue",
    priority: 2,
    status: "open",
    issue_type: "task",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

import { ListPage } from "../ListPage";

describe("ListPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockData.filteredIssues = [];
  });

  it("shows epic id alongside epic title in lane header", () => {
    mockData.filteredIssues = [
      createMockIssue({
        id: "HELLO-WORLD-2",
        title: "Build the Hello World web app",
        issue_type: "epic",
        status: "open",
      }),
      createMockIssue({
        id: "task-1",
        title: "Child Task",
        issue_type: "task",
        parent: "HELLO-WORLD-2",
        parent_title: "Build the Hello World web app",
        status: "open",
      }),
    ];

    render(<ListPage />);

    expect(screen.getByText("HELLO-WORLD-2")).toBeInTheDocument();
    expect(
      screen.getByText("Build the Hello World web app"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "Open epic HELLO-WORLD-2: Build the Hello World web app",
      }),
    ).toBeInTheDocument();
  });

  it("opens epic when lane title is clicked", () => {
    const epic = createMockIssue({
      id: "HELLO-WORLD-2",
      title: "Build the Hello World web app",
      issue_type: "epic",
      status: "open",
    });
    mockData.filteredIssues = [
      epic,
      createMockIssue({
        id: "task-1",
        title: "Child Task",
        issue_type: "task",
        parent: "HELLO-WORLD-2",
        parent_title: "Build the Hello World web app",
        status: "open",
      }),
    ];

    render(<ListPage />);

    fireEvent.click(
      screen.getByRole("button", {
        name: "Open epic HELLO-WORLD-2: Build the Hello World web app",
      }),
    );

    expect(handleIssueClick).toHaveBeenCalledWith(
      expect.objectContaining({ id: "HELLO-WORLD-2" }),
    );
  });

  it("shows a run button for unclaimed epic lanes", () => {
    mockData.filteredIssues = [
      createMockIssue({
        id: "HELLO-WORLD-2",
        title: "Build the Hello World web app",
        issue_type: "epic",
        status: "open",
      }),
      createMockIssue({
        id: "task-1",
        title: "Child Task",
        issue_type: "task",
        parent: "HELLO-WORLD-2",
        parent_title: "Build the Hello World web app",
        status: "open",
      }),
    ];

    render(<ListPage />);

    const runButton = screen.getByTestId("lane-run-epic-button");
    expect(runButton).toHaveTextContent("Run");
    fireEvent.click(runButton);
    expect(runEpic).toHaveBeenCalledWith(
      expect.objectContaining({ id: "HELLO-WORLD-2" }),
    );
  });
});
