/**
 * @vitest-environment jsdom
 */

import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

const mocks = vi.hoisted(() => ({
  browserProps: [] as Array<{ mode?: string; agentName?: string }>,
}));

vi.mock("@/hooks", () => ({
  useRouteView: () => ({
    view: "skills",
    setView: vi.fn(),
    navigateToView: vi.fn(),
  }),
  useWorkspaceContext: () => ({
    workspaceId: "test-ws",
  }),
}));

vi.mock("@/components/FileExplorer", () => ({
  WorkspaceFileBrowser: (props: { mode?: string; agentName?: string }) => {
    mocks.browserProps.push(props);
    return (
      <div
        data-testid="workspace-file-browser"
        data-mode={props.mode}
        data-agent={props.agentName ?? ""}
      />
    );
  },
}));

vi.mock("@/components", () => ({
  ErrorBoundary: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="error-boundary">{children}</div>
  ),
  LoadingSkeleton: {
    FileExplorer: () => <div data-testid="loading-skeleton-file-explorer" />,
  },
}));

import { SkillsPage } from "../SkillsPage";

describe("SkillsPage", () => {
  beforeEach(() => {
    mocks.browserProps.length = 0;
  });

  it("renders without crashing", () => {
    const { container } = render(<SkillsPage />);
    expect(container).toBeTruthy();
  });

  it("renders WorkspaceFileBrowser inside ErrorBoundary after lazy load", async () => {
    render(<SkillsPage />);
    expect(screen.getByTestId("error-boundary")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByTestId("workspace-file-browser")).toBeInTheDocument();
    });
  });

  /**
   * The whole point of the section is that it reuses the Files browser rather
   * than reimplementing one. If this ever renders something other than
   * WorkspaceFileBrowser, the two surfaces have started to drift.
   */
  it("reuses the shared browser in skills mode, exactly once", async () => {
    render(<SkillsPage />);

    const browser = await screen.findByTestId("workspace-file-browser");
    expect(browser).toHaveAttribute("data-mode", "skills");
    expect(browser).toHaveAttribute("data-agent", "");
    expect(mocks.browserProps).toHaveLength(1);
  });
});
