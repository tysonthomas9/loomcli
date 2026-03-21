/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TabContextMenu component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { TabContextMenu } from "../TabContextMenu";

// Mock CSS module
vi.mock("../TerminalTabBar.module.css", () => ({
  default: {
    contextMenu: "contextMenu",
    contextMenuItem: "contextMenuItem",
    contextMenuItemDisabled: "contextMenuItemDisabled",
    contextMenuDivider: "contextMenuDivider",
    pinned: "pinned",
    dragging: "dragging",
  },
}));

const defaultProps = {
  tabId: "tab-1",
  isPinned: false,
  x: 100,
  y: 200,
  tabCount: 3,
  onClose: vi.fn(),
  onDismiss: vi.fn(),
};

describe("TabContextMenu", () => {
  describe("rendering", () => {
    it("renders the context menu with correct testid", () => {
      render(<TabContextMenu {...defaultProps} />);

      expect(
        screen.getByTestId("terminal-tab-context-menu"),
      ).toBeInTheDocument();
    });

    it("positions the menu at the specified x and y coordinates", () => {
      render(<TabContextMenu {...defaultProps} x={150} y={250} />);

      const menu = screen.getByTestId("terminal-tab-context-menu");
      expect(menu.style.left).toBe("150px");
      expect(menu.style.top).toBe("250px");
    });

    it('has role="menu"', () => {
      render(<TabContextMenu {...defaultProps} />);

      expect(screen.getByRole("menu")).toBeInTheDocument();
    });
  });

  describe("Duplicate button", () => {
    it("renders Duplicate when onDuplicate is provided", () => {
      render(
        <TabContextMenu {...defaultProps} onDuplicate={vi.fn()} />,
      );

      expect(
        screen.getByTestId("context-menu-duplicate"),
      ).toBeInTheDocument();
      expect(screen.getByText("Duplicate")).toBeInTheDocument();
    });

    it("does NOT render Duplicate when onDuplicate is not provided", () => {
      render(<TabContextMenu {...defaultProps} />);

      expect(
        screen.queryByTestId("context-menu-duplicate"),
      ).not.toBeInTheDocument();
    });

    it("calls onDuplicate and onDismiss when clicked", () => {
      const onDuplicate = vi.fn();
      const onDismiss = vi.fn();
      render(
        <TabContextMenu
          {...defaultProps}
          onDuplicate={onDuplicate}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.click(screen.getByTestId("context-menu-duplicate"));

      expect(onDuplicate).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("is disabled when maxTabsReached is true", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          onDuplicate={vi.fn()}
          maxTabsReached={true}
        />,
      );

      expect(screen.getByTestId("context-menu-duplicate")).toBeDisabled();
    });

    it("has 'Maximum tabs reached' title when maxTabsReached is true", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          onDuplicate={vi.fn()}
          maxTabsReached={true}
        />,
      );

      expect(screen.getByTestId("context-menu-duplicate")).toHaveAttribute(
        "title",
        "Maximum tabs reached",
      );
    });

    it("is enabled when maxTabsReached is false", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          onDuplicate={vi.fn()}
          maxTabsReached={false}
        />,
      );

      expect(screen.getByTestId("context-menu-duplicate")).toBeEnabled();
    });
  });

  describe("Rename button", () => {
    it("renders Rename when onRename is provided", () => {
      render(
        <TabContextMenu {...defaultProps} onRename={vi.fn()} />,
      );

      expect(
        screen.getByTestId("context-menu-rename"),
      ).toBeInTheDocument();
    });

    it("does NOT render Rename when onRename is not provided", () => {
      render(<TabContextMenu {...defaultProps} />);

      expect(
        screen.queryByTestId("context-menu-rename"),
      ).not.toBeInTheDocument();
    });

    it("calls onRename and onDismiss when clicked", () => {
      const onRename = vi.fn();
      const onDismiss = vi.fn();
      render(
        <TabContextMenu
          {...defaultProps}
          onRename={onRename}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.click(screen.getByTestId("context-menu-rename"));

      expect(onRename).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe("Pin/Unpin button", () => {
    it("renders 'Pin' when isPinned is false", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          isPinned={false}
          onPin={vi.fn()}
        />,
      );

      expect(screen.getByTestId("context-menu-pin")).toHaveTextContent(
        "Pin",
      );
    });

    it("renders 'Unpin' when isPinned is true", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          isPinned={true}
          onPin={vi.fn()}
        />,
      );

      expect(screen.getByTestId("context-menu-pin")).toHaveTextContent(
        "Unpin",
      );
    });

    it("does NOT render Pin when onPin is not provided", () => {
      render(<TabContextMenu {...defaultProps} />);

      expect(
        screen.queryByTestId("context-menu-pin"),
      ).not.toBeInTheDocument();
    });

    it("calls onPin and onDismiss when clicked", () => {
      const onPin = vi.fn();
      const onDismiss = vi.fn();
      render(
        <TabContextMenu
          {...defaultProps}
          onPin={onPin}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.click(screen.getByTestId("context-menu-pin"));

      expect(onPin).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe("Close button", () => {
    it("renders Close when tabCount > 1", () => {
      render(<TabContextMenu {...defaultProps} tabCount={2} />);

      expect(
        screen.getByTestId("context-menu-close"),
      ).toBeInTheDocument();
    });

    it("does NOT render Close when tabCount is 1", () => {
      render(<TabContextMenu {...defaultProps} tabCount={1} />);

      expect(
        screen.queryByTestId("context-menu-close"),
      ).not.toBeInTheDocument();
    });

    it("calls onClose and onDismiss when clicked", () => {
      const onClose = vi.fn();
      const onDismiss = vi.fn();
      render(
        <TabContextMenu
          {...defaultProps}
          onClose={onClose}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.click(screen.getByTestId("context-menu-close"));

      expect(onClose).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe("Close Others button", () => {
    it("renders Close Others when onCloseOthers is provided and tabCount > 1", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          tabCount={3}
          onCloseOthers={vi.fn()}
        />,
      );

      expect(
        screen.getByTestId("context-menu-close-others"),
      ).toBeInTheDocument();
    });

    it("does NOT render Close Others when tabCount is 1", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          tabCount={1}
          onCloseOthers={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("context-menu-close-others"),
      ).not.toBeInTheDocument();
    });

    it("does NOT render Close Others when onCloseOthers is not provided", () => {
      render(<TabContextMenu {...defaultProps} tabCount={3} />);

      expect(
        screen.queryByTestId("context-menu-close-others"),
      ).not.toBeInTheDocument();
    });

    it("calls onCloseOthers and onDismiss when clicked", () => {
      const onCloseOthers = vi.fn();
      const onDismiss = vi.fn();
      render(
        <TabContextMenu
          {...defaultProps}
          onCloseOthers={onCloseOthers}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.click(screen.getByTestId("context-menu-close-others"));

      expect(onCloseOthers).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe("Close All button", () => {
    it("renders Close All when onCloseAll is provided and tabCount > 1", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          tabCount={3}
          onCloseAll={vi.fn()}
        />,
      );

      expect(
        screen.getByTestId("context-menu-close-all"),
      ).toBeInTheDocument();
    });

    it("does NOT render Close All when tabCount is 1", () => {
      render(
        <TabContextMenu
          {...defaultProps}
          tabCount={1}
          onCloseAll={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("context-menu-close-all"),
      ).not.toBeInTheDocument();
    });

    it("calls onCloseAll and onDismiss when clicked", () => {
      const onCloseAll = vi.fn();
      const onDismiss = vi.fn();
      render(
        <TabContextMenu
          {...defaultProps}
          onCloseAll={onCloseAll}
          onDismiss={onDismiss}
        />,
      );

      fireEvent.click(screen.getByTestId("context-menu-close-all"));

      expect(onCloseAll).toHaveBeenCalledTimes(1);
      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe("dismiss behavior", () => {
    it("calls onDismiss when Escape is pressed", () => {
      const onDismiss = vi.fn();
      render(<TabContextMenu {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("calls onDismiss when clicking outside the menu", () => {
      const onDismiss = vi.fn();
      render(<TabContextMenu {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.mouseDown(document);

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });

    it("does NOT call onDismiss when clicking inside the menu", () => {
      const onDismiss = vi.fn();
      render(<TabContextMenu {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.mouseDown(
        screen.getByTestId("terminal-tab-context-menu"),
      );

      expect(onDismiss).not.toHaveBeenCalled();
    });

    it("calls onDismiss on scroll", () => {
      const onDismiss = vi.fn();
      render(<TabContextMenu {...defaultProps} onDismiss={onDismiss} />);

      fireEvent.scroll(window);

      expect(onDismiss).toHaveBeenCalledTimes(1);
    });
  });

  describe("divider", () => {
    it("renders divider when action buttons are present and tabCount > 1", () => {
      const { container } = render(
        <TabContextMenu
          {...defaultProps}
          tabCount={3}
          onDuplicate={vi.fn()}
        />,
      );

      const dividers = container.querySelectorAll(".contextMenuDivider");
      expect(dividers.length).toBeGreaterThan(0);
    });

    it("does NOT render divider when no action buttons are present", () => {
      const { container } = render(
        <TabContextMenu {...defaultProps} tabCount={1} />,
      );

      const dividers = container.querySelectorAll(".contextMenuDivider");
      expect(dividers.length).toBe(0);
    });
  });
});
