/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { Issue } from "@/types";
import type { ViewMode } from "@/components/ViewSwitcher";

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

const baseProps = {
  onViewChange: vi.fn() as (view: ViewMode) => void,
  onIssueClick: vi.fn() as (issue: Issue) => void,
  onAgentClick: vi.fn() as (agentName: string) => void,
  activeView: "monitor" as const,
};

describe("MonitorPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<MonitorPage {...baseProps} />);
    expect(container).toBeTruthy();
  });

  it("renders MonitorDashboard inside ErrorBoundary after lazy load", async () => {
    render(<MonitorPage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
    });
  });
});
