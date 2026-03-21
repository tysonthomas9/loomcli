/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EmptyWorkspaceBoard component.
 * Covers default props, isMultiRepo / hasFiltersActive variations,
 * filter precedence, and accessibility attributes.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { EmptyWorkspaceBoard } from "../EmptyWorkspaceBoard";

describe("EmptyWorkspaceBoard", () => {
  describe("default props", () => {
    it("renders correctly with default props", () => {
      render(<EmptyWorkspaceBoard />);

      expect(
        screen.getByRole("heading", { name: "No issues yet" }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "Create your first issue with bd new or import from your tracker",
        ),
      ).toBeInTheDocument();
    });
  });

  describe("headline text", () => {
    it('shows "No issues yet" when isMultiRepo is false (default)', () => {
      render(<EmptyWorkspaceBoard />);

      expect(
        screen.getByRole("heading", { name: "No issues yet" }),
      ).toBeInTheDocument();
    });

    it('shows "No issues in this workspace" when isMultiRepo is true', () => {
      render(<EmptyWorkspaceBoard isMultiRepo />);

      expect(
        screen.getByRole("heading", {
          name: "No issues in this workspace",
        }),
      ).toBeInTheDocument();
    });

    it('shows "No issues match your filters" when hasFiltersActive is true', () => {
      render(<EmptyWorkspaceBoard hasFiltersActive />);

      expect(
        screen.getByRole("heading", {
          name: "No issues match your filters",
        }),
      ).toBeInTheDocument();
    });

    it("hasFiltersActive takes precedence over isMultiRepo", () => {
      render(<EmptyWorkspaceBoard isMultiRepo hasFiltersActive />);

      expect(
        screen.getByRole("heading", {
          name: "No issues match your filters",
        }),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("heading", {
          name: "No issues in this workspace",
        }),
      ).not.toBeInTheDocument();
    });
  });

  describe("subtitle text", () => {
    it("shows filter guidance when hasFiltersActive is true", () => {
      render(<EmptyWorkspaceBoard hasFiltersActive />);

      expect(
        screen.getByText(
          "Try adjusting or clearing your filters to see issues",
        ),
      ).toBeInTheDocument();
    });

    it("shows create guidance when isMultiRepo is true and no filters active", () => {
      render(<EmptyWorkspaceBoard isMultiRepo />);

      expect(
        screen.getByText(
          "Create your first issue with bd new or import from your tracker",
        ),
      ).toBeInTheDocument();
    });

    it("shows create guidance with default props", () => {
      render(<EmptyWorkspaceBoard />);

      expect(
        screen.getByText(
          "Create your first issue with bd new or import from your tracker",
        ),
      ).toBeInTheDocument();
    });
  });

  describe("data-testid", () => {
    it('has data-testid="empty-workspace-board"', () => {
      render(<EmptyWorkspaceBoard />);

      expect(screen.getByTestId("empty-workspace-board")).toBeInTheDocument();
    });
  });

  describe("accessibility", () => {
    it('has role="status"', () => {
      render(<EmptyWorkspaceBoard />);

      expect(screen.getByRole("status")).toBeInTheDocument();
    });

    it("has aria-label matching headline for default state", () => {
      render(<EmptyWorkspaceBoard />);

      const statusElement = screen.getByRole("status");
      expect(statusElement).toHaveAttribute("aria-label", "No issues yet");
    });

    it("has aria-label matching headline for isMultiRepo state", () => {
      render(<EmptyWorkspaceBoard isMultiRepo />);

      const statusElement = screen.getByRole("status");
      expect(statusElement).toHaveAttribute(
        "aria-label",
        "No issues in this workspace",
      );
    });

    it("has aria-label matching headline for hasFiltersActive state", () => {
      render(<EmptyWorkspaceBoard hasFiltersActive />);

      const statusElement = screen.getByRole("status");
      expect(statusElement).toHaveAttribute(
        "aria-label",
        "No issues match your filters",
      );
    });
  });

  describe("SVG icon", () => {
    it("renders an SVG icon", () => {
      const { container } = render(<EmptyWorkspaceBoard />);

      const svg = container.querySelector("svg");
      expect(svg).toBeInTheDocument();
    });

    it("SVG icon is aria-hidden", () => {
      const { container } = render(<EmptyWorkspaceBoard />);

      const iconWrapper = container.querySelector("[aria-hidden='true']");
      expect(iconWrapper).toBeInTheDocument();
      expect(iconWrapper?.querySelector("svg")).toBeInTheDocument();
    });
  });

  describe("element structure", () => {
    it("headline is an h3 element", () => {
      render(<EmptyWorkspaceBoard />);

      const heading = screen.getByRole("heading", { name: "No issues yet" });
      expect(heading.tagName).toBe("H3");
    });

    it("subtitle is a paragraph element", () => {
      const { container } = render(<EmptyWorkspaceBoard />);

      const paragraphs = container.querySelectorAll("p");
      expect(paragraphs.length).toBe(1);
    });
  });
});
