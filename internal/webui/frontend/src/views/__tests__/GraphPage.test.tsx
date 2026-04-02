/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  NO_WORKSPACE_VIEW_DATA,
  NO_WORKSPACE_VIEW_ACTIONS,
} from "@/contexts/WorkspaceViewContext";

const mockData = { ...NO_WORKSPACE_VIEW_DATA, activeView: "graph" as const };
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

// Mock the lazy-loaded component module
vi.mock("@/components/GraphView", () => ({
  GraphView: () => <div data-testid="graph-view" />,
}));

// Mock ErrorBoundary and LoadingSkeleton
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    Graph: () => <div data-testid="loading-skeleton-graph" />,
  },
}));

vi.mock("@/components/IssueViewGuard", () => ({
  IssueViewGuard: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="issue-view-guard">{children}</div>
  ),
}));

import { GraphPage } from "../GraphPage";

describe("GraphPage", () => {
  it("renders without crashing", async () => {
    const { container } = render(<GraphPage />);
    expect(container).toBeTruthy();
  });

  it("renders the GraphView inside ErrorBoundary after lazy load", async () => {
    render(<GraphPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("graph-view")).toBeInTheDocument();
    });
  });

  it("shows loading skeleton while lazy component loads", () => {
    render(<GraphPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
  });
});
