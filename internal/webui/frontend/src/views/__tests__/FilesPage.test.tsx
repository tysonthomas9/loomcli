/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

const mocks = vi.hoisted(() => ({
  browserProps: [] as Array<{ scopeRef?: { scope: string; target?: string } }>,
}));

vi.mock("@/hooks", () => ({
  useRouteView: () => ({
    view: "files",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  }),
  useWorkspaceContext: () => ({
    workspaceId: "test-ws",
    repos: [
      { name: "loomcli", is_linked_worktree: false },
      { name: "atlas-worktree", is_linked_worktree: true },
    ],
    agents: [{ name: "atlas" }],
  }),
}));

// Mock the lazy-loaded component module
vi.mock("@/components/FileExplorer", () => ({
  WorkspaceFileBrowser: (props: {
    scopeRef?: { scope: string; target?: string };
  }) => {
    mocks.browserProps.push(props);
    return (
      <div
        data-testid="workspace-file-browser"
        data-scope={props.scopeRef?.scope}
        data-target={props.scopeRef?.target ?? ""}
      />
    );
  },
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
  beforeEach(() => {
    mocks.browserProps.length = 0;
  });

  it("renders without crashing", () => {
    const { container } = render(<FilesPage />);
    expect(container).toBeTruthy();
  });

  it("renders WorkspaceFileBrowser inside ErrorBoundary after lazy load", async () => {
    render(<FilesPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("workspace-file-browser")).toBeInTheDocument();
    });
  });

  it("switches browser scope from workspace to repo and agent", async () => {
    render(<FilesPage />);

    const browser = await screen.findByTestId("workspace-file-browser");
    expect(browser).toHaveAttribute("data-scope", "workspace");

    fireEvent.change(screen.getByLabelText("File browser scope"), {
      target: { value: "repo:loomcli" },
    });

    await waitFor(() => {
      expect(screen.getByTestId("workspace-file-browser")).toHaveAttribute(
        "data-scope",
        "repo",
      );
      expect(screen.getByTestId("workspace-file-browser")).toHaveAttribute(
        "data-target",
        "loomcli",
      );
    });

    fireEvent.change(screen.getByLabelText("File browser scope"), {
      target: { value: "agent:atlas" },
    });

    await waitFor(() => {
      expect(screen.getByTestId("workspace-file-browser")).toHaveAttribute(
        "data-scope",
        "agent",
      );
      expect(screen.getByTestId("workspace-file-browser")).toHaveAttribute(
        "data-target",
        "atlas",
      );
    });
  });
});
