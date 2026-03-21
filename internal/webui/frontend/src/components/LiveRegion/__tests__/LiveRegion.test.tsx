/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for LiveRegion component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { LiveRegion } from "../LiveRegion";

describe("LiveRegion", () => {
  describe("rendering", () => {
    it("renders the polite live region", () => {
      render(<LiveRegion />);

      expect(screen.getByTestId("live-region-polite")).toBeInTheDocument();
    });

    it("renders the assertive live region", () => {
      render(<LiveRegion />);

      expect(screen.getByTestId("live-region-assertive")).toBeInTheDocument();
    });

    it("polite region has role='status'", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveAttribute("role", "status");
    });

    it("polite region has aria-live='polite'", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveAttribute("aria-live", "polite");
    });

    it("assertive region has aria-live='assertive'", () => {
      render(<LiveRegion />);

      const assertive = screen.getByTestId("live-region-assertive");
      expect(assertive).toHaveAttribute("aria-live", "assertive");
    });

    it("polite region has aria-atomic='true'", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveAttribute("aria-atomic", "true");
    });

    it("assertive region has aria-atomic='true'", () => {
      render(<LiveRegion />);

      const assertive = screen.getByTestId("live-region-assertive");
      expect(assertive).toHaveAttribute("aria-atomic", "true");
    });
  });

  describe("visual hiding", () => {
    it("polite region is visually hidden via inline style position:absolute", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveStyle({ position: "absolute" });
    });

    it("polite region has width:1px for visual hiding", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveStyle({ width: "1px" });
    });

    it("polite region has height:1px for visual hiding", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveStyle({ height: "1px" });
    });

    it("polite region has overflow:hidden for visual hiding", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveStyle({ overflow: "hidden" });
    });

    it("assertive region is visually hidden via inline style position:absolute", () => {
      render(<LiveRegion />);

      const assertive = screen.getByTestId("live-region-assertive");
      expect(assertive).toHaveStyle({ position: "absolute" });
    });

    it("assertive region has width:1px for visual hiding", () => {
      render(<LiveRegion />);

      const assertive = screen.getByTestId("live-region-assertive");
      expect(assertive).toHaveStyle({ width: "1px" });
    });

    it("assertive region has height:1px for visual hiding", () => {
      render(<LiveRegion />);

      const assertive = screen.getByTestId("live-region-assertive");
      expect(assertive).toHaveStyle({ height: "1px" });
    });
  });

  describe("initial state", () => {
    it("polite region starts empty", () => {
      render(<LiveRegion />);

      const polite = screen.getByTestId("live-region-polite");
      expect(polite).toHaveTextContent("");
    });

    it("assertive region starts empty", () => {
      render(<LiveRegion />);

      const assertive = screen.getByTestId("live-region-assertive");
      expect(assertive).toHaveTextContent("");
    });
  });
});
