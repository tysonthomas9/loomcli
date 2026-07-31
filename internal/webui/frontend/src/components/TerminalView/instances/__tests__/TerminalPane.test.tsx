/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalPane focusing on the connection/reconnect overlay
 * interaction. The two overlays (TerminalConnectionOverlay and
 * ReconnectingOverlay) must not render simultaneously in a way that
 * overlaps text or shows two Reconnect buttons.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";
import { forwardRef, type Ref } from "react";

import { TerminalPane } from "../TerminalPane";
import type { TabState } from "@/components/TerminalView/tabs";

// TerminalInstance is a heavy component that mounts wterm; stub it so the
// test focuses on overlay composition. forwardRef so the ref passed by
// TerminalPane is accepted without a warning. The factory is hoisted, so
// the stub is created inside it.
vi.mock("../TerminalInstance", () => ({
  TerminalInstance: forwardRef(function StubTerminalInstance(
    _props: unknown,
    _ref: Ref<unknown>,
  ) {
    return null;
  }),
}));

vi.mock("../TerminalConnectionOverlay.module.css", () => ({
  default: new Proxy({}, { get: () => "" }),
}));

const baseTab: TabState = {
  id: "tab-1",
  label: "Tab 1",
  sessionName: "s1",
  connectionState: "disconnected",
  backendName: "shell",
};

const defaultProps = {
  tab: baseTab,
  isActive: true,
  instanceRef: vi.fn(),
  onConnectionStateChange: vi.fn(),
  onReconnectStateChange: vi.fn(),
  onOutput: vi.fn(),
  onBackendCrash: vi.fn(),
  onCrashRestart: vi.fn(),
  onCloseTab: vi.fn(),
  onReconnect: vi.fn(),
  onTerminalFocus: undefined,
  hasConnected: false,
  reconnectState: null,
};

describe("TerminalPane overlay exclusivity", () => {
  describe("disconnected + reconnecting", () => {
    it("renders the connection overlay and suppresses the background ReconnectingOverlay", () => {
      render(
        <TerminalPane
          {...defaultProps}
          tab={{ ...baseTab, connectionState: "disconnected" }}
          reconnectState="reconnecting"
        />,
      );

      expect(
        screen.getByTestId("terminal-connection-overlay"),
      ).toBeInTheDocument();
      expect(screen.getByText("Disconnected")).toBeInTheDocument();
      expect(screen.getByText("Auto-reconnecting...")).toBeInTheDocument();
      // The background "Reconnecting..." overlay must NOT also render —
      // that was the source of the faint overlapping text.
      expect(
        screen.queryByTestId("reconnecting-overlay"),
      ).not.toBeInTheDocument();
      expect(screen.queryByText("Reconnecting...")).not.toBeInTheDocument();
    });

    it("renders a single Reconnect button (not two)", () => {
      render(
        <TerminalPane
          {...defaultProps}
          tab={{ ...baseTab, connectionState: "disconnected" }}
          reconnectState="reconnecting"
        />,
      );

      expect(screen.getAllByTestId("terminal-reconnect-button").length).toBe(1);
    });
  });

  describe("disconnected + expired", () => {
    it("shows Session expired subtext and a single Reconnect button", () => {
      render(
        <TerminalPane
          {...defaultProps}
          tab={{ ...baseTab, connectionState: "disconnected" }}
          reconnectState="expired"
        />,
      );

      expect(screen.getByText("Disconnected")).toBeInTheDocument();
      expect(screen.getByText("Session expired")).toBeInTheDocument();
      expect(screen.getAllByTestId("terminal-reconnect-button").length).toBe(1);
      // Expired ReconnectingOverlay is suppressed because the connection
      // overlay owns the disconnected state.
      expect(
        screen.queryByTestId("reconnecting-overlay"),
      ).not.toBeInTheDocument();
    });
  });

  describe("background reconnect (connecting + hasConnected)", () => {
    it("renders only the ReconnectingOverlay while the terminal stays visible", () => {
      render(
        <TerminalPane
          {...defaultProps}
          tab={{ ...baseTab, connectionState: "connecting" }}
          hasConnected
          reconnectState="reconnecting"
        />,
      );

      expect(screen.getByTestId("reconnecting-overlay")).toBeInTheDocument();
      expect(screen.getByText("Reconnecting...")).toBeInTheDocument();
      // TerminalConnectionOverlay returns null for connecting+hasConnected.
      expect(
        screen.queryByTestId("terminal-connection-overlay"),
      ).not.toBeInTheDocument();
    });
  });

  describe("initial connecting (no prior connect)", () => {
    it("renders the connection overlay spinner, not the background overlay", () => {
      render(
        <TerminalPane
          {...defaultProps}
          tab={{ ...baseTab, connectionState: "connecting" }}
          hasConnected={false}
          reconnectState={null}
        />,
      );

      expect(
        screen.getByTestId("terminal-connection-overlay"),
      ).toBeInTheDocument();
      expect(screen.getByText("Connecting...")).toBeInTheDocument();
      expect(
        screen.getByTestId("loading-skeleton-terminal"),
      ).toBeInTheDocument();
      expect(
        screen.queryByTestId("reconnecting-overlay"),
      ).not.toBeInTheDocument();
    });
  });
});
