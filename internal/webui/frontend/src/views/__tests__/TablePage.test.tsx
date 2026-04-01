/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

// Mock child components to avoid deep rendering
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  IssueTable: (props: Record<string, unknown>) => (
    <div data-testid="issue-table" data-sortable={String(props.sortable)} />
  ),
  BulkActionToolbar: () => <div data-testid="bulk-action-toolbar" />,
}));

import { TablePage } from "../TablePage";

const baseProps = {
  filteredIssues: [] as Issue[],
  selectedIds: new Set<string>(),
  onSelectionChange: vi.fn(),
  onClearSelection: vi.fn(),
  onIssueClick: vi.fn() as (issue: Issue) => void,
  searchTerm: "",
  activeView: "table" as const,
};

describe("TablePage", () => {
  it("renders without crashing", () => {
    const { container } = render(<TablePage {...baseProps} />);
    expect(container).toBeTruthy();
  });

  it("renders IssueTable and BulkActionToolbar inside ErrorBoundary", () => {
    render(<TablePage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("issue-table")).toBeInTheDocument();
    expect(screen.getByTestId("bulk-action-toolbar")).toBeInTheDocument();
  });

  it("passes sortable prop to IssueTable", () => {
    render(<TablePage {...baseProps} />);
    expect(
      screen.getByTestId("issue-table").getAttribute("data-sortable"),
    ).toBe("true");
  });
});
