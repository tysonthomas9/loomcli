/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BlockedCell component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { BlockedCell } from "../BlockedCell";

describe("BlockedCell", () => {
  describe("rendering", () => {
    it("renders dash when blockedByCount is 0", () => {
      render(<BlockedCell blockedByCount={0} />);
      expect(screen.getByText("—")).toBeInTheDocument();
    });

    it("renders badge with count when blockedByCount > 0", () => {
      render(
        <BlockedCell
          blockedByCount={3}
          blockedBy={["loom-1", "loom-2", "loom-3"]}
        />,
      );
      expect(screen.getByText("3")).toBeInTheDocument();
      expect(screen.getByText("⛔")).toBeInTheDocument();
    });

    it("renders 99+ for counts over 99", () => {
      render(<BlockedCell blockedByCount={150} />);
      expect(screen.getByText("99+")).toBeInTheDocument();
    });

    it("has correct aria-label when blocked", () => {
      render(
        <BlockedCell blockedByCount={2} blockedBy={["loom-1", "loom-2"]} />,
      );
      expect(screen.getByLabelText("Blocked by 2 issues")).toBeInTheDocument();
    });

    it("has singular aria-label when blocked by 1 issue", () => {
      render(<BlockedCell blockedByCount={1} blockedBy={["loom-1"]} />);
      expect(screen.getByLabelText("Blocked by 1 issue")).toBeInTheDocument();
    });
  });

  describe("tooltip", () => {
    it("shows blocker IDs in title attribute", () => {
      render(
        <BlockedCell blockedByCount={2} blockedBy={["loom-abc", "loom-def"]} />,
      );
      const button = screen.getByRole("button");
      expect(button.title).toContain("loom-abc");
      expect(button.title).toContain("loom-def");
    });

    it('truncates tooltip at 5 blockers with "and N more"', () => {
      const blockedBy = [
        "loom-1",
        "loom-2",
        "loom-3",
        "loom-4",
        "loom-5",
        "loom-6",
        "loom-7",
      ];
      render(<BlockedCell blockedByCount={7} blockedBy={blockedBy} />);
      const button = screen.getByRole("button");
      expect(button.title).toContain("loom-1");
      expect(button.title).toContain("loom-5");
      expect(button.title).toContain("and 2 more...");
      expect(button.title).not.toContain("loom-6");
    });

    it('does not show "and more" when 5 or fewer blockers', () => {
      const blockedBy = ["loom-1", "loom-2", "loom-3"];
      render(<BlockedCell blockedByCount={3} blockedBy={blockedBy} />);
      const button = screen.getByRole("button");
      expect(button.title).not.toContain("and");
    });
  });

  describe("click handling", () => {
    it("calls onClick when button is clicked", () => {
      const handleClick = vi.fn();
      render(
        <BlockedCell
          blockedByCount={1}
          blockedBy={["loom-1"]}
          onClick={handleClick}
        />,
      );

      fireEvent.click(screen.getByRole("button"));
      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it("stops propagation on click", () => {
      const parentClick = vi.fn();
      const handleClick = vi.fn();

      render(
        <div onClick={parentClick}>
          <BlockedCell
            blockedByCount={1}
            blockedBy={["loom-1"]}
            onClick={handleClick}
          />
        </div>,
      );

      fireEvent.click(screen.getByRole("button"));
      expect(handleClick).toHaveBeenCalledTimes(1);
      expect(parentClick).not.toHaveBeenCalled();
    });

    it("does not throw when onClick is not provided", () => {
      render(<BlockedCell blockedByCount={1} blockedBy={["loom-1"]} />);

      expect(() => fireEvent.click(screen.getByRole("button"))).not.toThrow();
    });
  });

  describe("styling", () => {
    it("has --none modifier class when not blocked", () => {
      render(<BlockedCell blockedByCount={0} />);
      const element = screen.getByText("—");
      expect(element.className).toContain("issue-table__blocked--none");
    });

    it("has --active modifier class when blocked", () => {
      render(<BlockedCell blockedByCount={1} blockedBy={["loom-1"]} />);
      const button = screen.getByRole("button");
      expect(button.className).toContain("issue-table__blocked--active");
    });
  });
});
