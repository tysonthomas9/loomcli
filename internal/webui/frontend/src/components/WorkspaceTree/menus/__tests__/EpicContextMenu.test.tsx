/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EpicContextMenu component.
 * Covers rendering, click interactions, and dismiss behaviour.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { EpicContextMenu } from "../EpicContextMenu";

// Mock CSS module
vi.mock("../WorkspaceContextMenu.module.css", () => ({
  default: {
    menu: "menu",
    menuItem: "menuItem",
    menuItemIcon: "menuItemIcon",
    dangerItem: "dangerItem",
  },
}));

describe("EpicContextMenu", () => {
  let onRename: ReturnType<typeof vi.fn>;
  let onMarkDone: ReturnType<typeof vi.fn>;
  let onArchive: ReturnType<typeof vi.fn>;
  let onClose: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onRename = vi.fn();
    onMarkDone = vi.fn();
    onArchive = vi.fn();
    onClose = vi.fn();
  });

  const defaultPosition = { x: 100, y: 200 };

  const renderMenu = (isOpen = true) =>
    render(
      <EpicContextMenu
        isOpen={isOpen}
        position={defaultPosition}
        onRename={onRename}
        onMarkDone={onMarkDone}
        onArchive={onArchive}
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

    it("renders three menu items when isOpen is true", () => {
      renderMenu(true);

      expect(screen.getByRole("menu")).toBeInTheDocument();
      expect(screen.getByTestId("epic-context-menu")).toBeInTheDocument();
      expect(screen.getByText("Rename")).toBeInTheDocument();
      expect(screen.getByText("Mark as Done")).toBeInTheDocument();
      expect(screen.getByText("Archive")).toBeInTheDocument();
      expect(screen.getAllByRole("menuitem")).toHaveLength(3);
    });

    it("positions the menu at the given coordinates", () => {
      renderMenu(true);

      const menu = screen.getByTestId("epic-context-menu");
      expect(menu.style.left).toBe("100px");
      expect(menu.style.top).toBe("200px");
    });
  });

  // ── Click interactions ─────────────────────────────────────────────────────

  describe("click interactions", () => {
    it("fires onRename and onClose when Rename is clicked", () => {
      renderMenu(true);

      fireEvent.click(screen.getByTestId("epic-context-menu-rename"));

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onRename and onClose when Enter key is pressed on Rename", () => {
      renderMenu(true);

      const button = screen.getByTestId("epic-context-menu-rename");
      fireEvent.keyDown(button, { key: "Enter" });

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onRename and onClose when Space key is pressed on Rename", () => {
      renderMenu(true);

      const button = screen.getByTestId("epic-context-menu-rename");
      fireEvent.keyDown(button, { key: " " });

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onMarkDone and onClose when Mark as Done is clicked", () => {
      renderMenu(true);

      fireEvent.click(screen.getByTestId("epic-context-menu-done"));

      expect(onMarkDone).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onMarkDone and onClose when Enter key is pressed on Mark as Done", () => {
      renderMenu(true);

      const button = screen.getByTestId("epic-context-menu-done");
      fireEvent.keyDown(button, { key: "Enter" });

      expect(onMarkDone).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onArchive and onClose when Archive is clicked", () => {
      renderMenu(true);

      fireEvent.click(screen.getByTestId("epic-context-menu-archive"));

      expect(onArchive).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("fires onArchive and onClose when Enter key is pressed on Archive", () => {
      renderMenu(true);

      const button = screen.getByTestId("epic-context-menu-archive");
      fireEvent.keyDown(button, { key: "Enter" });

      expect(onArchive).toHaveBeenCalledTimes(1);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("applies dangerItem styling to Archive button", () => {
      renderMenu(true);

      const archiveButton = screen.getByTestId("epic-context-menu-archive");
      expect(archiveButton.className).toContain("dangerItem");
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
