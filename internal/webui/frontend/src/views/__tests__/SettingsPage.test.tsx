/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import type { ViewMode } from "@/components/ViewSwitcher";

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

const baseProps = {
  onNavigate: vi.fn() as (view: ViewMode) => void,
  activeView: "settings" as const,
};

describe("SettingsPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<SettingsPage {...baseProps} />);
    expect(container).toBeTruthy();
  });

  it("renders SettingsView inside ErrorBoundary after lazy load", async () => {
    render(<SettingsPage {...baseProps} />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("settings-view")).toBeInTheDocument();
    });
  });
});
