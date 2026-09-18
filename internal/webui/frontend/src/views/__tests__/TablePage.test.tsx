/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";

const {
  mockUpdateIssue,
  mockBulkClose,
  mockBulkCloseOptions,
  mockSelectedIds,
  mockRefetch,
  mockClearSelection,
} = vi.hoisted(() => ({
  mockUpdateIssue: vi.fn(),
  mockBulkClose: vi.fn(),
  mockBulkCloseOptions: {
    current: undefined as
      | {
          onSuccess?: (closedIds: string[]) => void;
          onPartialSuccess?: (closedIds: string[], failedIds: string[]) => void;
          onError?: (error: Error, failedIds: string[]) => void;
        }
      | undefined,
  },
  mockSelectedIds: { current: new Set<string>(["issue-1"]) },
  mockRefetch: vi.fn(),
  mockClearSelection: vi.fn(),
}));

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "table" as const };
const mockActions = {
  ...NO_WORKSPACE_VIEW_ACTIONS,
  refetch: mockRefetch,
};

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
    actions: Array<{
      id: string;
      label: string;
      onClick: (ids: Set<string>) => void;
    }>;
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
  ErrorToast: (props: { message: string; onDismiss: () => void }) => (
    <div role="alert">
      {props.message}
      <button onClick={props.onDismiss}>Dismiss</button>
    </div>
  ),
}));

vi.mock("@/components/IssueViewGuard", () => ({
  IssueViewGuard: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="issue-view-guard">{children}</div>
  ),
}));

vi.mock("@/hooks", () => ({
  useSelection: () => ({
    selectedIds: mockSelectedIds.current,
    toggleSelection: vi.fn(),
    deselectAll: mockClearSelection,
  }),
  useWorkspaceContext: () => ({ workspaceId: "workspace-1" }),
  useBulkClose: (options: typeof mockBulkCloseOptions.current) => {
    mockBulkCloseOptions.current = options;
    return {
      bulkClose: mockBulkClose,
      isLoading: false,
      error: null,
      failedIds: new Set<string>(),
      successCount: 0,
      createBulkAction: vi.fn(),
      reset: vi.fn(),
    };
  },
  updateIssue: mockUpdateIssue,
}));

import { TablePage } from "../TablePage";

describe("TablePage", () => {
  beforeEach(() => {
    mockSelectedIds.current = new Set(["issue-1"]);
    mockUpdateIssue.mockReset().mockResolvedValue({});
    mockBulkClose.mockReset().mockResolvedValue(undefined);
    mockRefetch.mockReset().mockResolvedValue(undefined);
    mockClearSelection.mockReset();
    mockBulkCloseOptions.current = undefined;
  });

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

  it("keeps the status dialog open and reports partial update failures", async () => {
    mockSelectedIds.current = new Set(["issue-1", "issue-2"]);
    mockUpdateIssue.mockImplementation(
      async (_workspaceId: string, issueId: string) => {
        if (issueId === "issue-2") throw new Error("status update rejected");
        return {};
      },
    );
    render(<TablePage />);

    fireEvent.click(screen.getByTestId("bulk-action-status"));
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

    expect(
      await screen.findByText("Updated 1 of 2 issues; 1 failed"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("confirm-dialog")).toBeInTheDocument();
    expect(mockRefetch).toHaveBeenCalledTimes(1);
    expect(mockClearSelection).not.toHaveBeenCalled();
  });

  it("keeps the close dialog open and reports total close failure", async () => {
    mockBulkClose.mockImplementationOnce(async () => {
      mockBulkCloseOptions.current?.onError?.(new Error("close rejected"), [
        "issue-1",
      ]);
    });
    render(<TablePage />);

    fireEvent.click(screen.getByTestId("bulk-action-close"));
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

    expect(await screen.findByText("close rejected")).toBeInTheDocument();
    expect(screen.getByTestId("confirm-dialog")).toBeInTheDocument();
    expect(mockClearSelection).not.toHaveBeenCalled();
  });

  it("keeps the close dialog open and reports partial close failure", async () => {
    mockSelectedIds.current = new Set(["issue-1", "issue-2"]);
    mockBulkClose.mockImplementationOnce(async () => {
      mockBulkCloseOptions.current?.onPartialSuccess?.(
        ["issue-1"],
        ["issue-2"],
      );
    });
    render(<TablePage />);

    fireEvent.click(screen.getByTestId("bulk-action-close"));
    fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));

    expect(
      await screen.findByText("Closed 1 of 2 issues; 1 failed"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("confirm-dialog")).toBeInTheDocument();
    expect(mockRefetch).toHaveBeenCalledTimes(1);
    expect(mockClearSelection).not.toHaveBeenCalled();
  });
});
