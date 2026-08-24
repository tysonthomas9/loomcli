/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

vi.mock("@/hooks", () => ({
  useRouteView: () => ({
    view: "workflows",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  }),
}));

vi.mock("@/components/WorkflowsDashboard", () => ({
  WorkflowsDashboard: () => <div data-testid="workflows-dashboard" />,
}));

vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    Observability: () => <div data-testid="loading-skeleton" />,
  },
}));

import { WorkflowsPage } from "../WorkflowsPage";

describe("WorkflowsPage", () => {
  it("renders the dashboard inside an ErrorBoundary after lazy load", async () => {
    render(<WorkflowsPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("workflows-dashboard")).toBeInTheDocument();
    });
  });
});
