/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for VisuallyHidden component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { expectNoA11yViolations } from "@/test-utils/a11y-helpers";
import { VisuallyHidden } from "../VisuallyHidden";

describe("VisuallyHidden", () => {
  describe("rendering", () => {
    it("renders children in the DOM", () => {
      render(<VisuallyHidden>Screen reader text</VisuallyHidden>);

      expect(screen.getByText("Screen reader text")).toBeInTheDocument();
    });

    it("renders children with the visuallyHidden CSS module class", () => {
      render(<VisuallyHidden>Hidden content</VisuallyHidden>);

      const el = screen.getByText("Hidden content");
      expect(el.className).toMatch(/visuallyHidden/);
    });

    it("defaults to a span element", () => {
      const { container } = render(
        <VisuallyHidden>Span content</VisuallyHidden>,
      );

      const el = container.querySelector("span");
      expect(el).toBeInTheDocument();
      expect(el).toHaveTextContent("Span content");
    });

    it("renders as a div when as='div' is passed", () => {
      const { container } = render(
        <VisuallyHidden as="div">Div content</VisuallyHidden>,
      );

      const el = container.querySelector("div");
      expect(el).toBeInTheDocument();
      expect(el).toHaveTextContent("Div content");
    });

    it("does not render a div when as is not specified", () => {
      const { container } = render(<VisuallyHidden>Span only</VisuallyHidden>);

      expect(container.querySelector("div")).not.toBeInTheDocument();
      expect(container.querySelector("span")).toBeInTheDocument();
    });

    it("renders multiple children", () => {
      render(
        <VisuallyHidden>
          <span>child one</span>
          <span>child two</span>
        </VisuallyHidden>,
      );

      expect(screen.getByText("child one")).toBeInTheDocument();
      expect(screen.getByText("child two")).toBeInTheDocument();
    });
  });

  describe("accessibility", () => {
    it("has no axe violations when rendering a span", async () => {
      const { container } = render(
        <VisuallyHidden>Accessible label text</VisuallyHidden>,
      );

      await expectNoA11yViolations(container);
    });

    it("has no axe violations when rendering a div", async () => {
      const { container } = render(
        <VisuallyHidden as="div">Accessible label text</VisuallyHidden>,
      );

      await expectNoA11yViolations(container);
    });
  });
});
