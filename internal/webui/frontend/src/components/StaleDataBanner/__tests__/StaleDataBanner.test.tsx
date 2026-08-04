/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for StaleDataBanner component.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { StaleDataBanner } from "../StaleDataBanner";

describe("StaleDataBanner", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-15T12:00:10.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("rendering", () => {
    it("renders without crashing", () => {
      const disconnectedSince = Date.now() - 3000;
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });

    it("renders with warning styling by default (reconnecting state)", () => {
      const disconnectedSince = Date.now() - 7000;
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);

      const alert = screen.getByRole("alert");
      // Should not have data-lost attribute when connectionLost is false
      expect(alert).not.toHaveAttribute("data-lost");
      // Should show reconnecting message
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale/),
      ).toBeInTheDocument();
    });

    it("renders warning icon (aria-hidden)", () => {
      const disconnectedSince = Date.now() - 3000;
      const { container } = render(
        <StaleDataBanner disconnectedSince={disconnectedSince} />,
      );
      const icon = container.querySelector('[aria-hidden="true"]');
      expect(icon).toBeInTheDocument();
      expect(icon?.textContent).toBe("\u26A0");
    });
  });

  describe("elapsed time display", () => {
    it("shows elapsed time in seconds", () => {
      const disconnectedSince = Date.now() - 7000; // 7 seconds ago
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(7s\)/),
      ).toBeInTheDocument();
    });

    it("updates elapsed time every second via setInterval", () => {
      const disconnectedSince = Date.now() - 3000; // 3 seconds ago
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);

      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(3s\)/),
      ).toBeInTheDocument();

      // Advance 2 seconds
      act(() => {
        vi.advanceTimersByTime(2000);
      });

      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(5s\)/),
      ).toBeInTheDocument();
    });

    it("shows minutes and seconds for longer durations", () => {
      const disconnectedSince = Date.now() - 75000; // 75 seconds = 1m 15s
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(1m 15s\)/),
      ).toBeInTheDocument();
    });

    it("cleans up interval on unmount", () => {
      const disconnectedSince = Date.now() - 3000;
      const clearIntervalSpy = vi.spyOn(global, "clearInterval");
      const { unmount } = render(
        <StaleDataBanner disconnectedSince={disconnectedSince} />,
      );

      unmount();
      expect(clearIntervalSpy).toHaveBeenCalled();
      clearIntervalSpy.mockRestore();
    });
  });

  describe("formatElapsed", () => {
    it("formats seconds under 60 as Xs", () => {
      const disconnectedSince = Date.now() - 45000; // 45 seconds
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(45s\)/),
      ).toBeInTheDocument();
    });

    it("formats exactly 60 seconds as 1m 0s", () => {
      const disconnectedSince = Date.now() - 60000; // 60 seconds
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(1m 0s\)/),
      ).toBeInTheDocument();
    });

    it("formats 90 seconds as 1m 30s", () => {
      const disconnectedSince = Date.now() - 90000; // 90 seconds
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(1m 30s\)/),
      ).toBeInTheDocument();
    });

    it("formats 0 seconds as 0s", () => {
      const disconnectedSince = Date.now(); // 0 seconds ago
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(0s\)/),
      ).toBeInTheDocument();
    });

    it("formats multiple minutes correctly", () => {
      const disconnectedSince = Date.now() - 185000; // 3m 5s
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(3m 5s\)/),
      ).toBeInTheDocument();
    });
  });

  describe("connectionLost state", () => {
    it('shows "Connection lost" when connectionLost=true', () => {
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
        />,
      );
      expect(screen.getByText("Connection lost")).toBeInTheDocument();
    });

    it("does not show reconnecting message when connectionLost=true", () => {
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
        />,
      );
      expect(screen.queryByText(/Reconnecting/)).not.toBeInTheDocument();
    });

    it('sets data-lost="true" when connectionLost=true', () => {
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
        />,
      );
      const alert = screen.getByRole("alert");
      expect(alert).toHaveAttribute("data-lost", "true");
    });

    it("does not set data-lost when connectionLost=false", () => {
      const disconnectedSince = Date.now() - 5000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={false}
        />,
      );
      const alert = screen.getByRole("alert");
      expect(alert).not.toHaveAttribute("data-lost");
    });
  });

  describe("retry button", () => {
    it("shows Retry button when onRetry provided AND connectionLost=true", () => {
      const onRetry = vi.fn();
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
          onRetry={onRetry}
        />,
      );
      expect(
        screen.getByRole("button", { name: /retry/i }),
      ).toBeInTheDocument();
    });

    it("does not show Retry button when connectionLost=false even with onRetry", () => {
      const onRetry = vi.fn();
      const disconnectedSince = Date.now() - 10000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={false}
          onRetry={onRetry}
        />,
      );
      expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("does not show Retry button when connectionLost=true but no onRetry", () => {
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
        />,
      );
      expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("calls onRetry when Retry button is clicked", () => {
      const onRetry = vi.fn();
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
          onRetry={onRetry}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: /retry/i }));
      expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it("retry button has accessible aria-label", () => {
      const onRetry = vi.fn();
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
          onRetry={onRetry}
        />,
      );
      const button = screen.getByRole("button");
      expect(button).toHaveAttribute("aria-label", "Retry connection");
    });

    it('retry button has type="button"', () => {
      const onRetry = vi.fn();
      const disconnectedSince = Date.now() - 30000;
      render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          connectionLost={true}
          onRetry={onRetry}
        />,
      );
      const button = screen.getByRole("button");
      expect(button).toHaveAttribute("type", "button");
    });
  });

  describe("accessibility", () => {
    it('has role="alert"', () => {
      const disconnectedSince = Date.now() - 5000;
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });

    it('has aria-live="assertive"', () => {
      const disconnectedSince = Date.now() - 5000;
      render(<StaleDataBanner disconnectedSince={disconnectedSince} />);
      const alert = screen.getByRole("alert");
      expect(alert).toHaveAttribute("aria-live", "assertive");
    });

    it("icon is hidden from screen readers", () => {
      const disconnectedSince = Date.now() - 5000;
      const { container } = render(
        <StaleDataBanner disconnectedSince={disconnectedSince} />,
      );
      const icon = container.querySelector('[aria-hidden="true"]');
      expect(icon).toBeInTheDocument();
    });
  });

  describe("props", () => {
    it("applies className prop to root element", () => {
      const disconnectedSince = Date.now() - 5000;
      const { container } = render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          className="custom-class"
        />,
      );
      const root = container.firstChild as HTMLElement;
      expect(root).toHaveClass("custom-class");
    });

    it("combines className with default banner class", () => {
      const disconnectedSince = Date.now() - 5000;
      const { container } = render(
        <StaleDataBanner
          disconnectedSince={disconnectedSince}
          className="custom-class"
        />,
      );
      const root = container.firstChild as HTMLElement;
      expect(root.classList.length).toBeGreaterThan(1);
    });

    it("recalculates elapsed when disconnectedSince changes", () => {
      const disconnectedSince1 = Date.now() - 10000; // 10s ago
      const { rerender } = render(
        <StaleDataBanner disconnectedSince={disconnectedSince1} />,
      );
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(10s\)/),
      ).toBeInTheDocument();

      // Change disconnectedSince to 3s ago
      const disconnectedSince2 = Date.now() - 3000;
      rerender(<StaleDataBanner disconnectedSince={disconnectedSince2} />);
      expect(
        screen.getByText(/Reconnecting \u2014 data may be stale \(3s\)/),
      ).toBeInTheDocument();
    });
  });
});
