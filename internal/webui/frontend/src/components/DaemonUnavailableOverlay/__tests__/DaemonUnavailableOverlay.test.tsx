/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DaemonUnavailableOverlay component.
 *
 * Verifies rendering for different connection modes, error display,
 * countdown/retrying states, button visibility, and accessibility attributes.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { DaemonUnavailableOverlay } from "../DaemonUnavailableOverlay";

// Mock useFocusTrap to avoid focus management side effects in tests
vi.mock("@/hooks", () => ({
  useFocusTrap: vi.fn(),
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
  LAYER_TERMINAL_SEARCH: 5,
}));

describe("DaemonUnavailableOverlay", () => {
  describe("rendering by mode", () => {
    it('renders "Connecting to daemon..." for never_connected mode', () => {
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.getByText(/Connecting to daemon/)).toBeInTheDocument();
    });

    it('renders "Connection to daemon lost" for lost_connection mode', () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.getByText("Connection to daemon lost")).toBeInTheDocument();
    });

    it('renders "Connection to daemon lost" for reconnecting mode', () => {
      render(
        <DaemonUnavailableOverlay
          mode="reconnecting"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.getByText("Connection to daemon lost")).toBeInTheDocument();
    });
  });

  describe("error detail display", () => {
    it("shows error detail when mode is not never_connected and lastError exists", () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError="Connection refused"
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.getByText("Connection refused")).toBeInTheDocument();
    });

    it("does not show error detail when mode is never_connected even if lastError exists", () => {
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={5}
          lastError="Connection refused"
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.queryByText("Connection refused")).not.toBeInTheDocument();
    });

    it("does not show error detail when lastError is null", () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      // Should only have the title, no error detail paragraph
      const title = screen.getByText("Connection to daemon lost");
      expect(title).toBeInTheDocument();
    });
  });

  describe("countdown and retrying text", () => {
    it("shows countdown when retryCountdown > 0", () => {
      render(
        <DaemonUnavailableOverlay
          mode="reconnecting"
          retryCountdown={10}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.getByText(/Retrying in 10s/)).toBeInTheDocument();
    });

    it('shows "Retrying..." when countdown is 0 and mode is not never_connected', () => {
      render(
        <DaemonUnavailableOverlay
          mode="reconnecting"
          retryCountdown={0}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.getByText(/Retrying/)).toBeInTheDocument();
    });

    it('does not show "Retrying..." when countdown is 0 and mode is never_connected', () => {
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={0}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.queryByText(/Retrying/)).not.toBeInTheDocument();
    });

    it("does not show countdown text when retryCountdown is 0 for never_connected", () => {
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={0}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
    });
  });

  describe("Retry Now button", () => {
    it("shows Retry Now button for lost_connection mode", () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(
        screen.getByRole("button", { name: "Retry Now" }),
      ).toBeInTheDocument();
    });

    it("shows Retry Now button for reconnecting mode", () => {
      render(
        <DaemonUnavailableOverlay
          mode="reconnecting"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(
        screen.getByRole("button", { name: "Retry Now" }),
      ).toBeInTheDocument();
    });

    it("does not show Retry Now button for never_connected mode", () => {
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(
        screen.queryByRole("button", { name: "Retry Now" }),
      ).not.toBeInTheDocument();
    });

    it("calls onRetry when Retry Now button is clicked", () => {
      const onRetry = vi.fn();
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={onRetry}
          onSettingsClick={vi.fn()}
        />,
      );

      fireEvent.click(screen.getByRole("button", { name: "Retry Now" }));
      expect(onRetry).toHaveBeenCalledTimes(1);
    });
  });

  describe("Open Settings button", () => {
    it("always shows Open Settings button for never_connected mode", () => {
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(
        screen.getByRole("button", { name: "Open Settings" }),
      ).toBeInTheDocument();
    });

    it("always shows Open Settings button for lost_connection mode", () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(
        screen.getByRole("button", { name: "Open Settings" }),
      ).toBeInTheDocument();
    });

    it("always shows Open Settings button for reconnecting mode", () => {
      render(
        <DaemonUnavailableOverlay
          mode="reconnecting"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      expect(
        screen.getByRole("button", { name: "Open Settings" }),
      ).toBeInTheDocument();
    });

    it("calls onSettingsClick when Open Settings button is clicked", () => {
      const onSettingsClick = vi.fn();
      render(
        <DaemonUnavailableOverlay
          mode="never_connected"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={onSettingsClick}
        />,
      );

      fireEvent.click(screen.getByRole("button", { name: "Open Settings" }));
      expect(onSettingsClick).toHaveBeenCalledTimes(1);
    });
  });

  describe("accessibility", () => {
    it('has role="dialog" with aria-modal for accessibility', () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      const dialog = screen.getByRole("dialog");
      expect(dialog).toBeInTheDocument();
      expect(dialog).toHaveAttribute("aria-modal", "true");
    });

    it("has aria-labelledby pointing to the title", () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      const dialog = screen.getByRole("dialog");
      expect(dialog).toHaveAttribute("aria-labelledby", "daemon-overlay-title");
    });

    it("all buttons have type=button", () => {
      render(
        <DaemonUnavailableOverlay
          mode="lost_connection"
          retryCountdown={5}
          lastError={null}
          onRetry={vi.fn()}
          onSettingsClick={vi.fn()}
        />,
      );

      const buttons = screen.getAllByRole("button");
      buttons.forEach((button) => {
        expect(button).toHaveAttribute("type", "button");
      });
    });
  });
});
