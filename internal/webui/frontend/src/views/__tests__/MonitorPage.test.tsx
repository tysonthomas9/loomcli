/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

vi.mock("@/contexts/WorkspaceViewContext", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/contexts/WorkspaceViewContext")>();
  return {
    ...actual,
    useWorkspaceViewData: () => ({
      ...actual.NO_WORKSPACE_VIEW_DATA,
      activeView: "monitor",
    }),
    useWorkspaceViewActions: () => actual.NO_WORKSPACE_VIEW_ACTIONS,
  };
});

// Mock the lazy-loaded component module
vi.mock("@/components/MonitorDashboard", () => ({
  MonitorDashboard: () => <div data-testid="monitor-dashboard" />,
}));

// Mock ErrorBoundary and LoadingSkeleton
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    Monitor: () => <div data-testid="loading-skeleton-monitor" />,
  },
}));

import { MonitorPage } from "../MonitorPage";

describe("MonitorPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<MonitorPage />);
    expect(container).toBeTruthy();
  });

  it("renders MonitorDashboard inside ErrorBoundary after lazy load", async () => {
    render(<MonitorPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
    });
  });
});
