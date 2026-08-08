/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

vi.mock("../components/ObservabilityDashboard", () => ({
  ObservabilityDashboard: () => <div data-testid="observability-dashboard" />,
}));

vi.mock("@/components/ErrorBoundary", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
}));

import { ObservabilityPage } from "../ObservabilityPage";

describe("ObservabilityPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<ObservabilityPage />);
    expect(container).toBeTruthy();
  });

  it("renders ObservabilityDashboard inside ErrorBoundary", async () => {
    render(<ObservabilityPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("observability-dashboard")).toBeInTheDocument();
    });
  });
});
