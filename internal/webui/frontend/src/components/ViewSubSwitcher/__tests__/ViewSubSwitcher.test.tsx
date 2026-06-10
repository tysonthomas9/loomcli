/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ViewSubSwitcher component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { expectNoA11yViolations } from "@/test-utils/a11y-helpers";
import { ViewSubSwitcher } from "../ViewSubSwitcher";

describe("ViewSubSwitcher", () => {
  describe("rendering", () => {
    it('renders "Kanban" and "List" tabs when activeView is "kanban"', () => {
      render(<ViewSubSwitcher activeView="kanban" onChange={() => {}} />);

      expect(screen.getByRole("tab", { name: "Kanban" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "List" })).toBeInTheDocument();
    });

    it('renders "Kanban" and "List" tabs when activeView is "table"', () => {
      render(<ViewSubSwitcher activeView="table" onChange={() => {}} />);

      expect(screen.getByRole("tab", { name: "Kanban" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "List" })).toBeInTheDocument();
    });

    it('returns null when activeView is "terminal"', () => {
      const { container } = render(
        <ViewSubSwitcher activeView="terminal" onChange={() => {}} />,
      );

      expect(container.innerHTML).toBe("");
    });

    it('returns null when activeView is "settings"', () => {
      const { container } = render(
        <ViewSubSwitcher activeView="settings" onChange={() => {}} />,
      );

      expect(container.innerHTML).toBe("");
    });

    it("container has tablist role with aria-label", () => {
      render(<ViewSubSwitcher activeView="kanban" onChange={() => {}} />);

      expect(
        screen.getByRole("tablist", { name: "View mode" }),
      ).toBeInTheDocument();
    });
  });

  describe("active state", () => {
    it('"Kanban" tab is active when activeView is "kanban"', () => {
      render(<ViewSubSwitcher activeView="kanban" onChange={() => {}} />);

      const kanbanTab = screen.getByRole("tab", { name: "Kanban" });
      const listTab = screen.getByRole("tab", { name: "List" });

      expect(kanbanTab).toHaveAttribute("data-active");
      expect(kanbanTab).toHaveAttribute("aria-selected", "true");
      expect(listTab).not.toHaveAttribute("data-active");
      expect(listTab).toHaveAttribute("aria-selected", "false");
    });

    it('"List" tab is active when activeView is "table"', () => {
      render(<ViewSubSwitcher activeView="table" onChange={() => {}} />);

      const kanbanTab = screen.getByRole("tab", { name: "Kanban" });
      const listTab = screen.getByRole("tab", { name: "List" });

      expect(listTab).toHaveAttribute("data-active");
      expect(listTab).toHaveAttribute("aria-selected", "true");
      expect(kanbanTab).not.toHaveAttribute("data-active");
      expect(kanbanTab).toHaveAttribute("aria-selected", "false");
    });
  });

  describe("interactions", () => {
    it('clicking "List" calls onChange("list")', () => {
      const onChange = vi.fn();
      render(<ViewSubSwitcher activeView="kanban" onChange={onChange} />);

      fireEvent.click(screen.getByRole("tab", { name: "List" }));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("list");
    });

    it('clicking "Kanban" calls onChange("kanban")', () => {
      const onChange = vi.fn();
      render(<ViewSubSwitcher activeView="table" onChange={onChange} />);

      fireEvent.click(screen.getByRole("tab", { name: "Kanban" }));

      expect(onChange).toHaveBeenCalledTimes(1);
      expect(onChange).toHaveBeenCalledWith("kanban");
    });
  });

  describe("accessibility", () => {
    it("has no axe violations", async () => {
      const { container } = render(
        <ViewSubSwitcher activeView="kanban" onChange={() => {}} />,
      );

      await expectNoA11yViolations(container);
    });
  });
});
