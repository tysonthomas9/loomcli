/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for NavRail component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { NavRail } from "../NavRail";

// Mock useWorkspaceContext — default to single-repo (isMultiRepo: false)
const mockUseWorkspaceContext = vi.fn(() => ({
  isMultiRepo: false,
}));

vi.mock("@/hooks", () => ({
  useWorkspaceContext: (...args: unknown[]) => mockUseWorkspaceContext(...args),
}));

describe("NavRail", () => {
  beforeEach(() => {
    mockUseWorkspaceContext.mockReturnValue({ isMultiRepo: false });
  });

  describe("rendering", () => {
    it("renders a Kanban button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Kanban")).toBeInTheDocument();
    });

    it("renders a Settings button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Settings")).toBeInTheDocument();
    });

    it("renders an Observability button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Observability")).toBeInTheDocument();
    });

    it("does not render a Monitor button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Monitor")).not.toBeInTheDocument();
    });

    it("renders a List button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("List")).toBeInTheDocument();
    });

    it("renders a Terminal button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Terminal")).toBeInTheDocument();
    });

    it("hides Workspace button in single-repo mode", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Workspace")).not.toBeInTheDocument();
    });

    it("shows Workspace button in multi-repo mode", () => {
      mockUseWorkspaceContext.mockReturnValue({ isMultiRepo: true });
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Workspace")).toBeInTheDocument();
    });

    it("renders exactly six navigation buttons in single-repo mode", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole("button");
      expect(buttons).toHaveLength(6);
    });

    it("renders seven navigation buttons in multi-repo mode", () => {
      mockUseWorkspaceContext.mockReturnValue({ isMultiRepo: true });
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole("button");
      expect(buttons).toHaveLength(7);
    });

    it("renders a Files button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Files")).toBeInTheDocument();
    });

    it("renders tooltips for each button in single-repo mode", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(
        screen.getByRole("tooltip", { name: "Kanban" }),
      ).toBeInTheDocument();
      expect(screen.getByRole("tooltip", { name: "List" })).toBeInTheDocument();
      expect(
        screen.getByRole("tooltip", { name: "Observability" }),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("tooltip", { name: "Workspace" }),
      ).not.toBeInTheDocument();
      expect(
        screen.getByRole("tooltip", { name: "Files" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("tooltip", { name: "Terminal" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("tooltip", { name: "Settings" }),
      ).toBeInTheDocument();
    });

    it("renders Terminal as the 3rd nav item (between List and Observability)", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole("button");
      // TOP_ITEMS order: kanban(0), table(1), terminal(2), observability(3), files(4), workspace(5), settings is BOTTOM
      expect(buttons[0]).toHaveAccessibleName("Kanban");
      expect(buttons[1]).toHaveAccessibleName("List");
      expect(buttons[2]).toHaveAccessibleName("Terminal");
      expect(buttons[3]).toHaveAccessibleName("Observability");
    });

    it("has navigation landmark with aria-label", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(
        screen.getByRole("navigation", { name: "Primary" }),
      ).toBeInTheDocument();
    });

    it("marks active view with data-active attribute", () => {
      render(<NavRail activeView="settings" onChange={() => {}} />);

      const settingsButton = screen.getByLabelText("Settings");
      const kanbanButton = screen.getByLabelText("Kanban");

      expect(settingsButton).toHaveAttribute("data-active");
      expect(kanbanButton).not.toHaveAttribute("data-active");
    });

    it("marks kanban as active when activeView is kanban", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const kanbanButton = screen.getByLabelText("Kanban");
      const settingsButton = screen.getByLabelText("Settings");

      expect(kanbanButton).toHaveAttribute("data-active");
      expect(settingsButton).not.toHaveAttribute("data-active");
    });

    it("applies custom className", () => {
      render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          className="custom-class"
        />,
      );

      expect(screen.getByRole("navigation")).toHaveClass("custom-class");
    });
  });

  describe("interactions", () => {
    it('calls onChange with "kanban" when Kanban button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="settings" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText("Kanban"));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("kanban");
    });

    it('calls onChange with "table" when List button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText("List"));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("table");
    });

    it('calls onChange with "settings" when Settings button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText("Settings"));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("settings");
    });

    it("calls onChange when clicking already active button", () => {
      const onChange = vi.fn();
      render(<NavRail activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText("Kanban"));

      expect(onChange).toHaveBeenCalledWith("kanban");
    });
  });

  describe("session badge", () => {
    it("does not render badge when sessionCount is undefined", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(
        screen.queryByLabelText(/active sessions/),
      ).not.toBeInTheDocument();
    });

    it("does not render badge when sessionCount is 0", () => {
      render(
        <NavRail activeView="kanban" onChange={() => {}} sessionCount={0} />,
      );

      expect(
        screen.queryByLabelText(/active sessions/),
      ).not.toBeInTheDocument();
    });

    it("renders badge on terminal button when sessionCount > 0", () => {
      render(
        <NavRail activeView="kanban" onChange={() => {}} sessionCount={3} />,
      );

      expect(screen.getByLabelText("3 active sessions")).toBeInTheDocument();
    });

    it("badge only appears on terminal button, not other buttons", () => {
      render(
        <NavRail activeView="kanban" onChange={() => {}} sessionCount={2} />,
      );

      // Badge is inside the Terminal button
      const badge = screen.getByLabelText("2 active sessions");
      const terminalButton = screen.getByLabelText("Terminal");
      expect(terminalButton).toContainElement(badge);

      // Other buttons do not contain the badge
      const kanbanButton = screen.getByLabelText("Kanban");
      expect(kanbanButton).not.toContainElement(badge);
    });
  });
});
