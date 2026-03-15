/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RepoBadge component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { getAvatarColor } from "@/utils/colorUtils";

import { RepoBadge } from "../RepoBadge";

/** Convert a 7-char hex color (#RRGGBB) to the rgb() string jsdom uses. */
function hexToRgb(hex: string): string {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  return `rgb(${r}, ${g}, ${b})`;
}

describe("RepoBadge", () => {
  describe("rendering", () => {
    it("renders the repo name text", () => {
      render(<RepoBadge repoName="frontend" />);

      expect(screen.getByText("frontend")).toBeInTheDocument();
    });

    it("renders as a span element", () => {
      render(<RepoBadge repoName="backend" />);

      const badge = screen.getByLabelText("Repository: backend");
      expect(badge.tagName).toBe("SPAN");
    });

    it("returns null for empty repoName", () => {
      const { container } = render(<RepoBadge repoName="" />);

      expect(container.firstChild).toBeNull();
    });
  });

  describe("styling", () => {
    it("applies background color derived from getAvatarColor", () => {
      render(<RepoBadge repoName="my-repo" />);

      const badge = screen.getByLabelText("Repository: my-repo");
      const expectedColor = hexToRgb(getAvatarColor("my-repo"));
      expect(badge.style.backgroundColor).toBe(expectedColor);
    });

    it("always uses dark text color (#1f2937)", () => {
      render(<RepoBadge repoName="some-repo" />);

      const badge = screen.getByLabelText("Repository: some-repo");
      expect(badge.style.color).toBe(hexToRgb("#1f2937"));
    });

    it("uses deterministic background color (same name produces same color)", () => {
      const { unmount } = render(<RepoBadge repoName="atlas" />);
      const color1 =
        screen.getByLabelText("Repository: atlas").style.backgroundColor;
      unmount();

      render(<RepoBadge repoName="atlas" />);
      const color2 =
        screen.getByLabelText("Repository: atlas").style.backgroundColor;

      expect(color1).toBe(color2);
    });
  });

  describe("accessibility", () => {
    it("has correct aria-label", () => {
      render(<RepoBadge repoName="loomcli" />);

      expect(screen.getByLabelText("Repository: loomcli")).toBeInTheDocument();
    });

    it("has title attribute set to repoName", () => {
      render(<RepoBadge repoName="loomcli" />);

      expect(screen.getByTitle("loomcli")).toBeInTheDocument();
    });
  });

  describe("className prop", () => {
    it("applies custom className when provided", () => {
      render(<RepoBadge repoName="myrepo" className="custom-class" />);

      const badge = screen.getByLabelText("Repository: myrepo");
      expect(badge).toHaveClass("custom-class");
    });

    it("includes base repoBadge class", () => {
      render(<RepoBadge repoName="myrepo" />);

      const badge = screen.getByLabelText("Repository: myrepo");
      expect(badge.className).toContain("repoBadge");
    });

    it("works without className prop", () => {
      render(<RepoBadge repoName="myrepo" />);

      const badge = screen.getByLabelText("Repository: myrepo");
      expect(badge).toBeInTheDocument();
    });
  });
});
