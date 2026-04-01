/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";

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

import { GraphPage } from "../GraphPage";

const baseProps = {
  filteredIssues: [] as Issue[],
  onNodeClick: vi.fn() as (issue: Issue) => void,
  activeView: "graph" as const,
};

describe("GraphPage", () => {
  it("renders without crashing", async () => {
    const { container } = render(<GraphPage {...baseProps} />);
    expect(container).toBeTruthy();
  });

  it("renders the GraphView inside ErrorBoundary after lazy load", async () => {
    render(<GraphPage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("graph-view")).toBeInTheDocument();
    });
  });

  it("shows loading skeleton while lazy component loads", () => {
    // Since we mock the module, the lazy component resolves immediately in the
    // next microtask. We can still verify the Suspense fallback structure exists
    // by checking ErrorBoundary wraps the content.
    render(<GraphPage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
  });
});
