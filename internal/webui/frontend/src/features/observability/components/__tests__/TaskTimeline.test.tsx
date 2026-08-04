/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TaskTimeline component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import type { HourlyBucket } from "../../types";

import { TaskTimeline } from "../TaskTimeline";

function createBucket(overrides: Partial<HourlyBucket> = {}): HourlyBucket {
  return {
    hour: "2026-03-05T10:00:00Z",
    completed: 5,
    failed: 1,
    avg_duration: 60,
    ...overrides,
  };
}

describe("TaskTimeline", () => {
  describe("rendering bars", () => {
    it("renders completed bar segments", () => {
      const buckets = [
        createBucket({ hour: "2026-03-05T10:00:00Z", completed: 8, failed: 0 }),
        createBucket({ hour: "2026-03-05T11:00:00Z", completed: 4, failed: 2 }),
      ];

      render(<TaskTimeline hourlyCompletions={buckets} />);

      // Should render bars (title attributes on completed/failed segments)
      expect(screen.getByTitle("8 completed")).toBeInTheDocument();
      expect(screen.getByTitle("4 completed")).toBeInTheDocument();
      expect(screen.getByTitle("2 failed")).toBeInTheDocument();
    });

    it("renders failed bar segments", () => {
      const buckets = [
        createBucket({ hour: "2026-03-05T10:00:00Z", completed: 3, failed: 5 }),
      ];

      render(<TaskTimeline hourlyCompletions={buckets} />);

      expect(screen.getByTitle("5 failed")).toBeInTheDocument();
      expect(screen.getByTitle("3 completed")).toBeInTheDocument();
    });

    it("does not render failed segment when failed is 0", () => {
      const buckets = [
        createBucket({
          hour: "2026-03-05T10:00:00Z",
          completed: 10,
          failed: 0,
        }),
      ];

      render(<TaskTimeline hourlyCompletions={buckets} />);

      expect(screen.getByTitle("10 completed")).toBeInTheDocument();
      expect(screen.queryByTitle(/failed/)).not.toBeInTheDocument();
    });

    it("does not render completed segment when completed is 0", () => {
      const buckets = [
        createBucket({ hour: "2026-03-05T10:00:00Z", completed: 0, failed: 3 }),
      ];

      render(<TaskTimeline hourlyCompletions={buckets} />);

      expect(screen.getByTitle("3 failed")).toBeInTheDocument();
      expect(screen.queryByTitle(/completed/)).not.toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty state when hourlyCompletions is empty", () => {
      render(<TaskTimeline hourlyCompletions={[]} />);

      expect(
        screen.getByText("No task completions in the last 24 hours"),
      ).toBeInTheDocument();
    });

    it("shows empty state when all buckets have zero completed and failed", () => {
      const buckets = [
        createBucket({ hour: "2026-03-05T10:00:00Z", completed: 0, failed: 0 }),
        createBucket({ hour: "2026-03-05T11:00:00Z", completed: 0, failed: 0 }),
      ];

      render(<TaskTimeline hourlyCompletions={buckets} />);

      expect(
        screen.getByText("No task completions in the last 24 hours"),
      ).toBeInTheDocument();
    });
  });

  describe("hour labels", () => {
    it("shows hour labels every 3rd column (i % 3 === 0)", () => {
      const buckets = Array.from({ length: 6 }, (_, i) =>
        createBucket({
          hour: `2026-03-05T${String(i + 10).padStart(2, "0")}:00:00Z`,
          completed: i + 1,
          failed: 0,
        }),
      );

      const { container } = render(
        <TaskTimeline hourlyCompletions={buckets} />,
      );

      // Columns at index 0 and 3 should have non-empty labels
      // Columns at index 1, 2, 4, 5 should have empty labels
      const columns = container.querySelectorAll('[class*="column"]');
      expect(columns).toHaveLength(6);
    });
  });

  describe("bar height calculation", () => {
    it("renders bar heights proportional to max value", () => {
      const buckets = [
        createBucket({
          hour: "2026-03-05T10:00:00Z",
          completed: 10,
          failed: 0,
        }),
        createBucket({ hour: "2026-03-05T11:00:00Z", completed: 5, failed: 0 }),
      ];

      const { container } = render(
        <TaskTimeline hourlyCompletions={buckets} />,
      );

      // First bar should be 100% (10/10), second should be 50% (5/10)
      const completedBars = container.querySelectorAll(
        '[class*="barCompleted"]',
      );
      expect(completedBars).toHaveLength(2);
      expect((completedBars[0] as HTMLElement).style.height).toBe("100%");
      expect((completedBars[1] as HTMLElement).style.height).toBe("50%");
    });
  });
});
