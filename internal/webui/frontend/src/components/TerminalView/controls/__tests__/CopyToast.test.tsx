/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CopyToast component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { CopyToast } from "../CopyToast";

// Mock CSS module
vi.mock("../CopyToast.module.css", () => ({
  default: {
    toast: "toast",
  },
}));

describe("CopyToast", () => {
  describe("rendering", () => {
    it("renders 'Copied!' text when visible is true", () => {
      render(<CopyToast visible={true} />);

      expect(screen.getByText("Copied!")).toBeInTheDocument();
    });

    it("returns null when visible is false", () => {
      const { container } = render(<CopyToast visible={false} />);

      expect(container.innerHTML).toBe("");
    });

    it("does not render 'Copied!' text when visible is false", () => {
      render(<CopyToast visible={false} />);

      expect(screen.queryByText("Copied!")).not.toBeInTheDocument();
    });
  });

  describe("accessibility", () => {
    it('has role="status" for screen readers', () => {
      render(<CopyToast visible={true} />);

      expect(screen.getByRole("status")).toBeInTheDocument();
    });

    it('has aria-live="polite" for non-intrusive announcement', () => {
      render(<CopyToast visible={true} />);

      const toast = screen.getByRole("status");
      expect(toast).toHaveAttribute("aria-live", "polite");
    });
  });

  describe("visibility transitions", () => {
    it("appears when visible changes from false to true", () => {
      const { rerender } = render(<CopyToast visible={false} />);

      expect(screen.queryByText("Copied!")).not.toBeInTheDocument();

      rerender(<CopyToast visible={true} />);

      expect(screen.getByText("Copied!")).toBeInTheDocument();
    });

    it("disappears when visible changes from true to false", () => {
      const { rerender } = render(<CopyToast visible={true} />);

      expect(screen.getByText("Copied!")).toBeInTheDocument();

      rerender(<CopyToast visible={false} />);

      expect(screen.queryByText("Copied!")).not.toBeInTheDocument();
    });
  });
});
