/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "table" as const };
const mockActions = { ...NO_WORKSPACE_VIEW_ACTIONS };
const { mockUpdateIssue, mockBulkClose } = vi.hoisted(() => ({
  mockUpdateIssue: vi.fn().mockResolvedValue({}),
  mockBulkClose: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/contexts/WorkspaceViewContext", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/contexts/WorkspaceViewContext")>();
  return {
    ...actual,
    useWorkspaceViewData: () => mockData,
    useWorkspaceViewActions: () => mockActions,
  };
});

// Mock child components to avoid deep rendering
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  IssueTable: (props: Record<string, unknown>) => (
    <div
      data-testid="issue-table"
      data-sortable={String(props.sortable)}
      data-group-by-epic={String(props.groupByEpic)}
    />
  ),
  BulkActionToolbar: (props: {
    selectedIds: Set<string>;
    actions: Array<{ id: string; label: string; onClick: (ids: Set<string>) => void }>;
  }) => (
    <div data-testid="bulk-action-toolbar">
      {props.actions.map((action) => (
        <button
          key={action.id}
          data-testid={`bulk-action-${action.id}`}
          onClick={() => action.onClick(props.selectedIds)}
        >
          {action.label}
        </button>
      ))}
    </div>
  ),
  ConfirmDialog: (props: {
    isOpen: boolean;
    message: React.ReactNode;
    onConfirm: () => void;
  }) =>
    props.isOpen ? (
      <div data-testid="confirm-dialog">
        {props.message}
        <button data-testid="confirm-dialog-confirm" onClick={props.onConfirm}>
          Confirm
        </button>
      </div>
    ) : null,
}));

vi.mock("@/components/IssueViewGuard", () => ({
  IssueViewGuard: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="issue-view-guard">{children}</div>
  ),
}));

vi.mock("@/hooks", () => ({
  useSelection: () => ({
    selectedIds: new Set<string>(["issue-1"]),
    toggleSelection: vi.fn(),
    deselectAll: vi.fn(),
  }),
  useWorkspaceContext: () => ({ workspaceId: "workspace-1" }),
  useBulkClose: () => ({ bulkClose: mockBulkClose, isLoading: false }),
  updateIssue: mockUpdateIssue,
}));

import { TablePage } from "../TablePage";

describe("TablePage", () => {
  it("renders without crashing", () => {
    const { container } = render(<TablePage />);
    expect(container).toBeTruthy();
  });

  it("renders IssueTable and BulkActionToolbar inside ErrorBoundary", () => {
    render(<TablePage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("issue-table")).toBeInTheDocument();
    expect(screen.getByTestId("bulk-action-toolbar")).toBeInTheDocument();
  });

  it("passes sortable prop to IssueTable", () => {
    render(<TablePage />);
    expect(
      screen.getByTestId("issue-table").getAttribute("data-sortable"),
    ).toBe("true");
  });

  it("groups the list view by epic", () => {
    render(<TablePage />);
    expect(
      screen.getByTestId("issue-table").getAttribute("data-group-by-epic"),
    ).toBe("true");
  });

  it("updates every selected issue through the bulk status dialog", async () => {
    render(<TablePage />);

    fireEvent.click(screen.getByTestId("bulk-action-status"));
    fireEvent.change(screen.getByLabelText("New status"), {
      target: { value: "blocked" },
    });
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

    await waitFor(() =>
      expect(mockUpdateIssue).toHaveBeenCalledWith("workspace-1", "issue-1", {
        status: "blocked",
      }),
    );
  });

  it("confirms before bulk-closing selected issues", async () => {
    render(<TablePage />);

    fireEvent.click(screen.getByTestId("bulk-action-close"));
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

    await waitFor(() =>
      expect(mockBulkClose).toHaveBeenCalledWith(new Set(["issue-1"])),
    );
  });
});
