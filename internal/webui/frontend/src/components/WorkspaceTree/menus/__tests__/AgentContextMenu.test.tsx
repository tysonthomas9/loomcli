/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { AgentContextMenu } from "../AgentContextMenu";

vi.mock("../WorkspaceContextMenu.module.css", () => ({
  default: {
    menu: "menu",
    menuItem: "menuItem",
    menuItemIcon: "menuItemIcon",
    dangerItem: "dangerItem",
  },
}));

describe("AgentContextMenu", () => {
  let onArchive: ReturnType<typeof vi.fn>;
  let onClose: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onArchive = vi.fn();
    onClose = vi.fn();
  });

  const defaultPosition = { x: 100, y: 200 };

  const renderMenu = (isOpen = true) =>
    render(
      <AgentContextMenu
        isOpen={isOpen}
        position={defaultPosition}
        onArchive={onArchive}
        onClose={onClose}
      />,
    );

  it("renders null when isOpen is false", () => {
    const { container } = renderMenu(false);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(container.innerHTML).toBe("");
  });

  it("renders Archive when isOpen is true", () => {
    renderMenu(true);
    expect(screen.getByTestId("agent-context-menu")).toBeInTheDocument();
    expect(screen.getByText("Archive")).toBeInTheDocument();
    expect(screen.getAllByRole("menuitem")).toHaveLength(1);
  });

  it("positions the menu at the given coordinates", () => {
    renderMenu(true);
    const menu = screen.getByTestId("agent-context-menu");
    expect(menu.style.left).toBe("100px");
    expect(menu.style.top).toBe("200px");
  });

  it("fires onArchive and onClose when Archive is clicked", () => {
    renderMenu(true);
    fireEvent.click(screen.getByTestId("agent-context-menu-archive"));
    expect(onArchive).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("fires onArchive and onClose when Enter is pressed on Archive", () => {
    renderMenu(true);
    const button = screen.getByTestId("agent-context-menu-archive");
    fireEvent.keyDown(button, { key: "Enter" });
    expect(onArchive).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("applies dangerItem styling to Archive button", () => {
    renderMenu(true);
    const archiveButton = screen.getByTestId("agent-context-menu-archive");
    expect(archiveButton.className).toContain("dangerItem");
  });

  it("closes on Escape", () => {
    renderMenu(true);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
