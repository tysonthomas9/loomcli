/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceContextMenu component.
 * Covers rendering, click interactions, and dismiss behaviour.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { WorkspaceContextMenu } from "../WorkspaceContextMenu";

// Mock CSS module
vi.mock("../WorkspaceContextMenu.module.css", () => ({
  default: {
    menu: "menu",
    menuItem: "menuItem",
    menuItemIcon: "menuItemIcon",
  },
}));

describe("WorkspaceContextMenu", () => {
  let onRename: ReturnType<typeof vi.fn>;
  let onRemove: ReturnType<typeof vi.fn>;
  let onClose: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onRename = vi.fn();
    onRemove = vi.fn();
    onClose = vi.fn();
  });

  const defaultPosition = { x: 100, y: 200 };

  const renderMenu = (isOpen = true) =>
    render(
      <WorkspaceContextMenu
        isOpen={isOpen}
        position={defaultPosition}
        onRename={onRename}
        onRemove={onRemove}
        onClose={onClose}
      />,
    );

  // ── Rendering ──────────────────────────────────────────────────────────────

  describe("rendering", () => {
    it("renders null when isOpen is false", () => {
      const { container } = renderMenu(false);

      expect(screen.queryByRole("menu")).not.toBeInTheDocument();
      expect(container.innerHTML).toBe("");
    });

    it("renders menu with Rename item when isOpen is true", () => {
      renderMenu(true);

      expect(screen.getByRole("menu")).toBeInTheDocument();
      expect(screen.getByTestId("workspace-context-menu")).toBeInTheDocument();
      expect(screen.getByText("Rename")).toBeInTheDocument();
      expect(screen.getAllByRole("menuitem").length).toBeGreaterThanOrEqual(1);
    });

    it("positions the menu at the given coordinates", () => {
      renderMenu(true);

      const menu = screen.getByTestId("workspace-context-menu");
      expect(menu.style.left).toBe("100px");
      expect(menu.style.top).toBe("200px");
    });
  });

  // ── Click interactions ─────────────────────────────────────────────────────

  describe("click interactions", () => {
    it("fires onRename and onClose when Rename is clicked", () => {
      renderMenu(true);

      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onRename and onClose when Enter key is pressed on Rename", () => {
      renderMenu(true);

      const button = screen.getByTestId("workspace-context-menu-rename");
      fireEvent.keyDown(button, { key: "Enter" });

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onRename and onClose when Space key is pressed on Rename", () => {
      renderMenu(true);

      const button = screen.getByTestId("workspace-context-menu-rename");
      fireEvent.keyDown(button, { key: " " });

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  // ── Dismiss behaviour ──────────────────────────────────────────────────────

  describe("dismiss behaviour", () => {
    it("fires onClose on outside click", () => {
      renderMenu(true);

      fireEvent.mouseDown(document.body);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not fire onClose on mousedown inside the menu", () => {
      renderMenu(true);

      const menu = screen.getByRole("menu");
      fireEvent.mouseDown(menu);

      expect(onClose).not.toHaveBeenCalled();
    });

    it("fires onClose on Escape key", () => {
      renderMenu(true);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not fire onClose for non-Escape keys", () => {
      renderMenu(true);

      fireEvent.keyDown(document, { key: "Tab" });
      fireEvent.keyDown(document, { key: "a" });
      fireEvent.keyDown(document, { key: "Enter" });

      expect(onClose).not.toHaveBeenCalled();
    });
  });

  // ── Default workspace menu item ──────────────────────────────────────────

  describe("default workspace menu item", () => {
    it('renders "Set as default" when isDefault is false', () => {
      const onSetDefault = vi.fn();
      const onClearDefault = vi.fn();

      render(
        <WorkspaceContextMenu
          isOpen={true}
          position={defaultPosition}
          onRename={onRename}
          onRemove={onRemove}
          onClose={onClose}
          isDefault={false}
          onSetDefault={onSetDefault}
          onClearDefault={onClearDefault}
        />,
      );

      expect(screen.getByText("Set as default")).toBeInTheDocument();
      expect(screen.queryByText("Clear default")).not.toBeInTheDocument();
    });

    it('renders "Clear default" when isDefault is true', () => {
      const onSetDefault = vi.fn();
      const onClearDefault = vi.fn();

      render(
        <WorkspaceContextMenu
          isOpen={true}
          position={defaultPosition}
          onRename={onRename}
          onRemove={onRemove}
          onClose={onClose}
          isDefault={true}
          onSetDefault={onSetDefault}
          onClearDefault={onClearDefault}
        />,
      );

      expect(screen.getByText("Clear default")).toBeInTheDocument();
      expect(screen.queryByText("Set as default")).not.toBeInTheDocument();
    });

    it('calls onSetDefault when "Set as default" is clicked', () => {
      const onSetDefault = vi.fn();
      const onClearDefault = vi.fn();

      render(
        <WorkspaceContextMenu
          isOpen={true}
          position={defaultPosition}
          onRename={onRename}
          onRemove={onRemove}
          onClose={onClose}
          isDefault={false}
          onSetDefault={onSetDefault}
          onClearDefault={onClearDefault}
        />,
      );

      fireEvent.click(screen.getByTestId("workspace-context-menu-default"));

      expect(onSetDefault).toHaveBeenCalledTimes(1);
      expect(onClearDefault).not.toHaveBeenCalled();
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('calls onClearDefault when "Clear default" is clicked', () => {
      const onSetDefault = vi.fn();
      const onClearDefault = vi.fn();

      render(
        <WorkspaceContextMenu
          isOpen={true}
          position={defaultPosition}
          onRename={onRename}
          onRemove={onRemove}
          onClose={onClose}
          isDefault={true}
          onSetDefault={onSetDefault}
          onClearDefault={onClearDefault}
        />,
      );

      fireEvent.click(screen.getByTestId("workspace-context-menu-default"));

      expect(onClearDefault).toHaveBeenCalledTimes(1);
      expect(onSetDefault).not.toHaveBeenCalled();
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not render default menu item when onSetDefault and onClearDefault are not provided", () => {
      renderMenu(true);

      expect(
        screen.queryByTestId("workspace-context-menu-default"),
      ).not.toBeInTheDocument();
      expect(screen.queryByText("Set as default")).not.toBeInTheDocument();
      expect(screen.queryByText("Clear default")).not.toBeInTheDocument();
    });
  });

  // ── Cleanup ────────────────────────────────────────────────────────────────

  describe("cleanup", () => {
    it("removes event listeners on unmount", () => {
      const { unmount } = renderMenu(true);

      unmount();

      fireEvent.keyDown(document, { key: "Escape" });
      fireEvent.mouseDown(document.body);

      expect(onClose).not.toHaveBeenCalled();
    });

    it("does not attach event listeners when isOpen is false", () => {
      renderMenu(false);

      fireEvent.keyDown(document, { key: "Escape" });
      fireEvent.mouseDown(document.body);

      expect(onClose).not.toHaveBeenCalled();
    });
  });
});
