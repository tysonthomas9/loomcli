/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for NavRail component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { expectNoA11yViolations } from "@/test-utils/a11y-helpers";
import { NavRail } from "../NavRail";

describe("NavRail", () => {
  describe("rendering", () => {
    it("renders a Kanban button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Kanban")).toBeInTheDocument();
    });

    it("renders a Settings button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Settings")).toBeInTheDocument();
    });

    it("renders a List button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("List")).toBeInTheDocument();
    });

    it("renders a Terminal button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Terminal")).toBeInTheDocument();
    });

    it("does not render Observability, Files, or Workspace buttons", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Observability")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Files")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Workspace")).not.toBeInTheDocument();
    });

    it("does not render a Monitor button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Monitor")).not.toBeInTheDocument();
    });

    it("renders exactly four navigation buttons", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole("button");
      expect(buttons).toHaveLength(4);
    });

    it("renders tooltips for each button", () => {
      const { container } = render(
        <NavRail activeView="kanban" onChange={() => {}} />,
      );

      const tooltips = container.querySelectorAll(
        "span.tooltip, [class*='tooltip']",
      );
      const tooltipTexts = Array.from(tooltips).map((t) => t.textContent);
      expect(tooltipTexts).toContain("Kanban");
      expect(tooltipTexts).toContain("List");
      expect(tooltipTexts).toContain("Terminal");
      expect(tooltipTexts).toContain("Settings");
    });

    it("each button has a title attribute matching its label", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Kanban")).toHaveAttribute(
        "title",
        "Kanban",
      );
      expect(screen.getByLabelText("List")).toHaveAttribute("title", "List");
      expect(screen.getByLabelText("Terminal")).toHaveAttribute(
        "title",
        "Terminal",
      );
      expect(screen.getByLabelText("Settings")).toHaveAttribute(
        "title",
        "Settings",
      );
    });

    it("renders buttons in correct order: Kanban, List, Terminal", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole("button");
      expect(buttons[0]).toHaveAccessibleName("Kanban");
      expect(buttons[1]).toHaveAccessibleName("List");
      expect(buttons[2]).toHaveAccessibleName("Terminal");
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

  describe("accessibility", () => {
    it("has no axe violations", async () => {
      const { container } = render(
        <NavRail activeView="kanban" onChange={() => {}} />,
      );

      await expectNoA11yViolations(container);
    });

    it("has no axe violations with active session badge", async () => {
      const { container } = render(
        <NavRail activeView="kanban" onChange={() => {}} sessionCount={3} />,
      );

      await expectNoA11yViolations(container);
    });

    it("has no axe violations with unread indicator", async () => {
      const { container } = render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          badges={{ terminal: true }}
        />,
      );

      await expectNoA11yViolations(container);
    });
  });

  describe("unread indicator", () => {
    it("renders unread indicator when badges={{ terminal: true }} and terminal is not active", () => {
      render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          badges={{ terminal: true }}
        />,
      );

      const indicator = screen.getByLabelText("has unread output");
      expect(indicator).toBeInTheDocument();

      // Indicator should be inside the Terminal button
      const terminalButton = screen.getByLabelText("Terminal");
      expect(terminalButton).toContainElement(indicator);
    });

    it("does NOT render unread indicator when terminal IS the active view", () => {
      render(
        <NavRail
          activeView="terminal"
          onChange={() => {}}
          badges={{ terminal: true }}
        />,
      );

      expect(
        screen.queryByLabelText("has unread output"),
      ).not.toBeInTheDocument();
    });

    it("does not render unread indicator without badges prop", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(
        screen.queryByLabelText("has unread output"),
      ).not.toBeInTheDocument();
    });

    it("does not render unread indicator when badges={{ terminal: false }}", () => {
      render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          badges={{ terminal: false }}
        />,
      );

      expect(
        screen.queryByLabelText("has unread output"),
      ).not.toBeInTheDocument();
    });

    it("does not render unread indicator for non-terminal badges on their active view", () => {
      render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          badges={{ kanban: true }}
        />,
      );

      // kanban is active, so its badge should not render
      expect(
        screen.queryByLabelText("has unread output"),
      ).not.toBeInTheDocument();
    });
  });
});
