/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ReconnectingOverlay component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { ReconnectingOverlay } from "../ReconnectingOverlay";

describe("ReconnectingOverlay", () => {
  describe("null state", () => {
    it("renders null when state is null", () => {
      const { container } = render(<ReconnectingOverlay state={null} />);

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("reconnecting-overlay"),
      ).not.toBeInTheDocument();
    });
  });

  describe("reconnecting state", () => {
    it("renders reconnecting message with pulse animation", () => {
      render(<ReconnectingOverlay state="reconnecting" />);

      expect(screen.getByTestId("reconnecting-overlay")).toBeInTheDocument();
      expect(screen.getByText("Reconnecting...")).toBeInTheDocument();
    });

    it("does not render reconnect button", () => {
      render(<ReconnectingOverlay state="reconnecting" />);

      expect(
        screen.queryByTestId("reconnect-expired-button"),
      ).not.toBeInTheDocument();
    });

    it("displays attempt count when attemptCount > 0", () => {
      render(<ReconnectingOverlay state="reconnecting" attemptCount={3} />);

      expect(
        screen.getByText("Reconnecting (attempt 3)..."),
      ).toBeInTheDocument();
    });

    it("does not display attempt count when attemptCount is 0", () => {
      render(<ReconnectingOverlay state="reconnecting" attemptCount={0} />);

      expect(screen.getByText("Reconnecting...")).toBeInTheDocument();
      expect(screen.queryByText(/attempt/)).not.toBeInTheDocument();
    });
  });

  describe("expired state", () => {
    it("renders expired state with reconnect button", () => {
      render(<ReconnectingOverlay state="expired" />);

      expect(screen.getByTestId("reconnecting-overlay")).toBeInTheDocument();
      expect(screen.getByText("Session expired")).toBeInTheDocument();
      expect(
        screen.getByTestId("reconnect-expired-button"),
      ).toBeInTheDocument();
    });

    it("reconnect button calls onReconnect", () => {
      const onReconnect = vi.fn();
      render(<ReconnectingOverlay state="expired" onReconnect={onReconnect} />);

      fireEvent.click(screen.getByTestId("reconnect-expired-button"));

      expect(onReconnect).toHaveBeenCalledTimes(1);
    });

    it("reconnect button text is 'Reconnect'", () => {
      render(<ReconnectingOverlay state="expired" />);

      expect(screen.getByTestId("reconnect-expired-button")).toHaveTextContent(
        "Reconnect",
      );
    });
  });
});
