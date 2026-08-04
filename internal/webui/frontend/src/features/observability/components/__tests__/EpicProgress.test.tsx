/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for EpicProgress component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { EpicProgress } from "../EpicProgress";

describe("EpicProgress", () => {
  describe("rendering epics", () => {
    it("renders all epics with IDs and counts", () => {
      const tasksByEpic = { "epic-auth": 10, "epic-payments": 5, "epic-ui": 3 };

      render(<EpicProgress tasksByEpic={tasksByEpic} />);

      expect(screen.getByText("epic-auth")).toBeInTheDocument();
      expect(screen.getByText("10")).toBeInTheDocument();
      expect(screen.getByText("epic-payments")).toBeInTheDocument();
      expect(screen.getByText("5")).toBeInTheDocument();
      expect(screen.getByText("epic-ui")).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
    });

    it("renders epics sorted by count descending", () => {
      const tasksByEpic = { small: 2, large: 20, medium: 10 };

      const { container } = render(<EpicProgress tasksByEpic={tasksByEpic} />);

      const epicIds = container.querySelectorAll('[class*="epicId"]');
      expect(epicIds).toHaveLength(3);
      expect(epicIds[0].textContent).toBe("large");
      expect(epicIds[1].textContent).toBe("medium");
      expect(epicIds[2].textContent).toBe("small");
    });
  });

  describe("bar widths", () => {
    it("sets bar width proportional to max count", () => {
      const tasksByEpic = { top: 10, half: 5 };

      const { container } = render(<EpicProgress tasksByEpic={tasksByEpic} />);

      const barFills = container.querySelectorAll('[class*="barFill"]');
      expect(barFills).toHaveLength(2);
      // top = 10/10 = 100%, half = 5/10 = 50%
      expect((barFills[0] as HTMLElement).style.width).toBe("100%");
      expect((barFills[1] as HTMLElement).style.width).toBe("50%");
    });
  });

  describe("empty state", () => {
    it("shows empty state when tasksByEpic is empty object", () => {
      render(<EpicProgress tasksByEpic={{}} />);

      expect(screen.getByText("No epic data available")).toBeInTheDocument();
    });
  });

  describe("edge cases", () => {
    it("handles single epic", () => {
      const tasksByEpic = { "only-epic": 7 };

      render(<EpicProgress tasksByEpic={tasksByEpic} />);

      expect(screen.getByText("only-epic")).toBeInTheDocument();
      expect(screen.getByText("7")).toBeInTheDocument();
    });

    it("handles many epics", () => {
      const tasksByEpic: Record<string, number> = {};
      for (let i = 0; i < 15; i++) {
        tasksByEpic[`epic-${i}`] = 15 - i;
      }

      render(<EpicProgress tasksByEpic={tasksByEpic} />);

      expect(screen.getByText("epic-0")).toBeInTheDocument();
      expect(screen.getByText("epic-14")).toBeInTheDocument();
    });

    it("handles epics with equal counts", () => {
      const tasksByEpic = { alpha: 5, beta: 5, gamma: 5 };

      const { container } = render(<EpicProgress tasksByEpic={tasksByEpic} />);

      const barFills = container.querySelectorAll('[class*="barFill"]');
      // All should be 100% since they're all equal to max
      for (const fill of barFills) {
        expect((fill as HTMLElement).style.width).toBe("100%");
      }
    });
  });
});
