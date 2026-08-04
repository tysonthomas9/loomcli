/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SortableTab component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { SortableTab } from "../SortableTab";

// Mock CSS module
vi.mock("../TerminalTabBar.module.css", () => ({
  default: {
    pinned: "pinned",
    dragging: "dragging",
    tab: "tab",
    active: "active",
  },
}));

// Mock @dnd-kit/sortable
const mockUseSortable = vi.fn();
vi.mock("@dnd-kit/sortable", () => ({
  useSortable: (args: unknown) => mockUseSortable(args),
}));

const defaultSortableReturn = {
  attributes: { role: "button" as const, tabIndex: 0 },
  listeners: {},
  setNodeRef: vi.fn(),
  transform: null,
  transition: null,
  isDragging: false,
};

const defaultProps = {
  id: "tab-1",
  children: <span>Tab Content</span>,
  className: "tab",
  isPinned: false,
  isActive: true,
  onClick: vi.fn(),
  onContextMenu: vi.fn(),
  onKeyDown: vi.fn(),
  "data-testid": "sortable-tab-1",
};

describe("SortableTab", () => {
  beforeEach(() => {
    mockUseSortable.mockReturnValue(defaultSortableReturn);
  });

  describe("rendering", () => {
    it("renders children content", () => {
      render(<SortableTab {...defaultProps} />);

      expect(screen.getByText("Tab Content")).toBeInTheDocument();
    });

    it("renders with the correct data-testid", () => {
      render(<SortableTab {...defaultProps} />);

      expect(screen.getByTestId("sortable-tab-1")).toBeInTheDocument();
    });

    it("calls useSortable with the correct id", () => {
      render(<SortableTab {...defaultProps} id="my-tab" />);

      expect(mockUseSortable).toHaveBeenCalledWith({
        id: "my-tab",
        disabled: false,
      });
    });
  });

  describe("ARIA attributes", () => {
    it('has role="tab"', () => {
      render(<SortableTab {...defaultProps} />);

      expect(screen.getByRole("tab")).toBeInTheDocument();
    });

    it("has aria-selected=true when isActive is true", () => {
      render(<SortableTab {...defaultProps} isActive={true} />);

      expect(screen.getByRole("tab")).toHaveAttribute("aria-selected", "true");
    });

    it("has aria-selected=false when isActive is false", () => {
      render(<SortableTab {...defaultProps} isActive={false} />);

      expect(screen.getByRole("tab")).toHaveAttribute("aria-selected", "false");
    });

    it("has tabIndex=0 when isActive is true", () => {
      render(<SortableTab {...defaultProps} isActive={true} />);

      expect(screen.getByTestId("sortable-tab-1")).toHaveAttribute(
        "tabindex",
        "0",
      );
    });

    it("has tabIndex=-1 when isActive is false", () => {
      render(<SortableTab {...defaultProps} isActive={false} />);

      expect(screen.getByTestId("sortable-tab-1")).toHaveAttribute(
        "tabindex",
        "-1",
      );
    });

    it("has correct aria-controls", () => {
      render(<SortableTab {...defaultProps} id="tab-xyz" />);

      expect(screen.getByRole("tab")).toHaveAttribute(
        "aria-controls",
        "terminal-panel-tab-xyz",
      );
    });

    it("has correct id attribute", () => {
      render(<SortableTab {...defaultProps} id="tab-abc" />);

      expect(
        document.getElementById("terminal-tab-tab-abc"),
      ).toBeInTheDocument();
    });
  });

  describe("event handlers", () => {
    it("calls onClick when clicked", () => {
      const onClick = vi.fn();
      render(<SortableTab {...defaultProps} onClick={onClick} />);

      fireEvent.click(screen.getByTestId("sortable-tab-1"));

      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it("calls onContextMenu on right-click", () => {
      const onContextMenu = vi.fn();
      render(<SortableTab {...defaultProps} onContextMenu={onContextMenu} />);

      fireEvent.contextMenu(screen.getByTestId("sortable-tab-1"));

      expect(onContextMenu).toHaveBeenCalledTimes(1);
    });

    it("calls onKeyDown on key press", () => {
      const onKeyDown = vi.fn();
      render(<SortableTab {...defaultProps} onKeyDown={onKeyDown} />);

      fireEvent.keyDown(screen.getByTestId("sortable-tab-1"), {
        key: "Enter",
      });

      expect(onKeyDown).toHaveBeenCalledTimes(1);
    });
  });

  describe("className composition", () => {
    it("includes base className", () => {
      render(<SortableTab {...defaultProps} className="myTab" />);

      expect(screen.getByTestId("sortable-tab-1").className).toContain("myTab");
    });

    it("includes pinned class when isPinned is true", () => {
      render(<SortableTab {...defaultProps} isPinned={true} />);

      expect(screen.getByTestId("sortable-tab-1").className).toContain(
        "pinned",
      );
    });

    it("does NOT include pinned class when isPinned is false", () => {
      render(<SortableTab {...defaultProps} isPinned={false} />);

      expect(screen.getByTestId("sortable-tab-1").className).not.toContain(
        "pinned",
      );
    });
  });

  describe("drag state", () => {
    it("applies reduced opacity when isDragging is true", () => {
      mockUseSortable.mockReturnValue({
        ...defaultSortableReturn,
        isDragging: true,
      });

      render(<SortableTab {...defaultProps} />);

      expect(screen.getByTestId("sortable-tab-1").style.opacity).toBe("0.5");
    });

    it("applies full opacity when not dragging", () => {
      render(<SortableTab {...defaultProps} />);

      expect(screen.getByTestId("sortable-tab-1").style.opacity).toBe("1");
    });

    it("applies transform style when transform is provided", () => {
      mockUseSortable.mockReturnValue({
        ...defaultSortableReturn,
        transform: { x: 50, y: 0, scaleX: 1, scaleY: 1 },
      });

      render(<SortableTab {...defaultProps} />);

      expect(screen.getByTestId("sortable-tab-1").style.transform).toBe(
        "translate3d(50px, 0, 0)",
      );
    });

    it("includes dragging class when isDragging is true", () => {
      mockUseSortable.mockReturnValue({
        ...defaultSortableReturn,
        isDragging: true,
      });

      render(<SortableTab {...defaultProps} />);

      expect(screen.getByTestId("sortable-tab-1").className).toContain(
        "dragging",
      );
    });

    it("sets zIndex to 10 when dragging", () => {
      mockUseSortable.mockReturnValue({
        ...defaultSortableReturn,
        isDragging: true,
      });

      render(<SortableTab {...defaultProps} />);

      expect(screen.getByTestId("sortable-tab-1").style.zIndex).toBe("10");
    });
  });
});
