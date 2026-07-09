/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "table" as const };
const mockActions = { ...NO_WORKSPACE_VIEW_ACTIONS };
const { selectionState, mockCloseIssue, mockUpdateIssue, mockDeselectAll } =
  vi.hoisted(() => ({
    selectionState: { selectedIds: new Set<string>() },
    mockCloseIssue: vi.fn(),
    mockUpdateIssue: vi.fn(),
    mockDeselectAll: vi.fn(),
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

vi.mock("@/api", () => ({
  closeIssue: mockCloseIssue,
  updateIssue: mockUpdateIssue,
}));

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
  BulkActionToolbar: ({
    selectedIds,
    onClearSelection,
    actions = [],
  }: {
    selectedIds: Set<string>;
    onClearSelection: () => void;
    actions?: Array<{
      id: string;
      label: string;
      disabled?: boolean;
      loading?: boolean;
      onClick: (selectedIds: Set<string>) => void;
    }>;
  }) =>
    selectedIds.size === 0 ? null : (
      <div data-testid="bulk-action-toolbar">
        {actions.map((action) => (
          <button
            key={action.id}
            type="button"
            data-testid={`bulk-action-${action.id}`}
            disabled={action.disabled || action.loading}
            onClick={() => action.onClick(selectedIds)}
          >
            {action.label}
          </button>
        ))}
        <button type="button" onClick={onClearSelection}>
          Deselect all
        </button>
      </div>
    ),
  ConfirmDialog: ({
    isOpen,
    title,
    message,
    confirmLabel = "Confirm",
    onConfirm,
    onCancel,
  }: {
    isOpen: boolean;
    title: string;
    message: React.ReactNode;
    confirmLabel?: string;
    onConfirm: () => void;
    onCancel: () => void;
  }) =>
    isOpen ? (
      <div role="alertdialog" aria-label={title}>
        <div>{message}</div>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="button"
          data-testid="confirm-dialog-confirm"
          onClick={onConfirm}
        >
          {confirmLabel}
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
    selectedIds: selectionState.selectedIds,
    toggleSelection: vi.fn(),
    deselectAll: mockDeselectAll,
  }),
  useBulkClose: (options: {
    closeReason?: string;
    onSuccess?: (closedIds: string[]) => void;
    onPartialSuccess?: (closedIds: string[], failedIds: string[]) => void;
    onError?: (error: Error, failedIds: string[]) => void;
  }) => ({
    bulkClose: async (issueIds: Set<string> | string[]) => {
      const ids = Array.isArray(issueIds) ? issueIds : Array.from(issueIds);
      const results = await Promise.allSettled(
        ids.map((id) => mockCloseIssue("test-ws-id", id, options.closeReason)),
      );
      const closedIds = ids.filter(
        (_id, index) => results[index]?.status === "fulfilled",
      );
      const failedIds = ids.filter(
        (_id, index) => results[index]?.status === "rejected",
      );
      if (failedIds.length === 0) {
        options.onSuccess?.(closedIds);
      } else if (closedIds.length > 0) {
        options.onPartialSuccess?.(closedIds, failedIds);
      } else {
        options.onError?.(new Error("Failed to close issues"), failedIds);
      }
    },
    isLoading: false,
  }),
}));

import { TablePage } from "../TablePage";

describe("TablePage", () => {
  beforeEach(() => {
    selectionState.selectedIds = new Set<string>();
    mockCloseIssue.mockReset();
    mockCloseIssue.mockResolvedValue(undefined);
    mockUpdateIssue.mockReset();
    mockUpdateIssue.mockResolvedValue(undefined);
    mockDeselectAll.mockReset();
    mockData.workspaceId = "test-ws-id";
    mockActions.showToast = vi.fn();
  });

  it("renders without crashing", () => {
    const { container } = render(<TablePage />);
    expect(container).toBeTruthy();
  });

  it("renders IssueTable inside ErrorBoundary", () => {
    render(<TablePage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("issue-table")).toBeInTheDocument();
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

  it("shows bulk actions when rows are selected", () => {
    selectionState.selectedIds = new Set(["ISS-1", "ISS-2"]);

    render(<TablePage />);

    expect(screen.getByTestId("bulk-action-toolbar")).toBeInTheDocument();
    expect(screen.getByTestId("bulk-action-close")).toHaveTextContent("Close");
    expect(screen.getByTestId("bulk-action-status")).toHaveTextContent(
      "Change status",
    );
    expect(screen.getByTestId("bulk-action-priority")).toHaveTextContent(
      "Change priority",
    );
    expect(screen.getByTestId("bulk-action-assign")).toHaveTextContent(
      "Assign",
    );
  });

  it("confirms bulk close and calls the API for each selected id", async () => {
    selectionState.selectedIds = new Set(["ISS-1", "ISS-2", "ISS-3"]);

    render(<TablePage />);

    fireEvent.click(screen.getByTestId("bulk-action-close"));

    expect(
      screen.getByRole("alertdialog", { name: "Close 3 issues?" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

    await waitFor(() => {
      expect(mockCloseIssue).toHaveBeenCalledTimes(3);
    });
    expect(mockCloseIssue).toHaveBeenCalledWith(
      "test-ws-id",
      "ISS-1",
      "Closed from table bulk action",
    );
    expect(mockCloseIssue).toHaveBeenCalledWith(
      "test-ws-id",
      "ISS-2",
      "Closed from table bulk action",
    );
    expect(mockCloseIssue).toHaveBeenCalledWith(
      "test-ws-id",
      "ISS-3",
      "Closed from table bulk action",
    );
    expect(mockDeselectAll).toHaveBeenCalledTimes(1);
  });
});
