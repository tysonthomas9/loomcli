/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "table" as const };
const mockActions = { ...NO_WORKSPACE_VIEW_ACTIONS };

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
  BulkActionToolbar: () => <div data-testid="bulk-action-toolbar" />,
}));

vi.mock("@/components/IssueViewGuard", () => ({
  IssueViewGuard: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="issue-view-guard">{children}</div>
  ),
}));

vi.mock("@/hooks", () => ({
  useSelection: () => ({
    selectedIds: new Set<string>(),
    toggleSelection: vi.fn(),
    deselectAll: vi.fn(),
  }),
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
});
