/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

vi.mock("@/hooks", () => ({
  useRouteView: () => ({
    view: "settings",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  }),
}));

// Mock the lazy-loaded component module
vi.mock("@/components/SettingsView", () => ({
  SettingsView: () => <div data-testid="settings-view" />,
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

import { SettingsPage } from "../SettingsPage";

describe("SettingsPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<SettingsPage />);
    expect(container).toBeTruthy();
  });

  it("renders SettingsView inside ErrorBoundary after lazy load", async () => {
    render(<SettingsPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("settings-view")).toBeInTheDocument();
    });
  });
});
