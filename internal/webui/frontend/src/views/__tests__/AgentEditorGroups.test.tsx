// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import { AgentEditorGroups } from "../AgentEditorGroups";

describe("AgentEditorGroups", () => {
  it("renders all agent tabs in a single group by default", () => {
    render(
      <AgentEditorGroups
        resetKey="agent-a"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    expect(screen.getByTestId("agent-editor-groups")).not.toHaveAttribute(
      "data-split",
      "true",
    );
    expect(
      screen.getByRole("button", { name: "Terminal" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Logs" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Files" })).toBeInTheDocument();
  });

  it("moves the active tab into a right editor group when split is clicked", () => {
    render(
      <AgentEditorGroups
        resetKey="agent-a"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Info" }));
    fireEvent.click(screen.getByTestId("agent-editor-split"));

    expect(screen.getByTestId("agent-editor-groups")).toHaveAttribute(
      "data-split",
      "true",
    );
    expect(screen.getAllByRole("button", { name: "Info" })).toHaveLength(1);
    expect(screen.getByTestId("pane-info")).toBeInTheDocument();
    expect(screen.getByTestId("pane-terminal")).toBeInTheDocument();
  });

  it("resets to a single group when the agent changes", () => {
    const { rerender } = render(
      <AgentEditorGroups
        resetKey="agent-a"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Git" }));
    fireEvent.click(screen.getByTestId("agent-editor-split"));
    expect(screen.getByTestId("agent-editor-groups")).toHaveAttribute(
      "data-split",
      "true",
    );

    rerender(
      <AgentEditorGroups
        resetKey="agent-b"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    expect(screen.getByTestId("agent-editor-groups")).not.toHaveAttribute(
      "data-split",
      "true",
    );
  });
});
