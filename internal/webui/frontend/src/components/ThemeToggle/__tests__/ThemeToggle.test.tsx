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

  describe("icons", () => {
    it("renders moon icon in light mode", () => {
      const { container } = render(
        <ThemeToggle theme="light" onToggle={() => {}} />,
      );
      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
      // Moon icon uses a path with the crescent shape, no circle element
      const circle = svg?.querySelector("circle");
      const path = svg?.querySelector("path");
      expect(circle).toBeNull();
      expect(path).toBeInTheDocument();
    });

    it("renders sun icon in dark mode", () => {
      const { container } = render(
        <ThemeToggle theme="dark" onToggle={() => {}} />,
      );
      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
      // Sun icon has a circle element (the sun body)
      const circle = svg?.querySelector("circle");
      expect(circle).toBeInTheDocument();
    });

    it("svg has aria-hidden true", () => {
      const { container } = render(
        <ThemeToggle theme="light" onToggle={() => {}} />,
      );
      const svg = container.querySelector("svg");
      expect(svg).toHaveAttribute("aria-hidden", "true");
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
    it("updates icon when theme prop changes from light to dark", () => {
      const { container, rerender } = render(
        <ThemeToggle theme="light" onToggle={() => {}} />,
      );

      // Light mode: moon icon (no circle)
      let svg = container.querySelector("svg");
      expect(svg?.querySelector("circle")).toBeNull();

      rerender(<ThemeToggle theme="dark" onToggle={() => {}} />);

      // Dark mode: sun icon (has circle)
      svg = container.querySelector("svg");
      expect(svg?.querySelector("circle")).toBeInTheDocument();
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
