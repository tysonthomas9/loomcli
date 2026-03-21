/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AuthorAvatar component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { AuthorAvatar } from "../AuthorAvatar";

describe("AuthorAvatar", () => {
  describe("rendering", () => {
    it("renders with data-testid", () => {
      render(<AuthorAvatar name="Alice" />);
      expect(screen.getByTestId("author-avatar")).toBeInTheDocument();
    });

    it("shows the first letter as initial, uppercased", () => {
      render(<AuthorAvatar name="alice" />);
      expect(screen.getByTestId("author-avatar")).toHaveTextContent("A");
    });

    it('shows "?" when name is empty', () => {
      render(<AuthorAvatar name="" />);
      expect(screen.getByTestId("author-avatar")).toHaveTextContent("?");
    });

    it("uses name as title attribute", () => {
      render(<AuthorAvatar name="Bob Smith" />);
      expect(screen.getByTestId("author-avatar")).toHaveAttribute(
        "title",
        "Bob Smith",
      );
    });
  });

  describe("color consistency", () => {
    it("returns the same color for the same name", () => {
      const { unmount } = render(<AuthorAvatar name="Alice" />);
      const color1 = screen.getByTestId("author-avatar").style.backgroundColor;
      unmount();

      render(<AuthorAvatar name="Alice" />);
      const color2 = screen.getByTestId("author-avatar").style.backgroundColor;

      expect(color1).toBe(color2);
    });

    it("returns different colors for different names", () => {
      // Use names that are known to hash differently
      const { unmount } = render(<AuthorAvatar name="Alice" />);
      const color1 = screen.getByTestId("author-avatar").style.backgroundColor;
      unmount();

      render(<AuthorAvatar name="Zephyr" />);
      const color2 = screen.getByTestId("author-avatar").style.backgroundColor;

      // While not guaranteed for all pairs, these particular names should differ
      expect(color1).not.toBe(color2);
    });

    it("sets a backgroundColor style", () => {
      render(<AuthorAvatar name="TestUser" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.style.backgroundColor).toBeTruthy();
    });
  });

  describe("human vs agent detection", () => {
    it("applies human shape class for regular names", () => {
      render(<AuthorAvatar name="Jane Doe" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/human/);
    });

    it('applies agent shape class for names containing "bot"', () => {
      render(<AuthorAvatar name="review-bot" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/agent/);
    });

    it('applies agent shape class for names containing "agent"', () => {
      render(<AuthorAvatar name="my-agent" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/agent/);
    });

    it('applies agent shape class for names containing "claude"', () => {
      render(<AuthorAvatar name="Claude" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/agent/);
    });

    it('applies agent shape class for "web-ui"', () => {
      render(<AuthorAvatar name="web-ui" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/agent/);
    });

    it("detection is case-insensitive", () => {
      render(<AuthorAvatar name="MyBOT" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/agent/);
    });

    it("allows explicit isAgent override to true", () => {
      render(<AuthorAvatar name="Jane Doe" isAgent={true} />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/agent/);
    });

    it("allows explicit isAgent override to false", () => {
      render(<AuthorAvatar name="review-bot" isAgent={false} />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/human/);
    });
  });

  describe("size variants", () => {
    it("defaults to standard size", () => {
      render(<AuthorAvatar name="Alice" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/standard/);
    });

    it('applies compact size class when size="compact"', () => {
      render(<AuthorAvatar name="Alice" size="compact" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/compact/);
    });

    it('applies standard size class when size="standard"', () => {
      render(<AuthorAvatar name="Alice" size="standard" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/standard/);
    });
  });

  describe("avatar base class", () => {
    it("has the avatar base class", () => {
      render(<AuthorAvatar name="Alice" />);
      const avatar = screen.getByTestId("author-avatar");
      expect(avatar.className).toMatch(/avatar/);
    });
  });
});
