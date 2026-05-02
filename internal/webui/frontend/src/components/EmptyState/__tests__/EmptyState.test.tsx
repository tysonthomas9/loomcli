/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EmptyState component.
 * Covers all three variants, accessibility attributes, and custom className.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { EmptyState } from "../EmptyState";
import type { EmptyStateVariant } from "../EmptyState";

const variants: EmptyStateVariant[] = [
  "no-workspaces",
  "no-issues",
  "no-agents",
];

const expectedTitles: Record<EmptyStateVariant, string> = {
  "no-workspaces": "No workspaces configured",
  "no-issues": "No issues yet",
  "no-agents": "No agents running",
};

describe("EmptyState", () => {
  describe("variant titles", () => {
    it('renders "No workspaces configured" for no-workspaces variant', () => {
      render(<EmptyState variant="no-workspaces" />);

      expect(
        screen.getByRole("heading", { name: "No workspaces configured" }),
      ).toBeInTheDocument();
    });

    it('renders "No issues yet" for no-issues variant', () => {
      render(<EmptyState variant="no-issues" />);

      expect(
        screen.getByRole("heading", { name: "No issues yet" }),
      ).toBeInTheDocument();
    });

    it('renders "No agents running" for no-agents variant', () => {
      render(<EmptyState variant="no-agents" />);

      expect(
        screen.getByRole("heading", { name: "No agents running" }),
      ).toBeInTheDocument();
    });
  });

  describe("variant descriptions", () => {
    it("no-workspaces variant includes loom.yaml guidance", () => {
      render(<EmptyState variant="no-workspaces" />);

      expect(screen.getByText(/loom\.yaml/)).toBeInTheDocument();
      expect(screen.getByText(/loom init/)).toBeInTheDocument();
    });

    it("no-issues variant includes New issue guidance", () => {
      render(<EmptyState variant="no-issues" />);

      expect(screen.getByText(/New issue/)).toBeInTheDocument();
      expect(screen.getByText(/loom data ready/)).toBeInTheDocument();
    });

    it("no-agents variant includes loom spawn guidance", () => {
      render(<EmptyState variant="no-agents" />);

      expect(screen.getByText(/loom spawn/)).toBeInTheDocument();
    });
  });

  describe("data-variant attribute", () => {
    it.each(variants)('sets data-variant="%s" for %s variant', (variant) => {
      render(<EmptyState variant={variant} />);

      expect(screen.getByTestId("empty-state")).toHaveAttribute(
        "data-variant",
        variant,
      );
    });
  });

  describe("accessibility", () => {
    it('has role="status"', () => {
      render(<EmptyState variant="no-workspaces" />);

      expect(screen.getByRole("status")).toBeInTheDocument();
    });

    it("has aria-label matching title text", () => {
      render(<EmptyState variant="no-issues" />);

      const statusElement = screen.getByRole("status");
      expect(statusElement).toHaveAttribute("aria-label", "No issues yet");
    });

    it("aria-label matches title for each variant", () => {
      variants.forEach((variant) => {
        const { unmount } = render(<EmptyState variant={variant} />);

        const statusElement = screen.getByRole("status");
        expect(statusElement).toHaveAttribute(
          "aria-label",
          expectedTitles[variant],
        );

        unmount();
      });
    });
  });

  describe("data-testid", () => {
    it('has data-testid="empty-state"', () => {
      render(<EmptyState variant="no-agents" />);

      expect(screen.getByTestId("empty-state")).toBeInTheDocument();
    });
  });

  describe("className prop", () => {
    it("applies custom className to root element", () => {
      render(<EmptyState variant="no-workspaces" className="custom-class" />);

      expect(screen.getByTestId("empty-state")).toHaveClass("custom-class");
    });

    it("root element has base styles when no custom className", () => {
      const { container } = render(<EmptyState variant="no-issues" />);

      const root = container.firstChild as HTMLElement;
      // Should have the CSS module class but not any custom class
      expect(root.className).toBeTruthy();
      expect(root.className).not.toContain("custom-class");
    });
  });

  describe("SVG icon rendering", () => {
    it("renders an SVG icon for no-workspaces variant", () => {
      const { container } = render(<EmptyState variant="no-workspaces" />);

      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute("aria-hidden", "true");
    });

    it("renders an SVG icon for no-issues variant", () => {
      const { container } = render(<EmptyState variant="no-issues" />);

      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute("aria-hidden", "true");
    });

    it("renders an SVG icon for no-agents variant", () => {
      const { container } = render(<EmptyState variant="no-agents" />);

      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute("aria-hidden", "true");
    });
  });

  describe("edge cases", () => {
    it("renders correctly with all props provided", () => {
      const { container } = render(
        <EmptyState variant="no-agents" className="extra-class" />,
      );

      expect(
        screen.getByRole("heading", { name: "No agents running" }),
      ).toBeInTheDocument();
      expect(screen.getByTestId("empty-state")).toHaveClass("extra-class");
      expect(screen.getByTestId("empty-state")).toHaveAttribute(
        "data-variant",
        "no-agents",
      );
      expect(screen.getByRole("status")).toHaveAttribute(
        "aria-label",
        "No agents running",
      );
      expect(container.querySelector("svg")).toBeInTheDocument();
    });

    it("each variant renders a unique title", () => {
      const titles = new Set<string>();

      variants.forEach((variant) => {
        const { unmount } = render(<EmptyState variant={variant} />);

        const heading = screen.getByRole("heading");
        titles.add(heading.textContent!);

        unmount();
      });

      expect(titles.size).toBe(variants.length);
    });

    it("title is an h3 element", () => {
      render(<EmptyState variant="no-workspaces" />);

      const heading = screen.getByRole("heading", {
        name: "No workspaces configured",
      });
      expect(heading.tagName).toBe("H3");
    });

    it("description is a paragraph element", () => {
      const { container } = render(<EmptyState variant="no-issues" />);

      const paragraphs = container.querySelectorAll("p");
      expect(paragraphs.length).toBe(1);
    });
  });
});
