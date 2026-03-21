/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalContextMenu component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { TerminalContextMenu } from "../TerminalContextMenu";

// Mock CSS module
vi.mock("../TerminalContextMenu.module.css", () => ({
  default: {
    menu: "menu",
    item: "item",
    shortcut: "shortcut",
  },
}));

describe("TerminalContextMenu", () => {
  let onCopy: ReturnType<typeof vi.fn>;
  let onPaste: ReturnType<typeof vi.fn>;
  let onSelectAll: ReturnType<typeof vi.fn>;
  let onClose: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onCopy = vi.fn();
    onPaste = vi.fn();
    onSelectAll = vi.fn();
    onClose = vi.fn();
  });

  const renderMenu = (hasSelection = false) =>
    render(
      <TerminalContextMenu
        x={100}
        y={200}
        hasSelection={hasSelection}
        onCopy={onCopy}
        onPaste={onPaste}
        onSelectAll={onSelectAll}
        onClose={onClose}
      />,
    );

  // ── Rendering ──────────────────────────────────────────────────────────────

  describe("rendering", () => {
    it("renders three menu items: Copy, Paste, Select All", () => {
      renderMenu();

      const items = screen.getAllByRole("menuitem");
      expect(items).toHaveLength(3);
      expect(screen.getByText("Copy")).toBeInTheDocument();
      expect(screen.getByText("Paste")).toBeInTheDocument();
      expect(screen.getByText("Select All")).toBeInTheDocument();
    });

    it('renders a container with role="menu"', () => {
      renderMenu();

      expect(screen.getByRole("menu")).toBeInTheDocument();
    });

    it("renders into document.body via portal", () => {
      renderMenu();

      // The menu should be a direct child of document.body
      const menu = screen.getByRole("menu");
      expect(menu.parentElement).toBe(document.body);
    });

    it("shows platform shortcut hints with correct modifier key", () => {
      renderMenu();

      // In jsdom, navigator.platform is empty string, so isMac = false => "Ctrl+"
      const items = screen.getAllByRole("menuitem");
      expect(items[0]).toHaveTextContent("Ctrl+C");
      expect(items[1]).toHaveTextContent("Ctrl+V");
      expect(items[2]).toHaveTextContent("Ctrl+A");
    });
  });

  // ── Copy disabled state ────────────────────────────────────────────────────

  describe("copy disabled state", () => {
    it("Copy is disabled when hasSelection is false", () => {
      renderMenu(false);

      const copyButton = screen.getByText("Copy").closest("button")!;
      expect(copyButton).toHaveAttribute("data-disabled", "true");
      expect(copyButton).toHaveAttribute("tabIndex", "-1");
    });

    it("Copy is enabled when hasSelection is true", () => {
      renderMenu(true);

      const copyButton = screen.getByText("Copy").closest("button")!;
      expect(copyButton).toHaveAttribute("data-disabled", "false");
      expect(copyButton).toHaveAttribute("tabIndex", "0");
    });

    it("Paste is always enabled regardless of hasSelection", () => {
      renderMenu(false);

      const pasteButton = screen.getByText("Paste").closest("button")!;
      expect(pasteButton).not.toHaveAttribute("data-disabled");
      expect(pasteButton).toHaveAttribute("tabIndex", "0");
    });

    it("Select All is always enabled regardless of hasSelection", () => {
      renderMenu(false);

      const selectAllButton = screen.getByText("Select All").closest("button")!;
      expect(selectAllButton).not.toHaveAttribute("data-disabled");
      expect(selectAllButton).toHaveAttribute("tabIndex", "0");
    });
  });

  // ── Click interactions ─────────────────────────────────────────────────────

  describe("click interactions", () => {
    it("calls onCopy when Copy is clicked and hasSelection is true", () => {
      renderMenu(true);

      fireEvent.click(screen.getByText("Copy").closest("button")!);
      expect(onCopy).toHaveBeenCalledTimes(1);
    });

    it("calls onPaste when Paste is clicked", () => {
      renderMenu();

      fireEvent.click(screen.getByText("Paste").closest("button")!);
      expect(onPaste).toHaveBeenCalledTimes(1);
    });

    it("calls onSelectAll when Select All is clicked", () => {
      renderMenu();

      fireEvent.click(screen.getByText("Select All").closest("button")!);
      expect(onSelectAll).toHaveBeenCalledTimes(1);
    });

    it("does not call other callbacks when Copy is clicked", () => {
      renderMenu(true);

      fireEvent.click(screen.getByText("Copy").closest("button")!);
      expect(onPaste).not.toHaveBeenCalled();
      expect(onSelectAll).not.toHaveBeenCalled();
    });

    it("does not call other callbacks when Paste is clicked", () => {
      renderMenu();

      fireEvent.click(screen.getByText("Paste").closest("button")!);
      expect(onCopy).not.toHaveBeenCalled();
      expect(onSelectAll).not.toHaveBeenCalled();
    });

    it("does not call other callbacks when Select All is clicked", () => {
      renderMenu();

      fireEvent.click(screen.getByText("Select All").closest("button")!);
      expect(onCopy).not.toHaveBeenCalled();
      expect(onPaste).not.toHaveBeenCalled();
    });
  });

  // ── Dismiss behavior ──────────────────────────────────────────────────────

  describe("dismiss behavior", () => {
    it("calls onClose when Escape key is pressed", () => {
      renderMenu();

      fireEvent.keyDown(document, { key: "Escape" });
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("calls onClose on mousedown outside the menu", () => {
      renderMenu();

      // mousedown on document.body (outside the menu)
      fireEvent.mouseDown(document.body);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not call onClose on mousedown inside the menu", () => {
      renderMenu();

      const menu = screen.getByRole("menu");
      fireEvent.mouseDown(menu);
      expect(onClose).not.toHaveBeenCalled();
    });

    it("does not call onClose for non-Escape keys", () => {
      renderMenu();

      fireEvent.keyDown(document, { key: "Tab" });
      fireEvent.keyDown(document, { key: "a" });
      fireEvent.keyDown(document, { key: "Enter" });
      expect(onClose).not.toHaveBeenCalled();
    });

    it("calls onClose on window scroll", () => {
      renderMenu();

      fireEvent.scroll(window);
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("calls onClose on window resize", () => {
      renderMenu();

      fireEvent.resize(window);
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  // ── Positioning ────────────────────────────────────────────────────────────

  describe("positioning", () => {
    it("applies left and top style from x and y props", () => {
      renderMenu();

      const menu = screen.getByRole("menu");
      expect(menu.style.left).toBe("100px");
      expect(menu.style.top).toBe("200px");
    });
  });

  // ── Cleanup ────────────────────────────────────────────────────────────────

  describe("cleanup", () => {
    it("removes event listeners on unmount", () => {
      const { unmount } = renderMenu();

      unmount();

      // After unmount, keydown Escape should not call onClose
      fireEvent.keyDown(document, { key: "Escape" });
      expect(onClose).not.toHaveBeenCalled();
    });
  });
});
