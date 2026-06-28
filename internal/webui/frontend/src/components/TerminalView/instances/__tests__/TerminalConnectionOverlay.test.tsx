/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalConnectionOverlay component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { TerminalConnectionOverlay } from "../TerminalConnectionOverlay";
import type { ConnectionState } from "../TerminalInstance";

const defaultProps = {
  connectionState: "connecting" as ConnectionState,
  hasConnected: false,
  onReconnect: vi.fn(),
};

describe("TerminalConnectionOverlay", () => {
  describe("connected state", () => {
    it("returns null when connected", () => {
      const { container } = render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="connected"
          hasConnected={true}
        />,
      );

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("terminal-connection-overlay"),
      ).not.toBeInTheDocument();
    });
  });

  describe("connecting + hasConnected (background reconnect)", () => {
    it("returns null when connecting and hasConnected is true", () => {
      const { container } = render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="connecting"
          hasConnected={true}
        />,
      );

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("terminal-connection-overlay"),
      ).not.toBeInTheDocument();
    });
  });

  describe("connecting + initial connection", () => {
    it("renders overlay with spinner and Connecting... message", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="connecting"
          hasConnected={false}
        />,
      );

      expect(
        screen.getByTestId("terminal-connection-overlay"),
      ).toBeInTheDocument();
      expect(screen.getByText("Connecting...")).toBeInTheDocument();
    });

    it("does not render a reconnect button", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="connecting"
          hasConnected={false}
        />,
      );

      expect(
        screen.queryByTestId("terminal-reconnect-button"),
      ).not.toBeInTheDocument();
    });
  });

  describe("disconnected state", () => {
    it("renders Disconnected message", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      expect(
        screen.getByTestId("terminal-connection-overlay"),
      ).toBeInTheDocument();
      expect(screen.getByText("Disconnected")).toBeInTheDocument();
    });

    it("renders reconnect button", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      expect(
        screen.getByTestId("terminal-reconnect-button"),
      ).toBeInTheDocument();
    });

    it("renders Auto-reconnecting... subtext while reconnect loop is active", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
          isAutoReconnecting
        />,
      );

      expect(screen.getByText("Auto-reconnecting...")).toBeInTheDocument();
    });

    it("does not render Auto-reconnecting... subtext after reconnect loop stops", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      expect(
        screen.queryByText("Auto-reconnecting..."),
      ).not.toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("renders Connection failed message", () => {
      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      expect(
        screen.getByTestId("terminal-connection-overlay"),
      ).toBeInTheDocument();
      expect(screen.getByText("Connection failed")).toBeInTheDocument();
    });

    it("renders reconnect button", () => {
      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      expect(
        screen.getByTestId("terminal-reconnect-button"),
      ).toBeInTheDocument();
    });

    it("does not render Auto-reconnecting... subtext", () => {
      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      expect(
        screen.queryByText("Auto-reconnecting..."),
      ).not.toBeInTheDocument();
    });
  });

  describe("onReconnect callback", () => {
    it("fires onReconnect when reconnect button is clicked in disconnected state", () => {
      const onReconnect = vi.fn();
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
          onReconnect={onReconnect}
        />,
      );

      fireEvent.click(screen.getByTestId("terminal-reconnect-button"));

      expect(onReconnect).toHaveBeenCalledTimes(1);
    });

    it("fires onReconnect when reconnect button is clicked in error state", () => {
      const onReconnect = vi.fn();
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="error"
          onReconnect={onReconnect}
        />,
      );

      fireEvent.click(screen.getByTestId("terminal-reconnect-button"));

      expect(onReconnect).toHaveBeenCalledTimes(1);
    });
  });

  describe("ARIA roles and live regions", () => {
    it("connecting state overlay has role='status' and aria-live='polite'", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="connecting"
          hasConnected={false}
        />,
      );

      const overlay = screen.getByTestId("terminal-connection-overlay");
      expect(overlay).toHaveAttribute("role", "status");
      expect(overlay).toHaveAttribute("aria-live", "polite");
    });

    it("error state overlay has role='alert'", () => {
      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      const overlay = screen.getByTestId("terminal-connection-overlay");
      expect(overlay).toHaveAttribute("role", "alert");
    });

    it("disconnected state overlay has role='alert'", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      const overlay = screen.getByTestId("terminal-connection-overlay");
      expect(overlay).toHaveAttribute("role", "alert");
    });

    it("error state overlay does NOT have aria-live (implicit for role=alert)", () => {
      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      const overlay = screen.getByTestId("terminal-connection-overlay");
      expect(overlay).not.toHaveAttribute("aria-live");
    });

    it("disconnected state overlay does NOT have aria-live (implicit for role=alert)", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      const overlay = screen.getByTestId("terminal-connection-overlay");
      expect(overlay).not.toHaveAttribute("aria-live");
    });
  });

  describe("reconnect button accessibility", () => {
    it("reconnect button in error state has aria-label='Reconnect to terminal'", () => {
      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      const button = screen.getByTestId("terminal-reconnect-button");
      expect(button).toHaveAttribute("aria-label", "Reconnect to terminal");
    });

    it("reconnect button in disconnected state has aria-label='Reconnect to terminal'", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      const button = screen.getByTestId("terminal-reconnect-button");
      expect(button).toHaveAttribute("aria-label", "Reconnect to terminal");
    });

    it("reconnect button auto-focuses when entering error state", () => {
      // jsdom doesn't implement layout, so offsetParent is always null.
      // Mock it to simulate a visible element.
      Object.defineProperty(HTMLButtonElement.prototype, "offsetParent", {
        configurable: true,
        get: () => document.body,
      });

      render(
        <TerminalConnectionOverlay {...defaultProps} connectionState="error" />,
      );

      const button = screen.getByTestId("terminal-reconnect-button");
      expect(document.activeElement).toBe(button);

      // Restore default
      Object.defineProperty(HTMLButtonElement.prototype, "offsetParent", {
        configurable: true,
        get: () => null,
      });
    });

    it("reconnect button auto-focuses when entering disconnected state", () => {
      Object.defineProperty(HTMLButtonElement.prototype, "offsetParent", {
        configurable: true,
        get: () => document.body,
      });

      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      const button = screen.getByTestId("terminal-reconnect-button");
      expect(document.activeElement).toBe(button);

      Object.defineProperty(HTMLButtonElement.prototype, "offsetParent", {
        configurable: true,
        get: () => null,
      });
    });
  });
});
