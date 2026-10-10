// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import { AgentEditorGroups, agentTabFromParam } from "../AgentEditorGroups";

describe("AgentEditorGroups", () => {
  it("renders the supported agent tabs without Logs", () => {
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
    expect(screen.getByRole("button", { name: "Info" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Changes" })).toBeInTheDocument();
    for (const gone of ["Logs", "Git", "Diff"]) {
      expect(
        screen.queryByRole("button", { name: gone }),
      ).not.toBeInTheDocument();
    }
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

    fireEvent.click(screen.getByRole("button", { name: "Changes" }));
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

  // D43: old links to the Git or Diff tab land on Changes.
  it("maps ?tab=git and ?tab=diff deep links to Changes", () => {
    expect(agentTabFromParam("git")).toBe("changes");
    expect(agentTabFromParam("diff")).toBe("changes");
    expect(agentTabFromParam("changes")).toBe("changes");
    expect(agentTabFromParam("files")).toBe("files");
    expect(agentTabFromParam("bogus")).toBeUndefined();
    expect(agentTabFromParam(null)).toBeUndefined();
  });
});
