/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ThemeToggle component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { ThemeToggle } from "../ThemeToggle";

describe("ThemeToggle", () => {
  describe("rendering", () => {
    it("renders without crashing", () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toBeInTheDocument();
    });

    it("renders as a button element", () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      const button = screen.getByRole("button");
      expect(button.tagName).toBe("BUTTON");
    });

    it('has type="button" to prevent form submission', () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute("type", "button");
    });
  });

  describe("pill switch", () => {
    it("renders a sliding knob (no icon svg)", () => {
      const { container } = render(
        <ThemeToggle theme="light" onToggle={() => {}} />,
      );
      // Aether pill switch has no sun/moon svg — just a knob span.
      expect(container.querySelector("svg")).toBeNull();
      expect(container.querySelector("span[aria-hidden]")).toBeInTheDocument();
    });

    it("marks dark state via data-dark in dark mode", () => {
      render(<ThemeToggle theme="dark" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute("data-dark", "true");
    });

    it("does not set data-dark in light mode", () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      expect(screen.getByRole("button")).not.toHaveAttribute("data-dark");
    });

    it("reflects state via aria-pressed (pressed = light)", () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-pressed",
        "true",
      );
    });
  });

  describe("accessibility", () => {
    it('has aria-label "Switch to dark mode" in light mode', () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-label",
        "Switch to dark mode",
      );
    });

    it('has aria-label "Switch to light mode" in dark mode', () => {
      render(<ThemeToggle theme="dark" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-label",
        "Switch to light mode",
      );
    });

    it('has title "Switch to dark mode" in light mode', () => {
      render(<ThemeToggle theme="light" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "title",
        "Switch to dark mode",
      );
    });

    it('has title "Switch to light mode" in dark mode', () => {
      render(<ThemeToggle theme="dark" onToggle={() => {}} />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "title",
        "Switch to light mode",
      );
    });
  });

  describe("interaction", () => {
    it("calls onToggle when clicked", () => {
      const onToggle = vi.fn();
      render(<ThemeToggle theme="light" onToggle={onToggle} />);

      fireEvent.click(screen.getByRole("button"));

      expect(onToggle).toHaveBeenCalledTimes(1);
    });

    it("calls onToggle on each click", () => {
      const onToggle = vi.fn();
      render(<ThemeToggle theme="light" onToggle={onToggle} />);

      const button = screen.getByRole("button");
      fireEvent.click(button);
      fireEvent.click(button);
      fireEvent.click(button);

      expect(onToggle).toHaveBeenCalledTimes(3);
    });
  });

  describe("theme transitions", () => {
    it("updates the data-dark state when theme prop changes from light to dark", () => {
      const { rerender } = render(
        <ThemeToggle theme="light" onToggle={() => {}} />,
      );

      // Light mode: no data-dark
      expect(screen.getByRole("button")).not.toHaveAttribute("data-dark");

      rerender(<ThemeToggle theme="dark" onToggle={() => {}} />);

      // Dark mode: data-dark set
      expect(screen.getByRole("button")).toHaveAttribute("data-dark", "true");
    });

    it("updates aria-label when theme prop changes", () => {
      const { rerender } = render(
        <ThemeToggle theme="light" onToggle={() => {}} />,
      );

      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-label",
        "Switch to dark mode",
      );

      rerender(<ThemeToggle theme="dark" onToggle={() => {}} />);

      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-label",
        "Switch to light mode",
      );
    });
  });
});
