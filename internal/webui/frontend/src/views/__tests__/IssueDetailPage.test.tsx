/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue, IssueDetails } from "@/types";

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

const baseProps = {
  issueDetails: null as Issue | IssueDetails | null,
  isLoading: false,
  error: null as string | null,
  previousView: "kanban" as const,
  selectedIssueId: "issue-1",
  onBack: vi.fn(),
  onApprove: vi.fn() as (issue: Issue) => Promise<void>,
  onReject: vi.fn() as (issue: Issue, comment: string) => Promise<void>,
  onOpenInTerminal: vi.fn() as (issue: Issue | IssueDetails) => void,
  onCopyLink: vi.fn(),
  onNavigateToIssue: vi.fn() as (issue: Issue) => void,
  onIssueUpdate: vi.fn() as (issue: Issue) => void,
};

describe("IssueDetailPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<IssueDetailPage {...baseProps} />);
    expect(container).toBeTruthy();
  });

  it("renders IssueDetailView inside ErrorBoundary", () => {
    render(<IssueDetailPage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    expect(screen.getByTestId("issue-detail-view")).toBeInTheDocument();
  });

  it("passes isLoading and error props to IssueDetailView", () => {
    render(
      <IssueDetailPage {...baseProps} isLoading={true} error="fetch failed" />,
    );
    const view = screen.getByTestId("issue-detail-view");
    expect(view.getAttribute("data-loading")).toBe("true");
    expect(view.getAttribute("data-error")).toBe("fetch failed");
  });

  it("renders with null selectedIssueId", () => {
    render(<IssueDetailPage {...baseProps} selectedIssueId={null} />);
    expect(screen.getByTestId("issue-detail-view")).toBeInTheDocument();
  });
});
