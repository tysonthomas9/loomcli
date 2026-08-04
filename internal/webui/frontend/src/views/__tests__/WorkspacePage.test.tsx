/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

let mockIsMultiRepo = true;

vi.mock("@/hooks", () => ({
  useRouteView: () => ({
    view: "workspace",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  }),
  useWorkspaceContext: () => ({
    isMultiRepo: mockIsMultiRepo,
    workspaceId: "ws-1",
    workspace: null,
    activeWorkspaceName: "test",
    setActiveWorkspace: vi.fn(),
    repos: [],
    selectedRepoNames: new Set<string>(),
    selectAll: vi.fn(),
    selectRepos: vi.fn(),
    sourceReposFilter: undefined,
  }),
}));

// Mock the lazy-loaded component module
vi.mock("@/components/WorkspaceView", () => ({
  WorkspaceView: () => <div data-testid="workspace-view" />,
}));

// Mock ErrorBoundary and LoadingSkeleton
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    Column: () => <div data-testid="loading-skeleton-column" />,
  },
}));

import { WorkspacePage } from "../WorkspacePage";

describe("WorkspacePage", () => {
  it("renders without crashing when isMultiRepo is true", () => {
    mockIsMultiRepo = true;
    const { container } = render(<WorkspacePage />);
    expect(container).toBeTruthy();
  });

  it("returns null when isMultiRepo is false", () => {
    mockIsMultiRepo = false;
    const { container } = render(<WorkspacePage />);
    expect(container.innerHTML).toBe("");
    mockIsMultiRepo = true;
  });

  it("renders WorkspaceView inside ErrorBoundary when isMultiRepo is true", async () => {
    mockIsMultiRepo = true;
    render(<WorkspacePage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("workspace-view")).toBeInTheDocument();
    });
  });

  it("does not render ErrorBoundary when isMultiRepo is false", () => {
    mockIsMultiRepo = false;
    render(<WorkspacePage />);
    expect(screen.queryByTestId("error-boundary")).not.toBeInTheDocument();
    mockIsMultiRepo = true;
  });
});
