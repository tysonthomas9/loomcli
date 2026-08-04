/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ErrorIndicators component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { ErrorIndicators } from "../ErrorIndicators";

const _defaultProps = {
  errorRatePct: 8.5,
  restartCount24h: 3,
  restartsByAgent: { alpha: 2, beta: 1 } as Record<string, number>,
};

describe("ErrorIndicators", () => {
  describe("severity badges", () => {
    it('renders error rate badge with severity "ok" when rate <= 5', () => {
      render(
        <ErrorIndicators
          errorRatePct={3.0}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("3.0% error rate");
      expect(badge).toHaveAttribute("data-severity", "ok");
    });

    it('renders error rate badge with severity "warning" when rate > 5 and <= 15', () => {
      render(
        <ErrorIndicators
          errorRatePct={12.0}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("12.0% error rate");
      expect(badge).toHaveAttribute("data-severity", "warning");
    });

    it('renders error rate badge with severity "critical" when rate > 15', () => {
      render(
        <ErrorIndicators
          errorRatePct={20.0}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("20.0% error rate");
      expect(badge).toHaveAttribute("data-severity", "critical");
    });

    it('renders restart count badge with severity "warning" when count > 0', () => {
      render(
        <ErrorIndicators
          errorRatePct={1.0}
          restartCount24h={5}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("5 restarts (24h)");
      expect(badge).toHaveAttribute("data-severity", "warning");
    });

    it('renders restart count badge with severity "ok" when count is 0 but errorRate > 0', () => {
      render(
        <ErrorIndicators
          errorRatePct={2.0}
          restartCount24h={0}
          restartsByAgent={{}}
        />,
      );

      // hasIssues is true because errorRatePct > 0
      const badge = screen.getByText("0 restarts (24h)");
      expect(badge).toHaveAttribute("data-severity", "ok");
    });

    it('uses singular "restart" when count is 1', () => {
      render(
        <ErrorIndicators
          errorRatePct={1.0}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      expect(screen.getByText("1 restart (24h)")).toBeInTheDocument();
    });

    it('uses plural "restarts" when count is not 1', () => {
      render(
        <ErrorIndicators
          errorRatePct={1.0}
          restartCount24h={3}
          restartsByAgent={{}}
        />,
      );

      expect(screen.getByText("3 restarts (24h)")).toBeInTheDocument();
    });
  });

  describe("severity boundary values", () => {
    it('severity is "ok" at exactly 5%', () => {
      render(
        <ErrorIndicators
          errorRatePct={5.0}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("5.0% error rate");
      expect(badge).toHaveAttribute("data-severity", "ok");
    });

    it('severity is "warning" at 5.1%', () => {
      render(
        <ErrorIndicators
          errorRatePct={5.1}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("5.1% error rate");
      expect(badge).toHaveAttribute("data-severity", "warning");
    });

    it('severity is "warning" at exactly 15%', () => {
      render(
        <ErrorIndicators
          errorRatePct={15.0}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("15.0% error rate");
      expect(badge).toHaveAttribute("data-severity", "warning");
    });

    it('severity is "critical" at 15.1%', () => {
      render(
        <ErrorIndicators
          errorRatePct={15.1}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const badge = screen.getByText("15.1% error rate");
      expect(badge).toHaveAttribute("data-severity", "critical");
    });
  });

  describe("agent restart list", () => {
    it("renders agents sorted by restart count descending", () => {
      const restartsByAgent = { alpha: 1, beta: 5, gamma: 3 };

      const { container } = render(
        <ErrorIndicators
          errorRatePct={10}
          restartCount24h={9}
          restartsByAgent={restartsByAgent}
        />,
      );

      const agentNames = container.querySelectorAll('[class*="agentName"]');
      expect(agentNames).toHaveLength(3);
      expect(agentNames[0].textContent).toBe("beta");
      expect(agentNames[1].textContent).toBe("gamma");
      expect(agentNames[2].textContent).toBe("alpha");
    });

    it("filters out agents with 0 restarts", () => {
      const restartsByAgent = { alpha: 3, beta: 0, gamma: 1 };

      const { container } = render(
        <ErrorIndicators
          errorRatePct={5}
          restartCount24h={4}
          restartsByAgent={restartsByAgent}
        />,
      );

      const agentNames = container.querySelectorAll('[class*="agentName"]');
      expect(agentNames).toHaveLength(2);
      expect(screen.queryByText("beta")).not.toBeInTheDocument();
    });

    it("does not render agent list when restartsByAgent is empty", () => {
      const { container } = render(
        <ErrorIndicators
          errorRatePct={5}
          restartCount24h={1}
          restartsByAgent={{}}
        />,
      );

      const agentList = container.querySelector('[class*="agentList"]');
      expect(agentList).not.toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty state when no errors and no restarts", () => {
      render(
        <ErrorIndicators
          errorRatePct={0}
          restartCount24h={0}
          restartsByAgent={{}}
        />,
      );

      expect(
        screen.getByText("No errors or restarts in the last 24 hours"),
      ).toBeInTheDocument();
    });

    it("does not show empty state when only errorRate > 0", () => {
      render(
        <ErrorIndicators
          errorRatePct={2.0}
          restartCount24h={0}
          restartsByAgent={{}}
        />,
      );

      expect(
        screen.queryByText("No errors or restarts in the last 24 hours"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("2.0% error rate")).toBeInTheDocument();
    });

    it("does not show empty state when only restartCount > 0", () => {
      render(
        <ErrorIndicators
          errorRatePct={0}
          restartCount24h={2}
          restartsByAgent={{}}
        />,
      );

      expect(
        screen.queryByText("No errors or restarts in the last 24 hours"),
      ).not.toBeInTheDocument();
      expect(screen.getByText("2 restarts (24h)")).toBeInTheDocument();
    });
  });
});
