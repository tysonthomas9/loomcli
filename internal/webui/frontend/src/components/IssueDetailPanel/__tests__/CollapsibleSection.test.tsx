/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CollapsibleSection component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { CollapsibleSection } from "../CollapsibleSection";

describe("CollapsibleSection", () => {
  describe("rendering", () => {
    it("renders the title", () => {
      render(
        <CollapsibleSection title="My Section">
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("My Section")).toBeInTheDocument();
    });

    it("renders children when expanded by default", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Child content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("Child content")).toBeInTheDocument();
    });

    it("renders count when provided", () => {
      render(
        <CollapsibleSection title="Items" count={5}>
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("(5)")).toBeInTheDocument();
    });

    it("does not render count when not provided", () => {
      render(
        <CollapsibleSection title="Items">
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.queryByText(/\(/)).not.toBeInTheDocument();
    });

    it("renders count of zero when explicitly provided", () => {
      render(
        <CollapsibleSection title="Items" count={0}>
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("(0)")).toBeInTheDocument();
    });

    it("applies testId when provided", () => {
      render(
        <CollapsibleSection title="Section" testId="my-section">
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByTestId("my-section")).toBeInTheDocument();
    });

    it("renders the chevron SVG", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Content</p>
        </CollapsibleSection>,
      );
      const svg = document.querySelector("svg");
      expect(svg).toBeInTheDocument();
      expect(svg).toHaveAttribute("aria-hidden", "true");
    });
  });

  describe("defaultExpanded behavior", () => {
    it("defaults to expanded (children visible)", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Visible</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("Visible")).toBeInTheDocument();
    });

    it("starts collapsed when defaultExpanded is false", () => {
      render(
        <CollapsibleSection title="Section" defaultExpanded={false}>
          <p>Hidden content</p>
        </CollapsibleSection>,
      );
      expect(screen.queryByText("Hidden content")).not.toBeInTheDocument();
    });

    it("starts expanded when defaultExpanded is true", () => {
      render(
        <CollapsibleSection title="Section" defaultExpanded={true}>
          <p>Shown content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("Shown content")).toBeInTheDocument();
    });
  });

  describe("toggle behavior", () => {
    it("collapses when header button is clicked while expanded", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Content to hide</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("Content to hide")).toBeInTheDocument();

      fireEvent.click(screen.getByRole("button"));
      expect(screen.queryByText("Content to hide")).not.toBeInTheDocument();
    });

    it("expands when header button is clicked while collapsed", () => {
      render(
        <CollapsibleSection title="Section" defaultExpanded={false}>
          <p>Content to show</p>
        </CollapsibleSection>,
      );
      expect(screen.queryByText("Content to show")).not.toBeInTheDocument();

      fireEvent.click(screen.getByRole("button"));
      expect(screen.getByText("Content to show")).toBeInTheDocument();
    });

    it("toggles multiple times", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Toggle me</p>
        </CollapsibleSection>,
      );
      const button = screen.getByRole("button");

      // Start expanded
      expect(screen.getByText("Toggle me")).toBeInTheDocument();

      // Collapse
      fireEvent.click(button);
      expect(screen.queryByText("Toggle me")).not.toBeInTheDocument();

      // Expand
      fireEvent.click(button);
      expect(screen.getByText("Toggle me")).toBeInTheDocument();
    });
  });

  describe("accessibility", () => {
    it("has aria-expanded=true when expanded", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-expanded",
        "true",
      );
    });

    it("has aria-expanded=false when collapsed", () => {
      render(
        <CollapsibleSection title="Section" defaultExpanded={false}>
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-expanded",
        "false",
      );
    });

    it("updates aria-expanded when toggled", () => {
      render(
        <CollapsibleSection title="Section">
          <p>Content</p>
        </CollapsibleSection>,
      );
      const button = screen.getByRole("button");
      expect(button).toHaveAttribute("aria-expanded", "true");

      fireEvent.click(button);
      expect(button).toHaveAttribute("aria-expanded", "false");
    });

    it('header button has type="button"', () => {
      render(
        <CollapsibleSection title="Section">
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByRole("button")).toHaveAttribute("type", "button");
    });
  });

  describe("edge cases", () => {
    it("renders with empty children", () => {
      render(<CollapsibleSection title="Empty">{null}</CollapsibleSection>);
      expect(screen.getByText("Empty")).toBeInTheDocument();
    });

    it("renders with multiple children", () => {
      render(
        <CollapsibleSection title="Multi">
          <p>First</p>
          <p>Second</p>
          <p>Third</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("First")).toBeInTheDocument();
      expect(screen.getByText("Second")).toBeInTheDocument();
      expect(screen.getByText("Third")).toBeInTheDocument();
    });

    it("renders with large count", () => {
      render(
        <CollapsibleSection title="Items" count={99999}>
          <p>Content</p>
        </CollapsibleSection>,
      );
      expect(screen.getByText("(99999)")).toBeInTheDocument();
    });
  });
});
