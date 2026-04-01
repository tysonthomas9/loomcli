/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

// Mock the lazy-loaded component module
vi.mock("@/components/FileExplorer", () => ({
  FileExplorer: () => <div data-testid="file-explorer" />,
}));

// Mock ErrorBoundary and LoadingSkeleton
vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    FileExplorer: () => <div data-testid="loading-skeleton-file-explorer" />,
  },
}));

import { FilesPage } from "../FilesPage";

describe("FilesPage", () => {
  it("renders without crashing", () => {
    const { container } = render(<FilesPage activeView="files" />);
    expect(container).toBeTruthy();
  });

  it("renders FileExplorer inside ErrorBoundary after lazy load", async () => {
    render(<FilesPage activeView="files" />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("file-explorer")).toBeInTheDocument();
    });
  });
});
