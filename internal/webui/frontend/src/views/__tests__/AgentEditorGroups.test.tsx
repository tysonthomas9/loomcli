// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import { AgentEditorGroups } from "../AgentEditorGroups";

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
    expect(screen.getByRole("tab", { name: "Terminal" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Info" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Git" })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Logs" })).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Diff" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Files" })).toBeInTheDocument();
  });

  it("exposes the tab bar as a tablist with a selected tab and its panel", () => {
    render(
      <AgentEditorGroups
        resetKey="agent-a"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    expect(screen.getByRole("tablist")).toBeInTheDocument();
    const selected = screen.getByRole("tab", { selected: true });
    expect(selected).toHaveTextContent("Terminal");
    expect(screen.getAllByRole("tab")).toHaveLength(5);
    const panel = screen.getByRole("tabpanel", { name: "Terminal" });
    expect(panel).toHaveAttribute("id", selected.getAttribute("aria-controls"));
    expect(panel).toContainElement(screen.getByTestId("pane-terminal"));

    fireEvent.click(screen.getByRole("tab", { name: "Git" }));
    expect(screen.getByRole("tab", { selected: true })).toHaveTextContent(
      "Git",
    );
    expect(screen.getByRole("tabpanel", { name: "Git" })).toContainElement(
      screen.getByTestId("pane-git"),
    );
  });

  it("moves between tabs with the arrow, Home and End keys", () => {
    render(
      <AgentEditorGroups
        resetKey="agent-a"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    const terminal = screen.getByRole("tab", { name: "Terminal" });
    expect(terminal).toHaveAttribute("tabindex", "0");
    expect(screen.getByRole("tab", { name: "Info" })).toHaveAttribute(
      "tabindex",
      "-1",
    );

    fireEvent.keyDown(terminal, { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "Info" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("tab", { name: "Info" })).toHaveFocus();

    fireEvent.keyDown(screen.getByRole("tab", { name: "Info" }), {
      key: "End",
    });
    expect(screen.getByRole("tab", { name: "Files" })).toHaveFocus();
    expect(screen.getByRole("tab", { selected: true })).toHaveTextContent(
      "Files",
    );

    fireEvent.keyDown(screen.getByRole("tab", { name: "Files" }), {
      key: "ArrowRight",
    });
    expect(screen.getByRole("tab", { name: "Terminal" })).toHaveFocus();

    fireEvent.keyDown(screen.getByRole("tab", { name: "Terminal" }), {
      key: "ArrowLeft",
    });
    expect(screen.getByRole("tab", { name: "Files" })).toHaveFocus();

    fireEvent.keyDown(screen.getByRole("tab", { name: "Files" }), {
      key: "Home",
    });
    expect(screen.getByRole("tab", { selected: true })).toHaveTextContent(
      "Terminal",
    );
  });

  it("moves the active tab into a right editor group when split is clicked", () => {
    render(
      <AgentEditorGroups
        resetKey="agent-a"
        renderPane={(tab) => <div data-testid={`pane-${tab}`}>{tab}</div>}
      />,
    );

    fireEvent.click(screen.getByRole("tab", { name: "Info" }));
    fireEvent.click(screen.getByTestId("agent-editor-split"));

    expect(screen.getByTestId("agent-editor-groups")).toHaveAttribute(
      "data-split",
      "true",
    );
    expect(screen.getAllByRole("tab", { name: "Info" })).toHaveLength(1);
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

    fireEvent.click(screen.getByRole("tab", { name: "Git" }));
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
