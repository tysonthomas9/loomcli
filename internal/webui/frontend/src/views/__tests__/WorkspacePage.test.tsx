/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

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
    const { container } = render(
      <WorkspacePage isMultiRepo={true} activeView="workspace" />,
    );
    expect(container).toBeTruthy();
  });

  it("returns null when isMultiRepo is false", () => {
    const { container } = render(
      <WorkspacePage isMultiRepo={false} activeView="workspace" />,
    );
    expect(container.innerHTML).toBe("");
  });

  it("renders WorkspaceView inside ErrorBoundary when isMultiRepo is true", async () => {
    render(<WorkspacePage isMultiRepo={true} activeView="workspace" />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("workspace-view")).toBeInTheDocument();
    });
  });

  it("does not render ErrorBoundary when isMultiRepo is false", () => {
    render(<WorkspacePage isMultiRepo={false} activeView="workspace" />);
    expect(screen.queryByTestId("error-boundary")).not.toBeInTheDocument();
  });
});
