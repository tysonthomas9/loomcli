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
import { NavRail, WORKSPACE_SWITCHER_LIST_MAX_HEIGHT_PX } from "../NavRail";

describe("NavRail", () => {
  describe("rendering", () => {
    it("renders a Workspaces button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Workspaces")).toBeInTheDocument();
    });

    it("renders a Settings button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Settings")).toBeInTheDocument();
    });

    it("renders a Terminal button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Terminal")).toBeInTheDocument();
    });

    it("does not render an Agents button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Agents")).not.toBeInTheDocument();
    });

    it("renders a Pull Requests button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.getByLabelText("Pull Requests")).toBeInTheDocument();
    });

    it("does not render a List button", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("List")).not.toBeInTheDocument();
    });

    it("does not render a Monitor button (relabeled to Terminal)", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Monitor")).not.toBeInTheDocument();
    });

    it("does not render Observability, Files, or Workspace buttons", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(screen.queryByLabelText("Observability")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Files")).not.toBeInTheDocument();
      expect(screen.queryByLabelText("Workspace")).not.toBeInTheDocument();
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
      expect(tooltipTexts).toContain("Workspaces");
      expect(tooltipTexts).not.toContain("Agents");
      expect(tooltipTexts).toContain("Pull Requests");
      expect(tooltipTexts).toContain("Terminal");
      expect(tooltipTexts).toContain("Settings");
    });

    it("each button exposes a hover tooltip with its label", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      expect(
        screen.getByRole("tooltip", { name: "Workspaces" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("tooltip", { name: "Terminal" }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("tooltip", { name: "Settings" }),
      ).toBeInTheDocument();
    });

    it("renders buttons in correct order: Workspaces, Pull Requests, Terminal, Settings", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const buttons = screen.getAllByRole("button");
      expect(buttons[0]).toHaveAccessibleName("Workspaces");
      expect(buttons[1]).toHaveAccessibleName("Pull Requests");
      expect(buttons[2]).toHaveAccessibleName("Terminal");
      expect(buttons[3]).toHaveAccessibleName("Settings");
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
      const workspacesButton = screen.getByLabelText("Workspaces");

      expect(settingsButton).toHaveAttribute("data-active");
      expect(workspacesButton).not.toHaveAttribute("data-active");
    });

    it("marks Workspaces as active when activeView is kanban", () => {
      render(<NavRail activeView="kanban" onChange={() => {}} />);

      const workspacesButton = screen.getByLabelText("Workspaces");
      const settingsButton = screen.getByLabelText("Settings");

      expect(workspacesButton).toHaveAttribute("data-active");
      expect(settingsButton).not.toHaveAttribute("data-active");
    });

    it("marks Workspaces as active when activeView is table", () => {
      render(<NavRail activeView="table" onChange={() => {}} />);

      const workspacesButton = screen.getByLabelText("Workspaces");

      expect(workspacesButton).toHaveAttribute("data-active");
    });

    it("marks Terminal as active when activeView is terminal", () => {
      render(<NavRail activeView="terminal" onChange={() => {}} />);

      const terminalButton = screen.getByLabelText("Terminal");

      expect(terminalButton).toHaveAttribute("data-active");
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
    it('calls onChange with "kanban" when Workspaces button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="settings" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText("Workspaces"));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("kanban");
    });

    it('calls onChange with "terminal" when Terminal button is clicked', () => {
      const onChange = vi.fn();
      render(<NavRail activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByLabelText("Terminal"));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("terminal");
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

      fireEvent.click(screen.getByLabelText("Workspaces"));

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

    it("renders badge on Monitor button when sessionCount > 0", () => {
      render(
        <NavRail activeView="kanban" onChange={() => {}} sessionCount={3} />,
      );

      expect(screen.getByLabelText("3 active sessions")).toBeInTheDocument();
    });

    it("badge only appears on Terminal button, not other buttons", () => {
      render(
        <NavRail activeView="kanban" onChange={() => {}} sessionCount={2} />,
      );

      // Badge is inside the Terminal button
      const badge = screen.getByLabelText("2 active sessions");
      const terminalButton = screen.getByLabelText("Terminal");
      expect(terminalButton).toContainElement(badge);

      // Other buttons do not contain the badge
      const workspacesButton = screen.getByLabelText("Workspaces");
      expect(workspacesButton).not.toContainElement(badge);
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

  describe("workspace switcher", () => {
    const manyWorkspaces = Array.from({ length: 8 }, (_, i) => ({
      id: `ws-${i}`,
      name: `Workspace ${i}`,
    }));

    it("uses the wireframe max-height for the scrollable workspace list", () => {
      const { container } = render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          workspaces={manyWorkspaces}
          activeWorkspaceId="ws-0"
          onWorkspaceSwitch={() => {}}
        />,
      );

      const list = container.querySelector('[class*="workspaceList"]');
      expect(list).toBeInTheDocument();
      expect(WORKSPACE_SWITCHER_LIST_MAX_HEIGHT_PX).toBe(210);
    });

    it("renders workspace selector with dividers and pinned add button", () => {
      render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          workspaces={manyWorkspaces}
          activeWorkspaceId="ws-1"
          onWorkspaceSwitch={() => {}}
          onAddWorkspace={() => {}}
        />,
      );

      expect(
        screen.getByRole("region", { name: "Workspace selector" }),
      ).toBeInTheDocument();
      expect(
        screen.getByLabelText("Switch to Workspace 7"),
      ).toBeInTheDocument();
      expect(screen.getByLabelText("Add workspace")).toBeInTheDocument();
    });

    it("shows workspace name in a hover tooltip", () => {
      render(
        <NavRail
          activeView="kanban"
          onChange={() => {}}
          workspaces={[{ id: "hello", name: "Hello-World" }]}
          activeWorkspaceId="hello"
          onWorkspaceSwitch={() => {}}
        />,
      );

      const workspaceButton = screen.getByLabelText("Switch to Hello-World");
      expect(workspaceButton).toHaveTextContent("HW");
      fireEvent.mouseEnter(workspaceButton);
      expect(
        screen.getByRole("tooltip", { name: "Hello-World" }),
      ).toBeInTheDocument();
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
