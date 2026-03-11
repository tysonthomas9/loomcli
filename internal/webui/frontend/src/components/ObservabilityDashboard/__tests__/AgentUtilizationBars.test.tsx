/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AgentUtilizationBars component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import "@testing-library/jest-dom";

import { AgentUtilizationBars } from "../AgentUtilizationBars";

describe("AgentUtilizationBars", () => {
  describe("rendering agents", () => {
    it("renders all agents with names and percentages", () => {
      const utilization = { alpha: 0.8, beta: 0.5, gamma: 0.2 };

      render(<AgentUtilizationBars utilization={utilization} />);

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("80%")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
      expect(screen.getByText("50%")).toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
      expect(screen.getByText("20%")).toBeInTheDocument();
    });

    it("renders agents sorted alphabetically by name", () => {
      const utilization = { zulu: 0.9, alpha: 0.1, mike: 0.5 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const agentNames = container.querySelectorAll('[class*="agentName"]');
      expect(agentNames).toHaveLength(3);
      expect(agentNames[0].textContent).toBe("alpha");
      expect(agentNames[1].textContent).toBe("mike");
      expect(agentNames[2].textContent).toBe("zulu");
    });
  });

  describe("bar widths", () => {
    it("sets correct bar width percentages", () => {
      const utilization = { agent1: 0.75 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector(
        '[class*="barFill"]',
      ) as HTMLElement;
      expect(barFill.style.width).toBe("75%");
    });

    it("caps bar width at 100%", () => {
      const utilization = { agent1: 1.5 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector(
        '[class*="barFill"]',
      ) as HTMLElement;
      expect(barFill.style.width).toBe("100%");
    });

    it("renders 0% width for zero utilization", () => {
      const utilization = { agent1: 0 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector(
        '[class*="barFill"]',
      ) as HTMLElement;
      expect(barFill.style.width).toBe("0%");
    });
  });

  describe("color thresholds (data-level)", () => {
    it('sets data-level="low" for values below 0.3', () => {
      const utilization = { agent1: 0.2 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector('[class*="barFill"]');
      expect(barFill).toHaveAttribute("data-level", "low");
    });

    it('sets data-level="medium" for values between 0.3 and 0.7', () => {
      const utilization = { agent1: 0.5 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector('[class*="barFill"]');
      expect(barFill).toHaveAttribute("data-level", "medium");
    });

    it('sets data-level="high" for values 0.7 and above', () => {
      const utilization = { agent1: 0.7 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector('[class*="barFill"]');
      expect(barFill).toHaveAttribute("data-level", "high");
    });

    it('sets data-level="low" at boundary value 0.29', () => {
      const utilization = { agent1: 0.29 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector('[class*="barFill"]');
      expect(barFill).toHaveAttribute("data-level", "low");
    });

    it('sets data-level="medium" at boundary value 0.3', () => {
      const utilization = { agent1: 0.3 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector('[class*="barFill"]');
      expect(barFill).toHaveAttribute("data-level", "medium");
    });

    it('sets data-level="medium" at boundary value 0.69', () => {
      const utilization = { agent1: 0.69 };

      const { container } = render(
        <AgentUtilizationBars utilization={utilization} />,
      );

      const barFill = container.querySelector('[class*="barFill"]');
      expect(barFill).toHaveAttribute("data-level", "medium");
    });
  });

  describe("empty state", () => {
    it("shows empty state when utilization is empty object", () => {
      render(<AgentUtilizationBars utilization={{}} />);

      expect(screen.getByText("No agent utilization data")).toBeInTheDocument();
    });
  });

  describe("edge cases", () => {
    it("handles single agent", () => {
      const utilization = { solo: 0.42 };

      render(<AgentUtilizationBars utilization={utilization} />);

      expect(screen.getByText("solo")).toBeInTheDocument();
      expect(screen.getByText("42%")).toBeInTheDocument();
    });

    it("handles many agents", () => {
      const utilization: Record<string, number> = {};
      for (let i = 0; i < 20; i++) {
        utilization[`agent-${String(i).padStart(2, "0")}`] = i / 20;
      }

      render(<AgentUtilizationBars utilization={utilization} />);

      expect(screen.getByText("agent-00")).toBeInTheDocument();
      expect(screen.getByText("agent-19")).toBeInTheDocument();
    });
  });
});
