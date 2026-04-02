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

const mockData = {
  ...NO_WORKSPACE_VIEW_DATA,
  activeView: "issue-detail" as const,
  selectedIssueId: "issue-1",
};
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

vi.mock("react-router-dom", () => ({
  useNavigate: () => vi.fn(),
}));

// Mock child components to avoid deep rendering
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  IssueDetailView: (props: Record<string, unknown>) => (
    <div
      data-testid="issue-detail-view"
      data-loading={String(props.isLoading)}
      data-error={props.error as string | null}
    />
  ),
}));

import { IssueDetailPage } from "../IssueDetailPage";

describe("IssueDetailPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<IssueDetailPage />);
    expect(container).toBeTruthy();
  });

  it("renders IssueDetailView inside ErrorBoundary", () => {
    render(<IssueDetailPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("issue-detail-view")).toBeInTheDocument();
  });

  it("passes isLoading and error props to IssueDetailView", () => {
    mockData.isLoadingDetails = true;
    mockData.detailError = "fetch failed";
    render(<IssueDetailPage />);
    const view = screen.getByTestId("issue-detail-view");
    expect(view.getAttribute("data-loading")).toBe("true");
    expect(view.getAttribute("data-error")).toBe("fetch failed");
    mockData.isLoadingDetails = false;
    mockData.detailError = null;
  });

  it("renders with null selectedIssueId", () => {
    mockData.selectedIssueId = null;
    render(<IssueDetailPage />);
    expect(screen.getByTestId("issue-detail-view")).toBeInTheDocument();
    mockData.selectedIssueId = "issue-1";
  });
});
