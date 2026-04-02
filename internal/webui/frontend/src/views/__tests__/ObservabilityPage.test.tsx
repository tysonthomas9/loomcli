/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

vi.mock("@/hooks", () => ({
  useRouteView: () => ({
    view: "observability",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  }),
}));

// Mock the lazy-loaded component module
vi.mock("@/components/ObservabilityDashboard", () => ({
  ObservabilityDashboard: () => <div data-testid="observability-dashboard" />,
}));

// Mock ErrorBoundary and LoadingSkeleton
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    Observability: () => <div data-testid="loading-skeleton-observability" />,
  },
}));

import { ObservabilityPage } from "../ObservabilityPage";

describe("ObservabilityPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<ObservabilityPage />);
    expect(container).toBeTruthy();
  });

  it("renders ObservabilityDashboard inside ErrorBoundary after lazy load", async () => {
    render(<ObservabilityPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("observability-dashboard")).toBeInTheDocument();
    });
  });
});
