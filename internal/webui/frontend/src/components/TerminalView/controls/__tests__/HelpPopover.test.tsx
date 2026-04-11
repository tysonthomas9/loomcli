/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for HelpPopover component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { HelpPopover } from "../HelpPopover";

// Mock CSS module
vi.mock("../HelpPopover.module.css", () => ({
  default: {
    popover: "popover",
    sectionTitle: "sectionTitle",
    row: "row",
    kbd: "kbd",
    command: "command",
    commandDesc: "commandDesc",
  },
}));

describe("HelpPopover", () => {
  describe("rendering", () => {
    it("renders popover when isOpen is true", () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByTestId("terminal-help-popover")).toBeInTheDocument();
    });

    it("returns null when isOpen is false", () => {
      const { container } = render(
        <HelpPopover isOpen={false} onClose={vi.fn()} />,
      );

      expect(container.innerHTML).toBe("");
    });

    it("shows Keyboard Shortcuts section title", () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByText("Keyboard Shortcuts")).toBeInTheDocument();
    });

    it("shows Slash Commands section title", () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByText("Slash Commands")).toBeInTheDocument();
    });
  });

  describe("keyboard shortcuts content", () => {
    it("displays all keyboard shortcuts", () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByText("Search in terminal")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+F")).toBeInTheDocument();
      expect(screen.getByText("Copy")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+Shift+C")).toBeInTheDocument();
      expect(screen.getByText("Paste")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+Shift+V")).toBeInTheDocument();
      expect(screen.getByText("New tab")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+T")).toBeInTheDocument();
      expect(screen.getByText("Close tab")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+W")).toBeInTheDocument();
      expect(screen.getByText("Next tab")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+Tab")).toBeInTheDocument();
      expect(screen.getByText("Previous tab")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+Shift+Tab")).toBeInTheDocument();
    });
  });

  describe("slash commands content", () => {
    it("displays all slash commands", () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByText("/help")).toBeInTheDocument();
      expect(screen.getByText("Show available commands")).toBeInTheDocument();
      expect(screen.getByText("/clear")).toBeInTheDocument();
      expect(screen.getByText("Clear terminal output")).toBeInTheDocument();
    });
  });

  describe("dismiss behavior", () => {
    it("calls onClose when Escape is pressed", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("calls onClose when clicking outside the popover", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.mouseDown(document);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does NOT call onClose when clicking inside the popover", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.mouseDown(screen.getByTestId("terminal-help-popover"));

      expect(onClose).not.toHaveBeenCalled();
    });

    it("does NOT register event listeners when isOpen is false", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={false} onClose={onClose} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it('has role="dialog"', () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it('has aria-label="Terminal help"', () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByRole("dialog")).toHaveAttribute(
        "aria-label",
        "Terminal help",
      );
    });

    it('has aria-modal="true"', () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByRole("dialog")).toHaveAttribute("aria-modal", "true");
    });

    it("has tabIndex=-1 for programmatic focus", () => {
      render(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByTestId("terminal-help-popover")).toHaveAttribute(
        "tabindex",
        "-1",
      );
    });
  });

  describe("visibility transitions", () => {
    it("appears when isOpen changes from false to true", () => {
      const { rerender } = render(
        <HelpPopover isOpen={false} onClose={vi.fn()} />,
      );

      expect(
        screen.queryByTestId("terminal-help-popover"),
      ).not.toBeInTheDocument();

      rerender(<HelpPopover isOpen={true} onClose={vi.fn()} />);

      expect(screen.getByTestId("terminal-help-popover")).toBeInTheDocument();
    });

    it("disappears when isOpen changes from true to false", () => {
      const { rerender } = render(
        <HelpPopover isOpen={true} onClose={vi.fn()} />,
      );

      expect(screen.getByTestId("terminal-help-popover")).toBeInTheDocument();

      rerender(<HelpPopover isOpen={false} onClose={vi.fn()} />);

      expect(
        screen.queryByTestId("terminal-help-popover"),
      ).not.toBeInTheDocument();
    });
  });
});
