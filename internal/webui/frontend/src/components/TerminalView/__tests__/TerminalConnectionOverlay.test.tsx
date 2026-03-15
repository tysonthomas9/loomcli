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

    it("renders Auto-reconnecting... subtext", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="disconnected"
        />,
      );

      expect(screen.getByText("Auto-reconnecting...")).toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("renders Connection failed message", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="error"
        />,
      );

      expect(
        screen.getByTestId("terminal-connection-overlay"),
      ).toBeInTheDocument();
      expect(screen.getByText("Connection failed")).toBeInTheDocument();
    });

    it("renders reconnect button", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="error"
        />,
      );

      expect(
        screen.getByTestId("terminal-reconnect-button"),
      ).toBeInTheDocument();
    });

    it("does not render Auto-reconnecting... subtext", () => {
      render(
        <TerminalConnectionOverlay
          {...defaultProps}
          connectionState="error"
        />,
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
});
