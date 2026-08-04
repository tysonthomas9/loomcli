/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { WorkspaceView } from "../WorkspaceView";

describe("WorkspaceView", () => {
  it("renders with data-testid", () => {
    render(<WorkspaceView />);
    expect(screen.getByTestId("workspace-view")).toBeInTheDocument();
  });

  it("renders heading 'Workspace'", () => {
    render(<WorkspaceView />);
    expect(
      screen.getByRole("heading", { name: "Workspace" }),
    ).toBeInTheDocument();
  });

  it("renders coming soon message", () => {
    render(<WorkspaceView />);
    expect(
      screen.getByText("Multi-repo workspace view coming soon."),
    ).toBeInTheDocument();
  });

  it("applies custom className", () => {
    render(<WorkspaceView className="my-custom-class" />);
    const view = screen.getByTestId("workspace-view");
    expect(view.className).toContain("my-custom-class");
  });

  it("renders without className prop", () => {
    render(<WorkspaceView />);
    const view = screen.getByTestId("workspace-view");
    expect(view).toBeInTheDocument();
    // Should not have 'undefined' in class list
    expect(view.className).not.toContain("undefined");
  });
});
