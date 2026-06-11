/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { NewTerminalTabMenu } from "../NewTerminalTabMenu";

vi.mock("../NewTerminalTabMenu.module.css", () => ({
  default: new Proxy(
    {},
    {
      get: (_target, prop) => String(prop),
    },
  ),
}));

describe("NewTerminalTabMenu", () => {
  it("opens a backend menu when + is clicked", () => {
    render(
      <NewTerminalTabMenu
        availableBackends={["claude", "codex"]}
        onSelect={vi.fn()}
      />,
    );

    expect(screen.queryByTestId("new-terminal-tab-menu")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("terminal-new-tab-button"));
    expect(screen.getByTestId("new-terminal-tab-menu")).toBeInTheDocument();
    expect(screen.getByTestId("new-tab-backend-shell")).toBeInTheDocument();
    expect(screen.getByTestId("new-tab-backend-claude")).toBeInTheDocument();
    expect(screen.getByTestId("new-tab-backend-codex")).toBeInTheDocument();
  });

  it("selecting a backend calls onSelect and closes the menu", () => {
    const onSelect = vi.fn();
    render(
      <NewTerminalTabMenu
        availableBackends={["claude", "codex"]}
        onSelect={onSelect}
      />,
    );

    fireEvent.click(screen.getByTestId("terminal-new-tab-button"));
    fireEvent.click(screen.getByTestId("new-tab-backend-codex"));

    expect(onSelect).toHaveBeenCalledWith("codex");
    expect(screen.queryByTestId("new-terminal-tab-menu")).not.toBeInTheDocument();
  });

  it("calls onDisabledAttempt when disabled", () => {
    const onDisabledAttempt = vi.fn();
    render(
      <NewTerminalTabMenu
        availableBackends={["claude"]}
        disabled
        onSelect={vi.fn()}
        onDisabledAttempt={onDisabledAttempt}
      />,
    );

    fireEvent.click(screen.getByTestId("terminal-new-tab-button"));
    expect(onDisabledAttempt).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("new-terminal-tab-menu")).not.toBeInTheDocument();
  });

  it("lists Terminal (shell) first and omits provider company names", () => {
    render(
      <NewTerminalTabMenu
        availableBackends={["claude", "codex"]}
        onSelect={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId("terminal-new-tab-button"));

    const menu = screen.getByTestId("new-terminal-tab-menu");
    const items = Array.from(menu.querySelectorAll('[role="menuitem"]'));
    expect(items[0]).toHaveTextContent("Terminal");
    expect(items[1]).toHaveTextContent("Claude");
    expect(items[2]).toHaveTextContent("Codex");
    expect(menu.textContent).not.toMatch(/Anthropic|OpenAI|Google/);
  });
});
