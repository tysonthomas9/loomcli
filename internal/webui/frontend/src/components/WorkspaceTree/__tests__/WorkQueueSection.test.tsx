/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkQueueSection component.
 * Verifies rendering of queue counts, expand/collapse toggling,
 * localStorage persistence, and data-highlight attributes.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { WorkQueueCounts } from "../WorkQueueSection";
import { WorkQueueSection } from "../WorkQueueSection";

const STORAGE_KEY = "workspace-tree-work-queue-expanded";

function makeCounts(
  overrides: Partial<WorkQueueCounts> = {},
): WorkQueueCounts {
  return {
    backlog: 0,
    open: 0,
    blocked: 0,
    inProgress: 0,
    needsReview: 0,
    done: 0,
    ...overrides,
  };
}

describe("WorkQueueSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  describe("basic rendering", () => {
    it("renders the Work Queue header", () => {
      render(<WorkQueueSection counts={makeCounts()} />);

      expect(screen.getByText("Work Queue")).toBeInTheDocument();
    });

    it("is expanded by default", () => {
      render(
        <WorkQueueSection counts={makeCounts({ backlog: 5, open: 3 })} />,
      );

      expect(screen.getByText("Backlog")).toBeInTheDocument();
      expect(screen.getByText("Open")).toBeInTheDocument();
      expect(screen.getByText("5")).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
    });

    it("renders all six queue categories", () => {
      render(
        <WorkQueueSection
          counts={makeCounts({
            backlog: 1,
            open: 2,
            blocked: 3,
            inProgress: 4,
            needsReview: 5,
            done: 6,
          })}
        />,
      );

      expect(screen.getByText("Backlog")).toBeInTheDocument();
      expect(screen.getByText("Open")).toBeInTheDocument();
      expect(screen.getByText("Blocked")).toBeInTheDocument();
      expect(screen.getByText("In Progress")).toBeInTheDocument();
      expect(screen.getByText("Needs Review")).toBeInTheDocument();
      expect(screen.getByText("Done")).toBeInTheDocument();

      expect(screen.getByText("1")).toBeInTheDocument();
      expect(screen.getByText("2")).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
      expect(screen.getByText("4")).toBeInTheDocument();
      expect(screen.getByText("5")).toBeInTheDocument();
      expect(screen.getByText("6")).toBeInTheDocument();
    });
  });

  describe("collapse/expand toggle", () => {
    it("collapses content when header is clicked", () => {
      render(
        <WorkQueueSection counts={makeCounts({ backlog: 5 })} />,
      );

      expect(screen.getByText("Backlog")).toBeInTheDocument();

      fireEvent.click(screen.getByText("Work Queue"));

      expect(screen.queryByText("Backlog")).not.toBeInTheDocument();
    });

    it("expands content when collapsed header is clicked again", () => {
      render(
        <WorkQueueSection counts={makeCounts({ backlog: 5 })} />,
      );

      // Collapse
      fireEvent.click(screen.getByText("Work Queue"));
      expect(screen.queryByText("Backlog")).not.toBeInTheDocument();

      // Expand
      fireEvent.click(screen.getByText("Work Queue"));
      expect(screen.getByText("Backlog")).toBeInTheDocument();
    });

    it("toggles on Enter key press", () => {
      render(
        <WorkQueueSection counts={makeCounts({ open: 2 })} />,
      );

      const header = screen.getByRole("button");
      fireEvent.keyDown(header, { key: "Enter" });
      expect(screen.queryByText("Open")).not.toBeInTheDocument();
    });

    it("does not toggle on other key presses", () => {
      render(
        <WorkQueueSection counts={makeCounts({ open: 2 })} />,
      );

      const header = screen.getByRole("button");
      fireEvent.keyDown(header, { key: "a" });
      // Should remain expanded
      expect(screen.getByText("Open")).toBeInTheDocument();
    });

    it("shows down arrow when expanded", () => {
      render(<WorkQueueSection counts={makeCounts()} />);

      // \u25BE = down-pointing triangle
      expect(screen.getByText("\u25BE")).toBeInTheDocument();
    });

    it("shows right arrow when collapsed", () => {
      render(<WorkQueueSection counts={makeCounts()} />);

      fireEvent.click(screen.getByText("Work Queue"));

      // \u25B8 = right-pointing triangle
      expect(screen.getByText("\u25B8")).toBeInTheDocument();
    });
  });

  describe("localStorage persistence", () => {
    it("persists expanded state to localStorage", () => {
      render(<WorkQueueSection counts={makeCounts()} />);

      // Default is expanded = "true"
      expect(localStorage.getItem(STORAGE_KEY)).toBe("true");
    });

    it("persists collapsed state to localStorage", () => {
      render(<WorkQueueSection counts={makeCounts()} />);

      fireEvent.click(screen.getByText("Work Queue"));

      expect(localStorage.getItem(STORAGE_KEY)).toBe("false");
    });

    it("reads initial state from localStorage", () => {
      localStorage.setItem(STORAGE_KEY, "false");

      render(
        <WorkQueueSection counts={makeCounts({ backlog: 5 })} />,
      );

      // Should start collapsed
      expect(screen.queryByText("Backlog")).not.toBeInTheDocument();
    });

    it("defaults to expanded when localStorage value is missing", () => {
      render(
        <WorkQueueSection counts={makeCounts({ backlog: 5 })} />,
      );

      expect(screen.getByText("Backlog")).toBeInTheDocument();
    });
  });

  describe("data-highlight attribute", () => {
    it("sets data-highlight=true on count when value > 0 for backlog", () => {
      const { container } = render(
        <WorkQueueSection counts={makeCounts({ backlog: 3 })} />,
      );

      const backlogCount = container.querySelector(
        '[data-highlight="true"]',
      );
      expect(backlogCount).toBeInTheDocument();
    });

    it("sets data-highlight=false on count when value is 0 for backlog", () => {
      const { container } = render(
        <WorkQueueSection counts={makeCounts({ backlog: 0 })} />,
      );

      const counts = container.querySelectorAll(
        '[data-highlight="false"]',
      );
      // backlog, open, inProgress, needsReview should all be false (0 values)
      expect(counts.length).toBeGreaterThanOrEqual(1);
    });

    it("blocked and done counts do not have data-highlight attribute", () => {
      const { container } = render(
        <WorkQueueSection counts={makeCounts({ blocked: 5, done: 10 })} />,
      );

      // Find the blocked count (value "5") and done count (value "10")
      const blockedLabel = screen.getByText("Blocked");
      const doneLabel = screen.getByText("Done");

      // Get sibling count spans
      const blockedCount =
        blockedLabel.parentElement?.querySelector('[class*="queueCount"]');
      const doneCount =
        doneLabel.parentElement?.querySelector('[class*="queueCount"]');

      // These should NOT have data-highlight
      expect(blockedCount).not.toHaveAttribute("data-highlight");
      expect(doneCount).not.toHaveAttribute("data-highlight");
    });
  });

  describe("zero counts", () => {
    it("displays all zeros correctly", () => {
      render(<WorkQueueSection counts={makeCounts()} />);

      // All counts should show "0"
      const zeros = screen.getAllByText("0");
      expect(zeros.length).toBe(6);
    });
  });
});
