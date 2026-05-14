/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for Talk to Lead terminal onboarding components:
 * NoBackendsEmptyState, HelpPopover.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { NoBackendsEmptyState } from "../NoBackendsEmptyState";
import { HelpPopover } from "@/components/TerminalView/controls";

// Mock CSS modules
vi.mock("../NoBackendsEmptyState.module.css", () => ({
  default: {
    container: "container",
    icon: "icon",
    heading: "heading",
    description: "description",
    settingsButton: "settingsButton",
  },
}));

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

describe("NoBackendsEmptyState", () => {
  describe("rendering", () => {
    it("renders heading", () => {
      render(<NoBackendsEmptyState />);

      expect(screen.getByText("No backends configured")).toBeInTheDocument();
    });

    it("renders description", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.getByText(
          "Configure at least one AI backend to start using Talk to Lead.",
        ),
      ).toBeInTheDocument();
    });

    it("has role=status", () => {
      render(<NoBackendsEmptyState />);

      expect(screen.getByTestId("no-backends-empty-state")).toHaveAttribute(
        "role",
        "status",
      );
    });
  });

  describe("interactions", () => {
    it("calls onGoToSettings when button is clicked", () => {
      const onGoToSettings = vi.fn();
      render(<NoBackendsEmptyState onGoToSettings={onGoToSettings} />);

      fireEvent.click(screen.getByTestId("go-to-settings-button"));

      expect(onGoToSettings).toHaveBeenCalledTimes(1);
    });

    it("renders Go to Settings button when onGoToSettings is provided", () => {
      const onGoToSettings = vi.fn();
      render(<NoBackendsEmptyState onGoToSettings={onGoToSettings} />);

      expect(screen.getByTestId("go-to-settings-button")).toBeInTheDocument();
      expect(screen.getByTestId("go-to-settings-button")).toHaveTextContent(
        "Go to Settings",
      );
    });

    it("hides button when onGoToSettings is not provided", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.queryByTestId("go-to-settings-button"),
      ).not.toBeInTheDocument();
    });
  });
});

describe("HelpPopover", () => {
  const defaultProps = {
    isOpen: true,
    onClose: vi.fn(),
  };

  beforeEach(() => {
    defaultProps.onClose = vi.fn();
  });

  describe("rendering", () => {
    it("renders shortcuts section", () => {
      render(<HelpPopover {...defaultProps} />);

      expect(screen.getByText("Keyboard Shortcuts")).toBeInTheDocument();
      expect(screen.getByText("Search in terminal")).toBeInTheDocument();
      expect(screen.getByText("Ctrl+F")).toBeInTheDocument();
      expect(screen.queryByText("New tab")).not.toBeInTheDocument();
      expect(screen.queryByText("Ctrl+T")).not.toBeInTheDocument();
    });

    it("renders slash commands section", () => {
      render(<HelpPopover {...defaultProps} />);

      expect(screen.getByText("Slash Commands")).toBeInTheDocument();
      expect(screen.getByText("/help")).toBeInTheDocument();
      expect(screen.getByText("Show available commands")).toBeInTheDocument();
      expect(screen.getByText("/clear")).toBeInTheDocument();
      expect(screen.getByText("Clear terminal output")).toBeInTheDocument();
    });

    it("has role=dialog with aria-label", () => {
      render(<HelpPopover {...defaultProps} />);

      const popover = screen.getByTestId("terminal-help-popover");
      expect(popover).toHaveAttribute("role", "dialog");
      expect(popover).toHaveAttribute("aria-label", "Terminal help");
    });
  });

  describe("interactions", () => {
    it("closes on Escape keydown", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("closes on click outside", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      fireEvent.mouseDown(document);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not close on click inside the popover", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={true} onClose={onClose} />);

      const popover = screen.getByTestId("terminal-help-popover");
      fireEvent.mouseDown(popover);

      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe("closed state", () => {
    it("returns null when isOpen is false", () => {
      const { container } = render(
        <HelpPopover isOpen={false} onClose={vi.fn()} />,
      );

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("terminal-help-popover"),
      ).not.toBeInTheDocument();
    });

    it("does not register event listeners when closed", () => {
      const onClose = vi.fn();
      render(<HelpPopover isOpen={false} onClose={onClose} />);

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).not.toHaveBeenCalled();
    });
  });
});
