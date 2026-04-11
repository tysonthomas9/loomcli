/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for CrashOverlay component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { CrashOverlay } from "../CrashOverlay";

const defaultProps = {
  reason: "backend process exited",
  onRestart: vi.fn(),
  onCloseTab: vi.fn(),
};

describe("CrashOverlay", () => {
  describe("rendering", () => {
    it("renders the overlay with role=alert", () => {
      render(<CrashOverlay {...defaultProps} />);

      const overlay = screen.getByTestId("crash-overlay");
      expect(overlay).toBeInTheDocument();
      expect(overlay).toHaveAttribute("role", "alert");
    });

    it("renders the Backend Exited heading", () => {
      render(<CrashOverlay {...defaultProps} />);

      expect(screen.getByText("Backend Exited")).toBeInTheDocument();
    });

    it("renders the error icon", () => {
      render(<CrashOverlay {...defaultProps} />);

      expect(screen.getByText("!")).toBeInTheDocument();
    });

    it("renders the reason text", () => {
      render(
        <CrashOverlay {...defaultProps} reason="segfault in backend process" />,
      );

      const reasonEl = screen.getByTestId("crash-overlay-reason");
      expect(reasonEl).toBeInTheDocument();
      expect(reasonEl).toHaveTextContent("segfault in backend process");
    });

    it("does not render reason element when reason is empty", () => {
      render(<CrashOverlay {...defaultProps} reason="" />);

      expect(
        screen.queryByTestId("crash-overlay-reason"),
      ).not.toBeInTheDocument();
    });

    it("renders Restart button", () => {
      render(<CrashOverlay {...defaultProps} />);

      const restartBtn = screen.getByTestId("crash-overlay-restart");
      expect(restartBtn).toBeInTheDocument();
      expect(restartBtn).toHaveTextContent("Restart");
    });

    it("renders Close Tab button", () => {
      render(<CrashOverlay {...defaultProps} />);

      const closeBtn = screen.getByTestId("crash-overlay-close");
      expect(closeBtn).toBeInTheDocument();
      expect(closeBtn).toHaveTextContent("Close Tab");
    });
  });

  describe("interactions", () => {
    it("clicking Restart calls onRestart", () => {
      const onRestart = vi.fn();
      render(<CrashOverlay {...defaultProps} onRestart={onRestart} />);

      fireEvent.click(screen.getByTestId("crash-overlay-restart"));

      expect(onRestart).toHaveBeenCalledTimes(1);
    });

    it("clicking Close Tab calls onCloseTab", () => {
      const onCloseTab = vi.fn();
      render(<CrashOverlay {...defaultProps} onCloseTab={onCloseTab} />);

      fireEvent.click(screen.getByTestId("crash-overlay-close"));

      expect(onCloseTab).toHaveBeenCalledTimes(1);
    });

    it("clicking Restart does not call onCloseTab", () => {
      const onRestart = vi.fn();
      const onCloseTab = vi.fn();
      render(
        <CrashOverlay
          {...defaultProps}
          onRestart={onRestart}
          onCloseTab={onCloseTab}
        />,
      );

      fireEvent.click(screen.getByTestId("crash-overlay-restart"));

      expect(onRestart).toHaveBeenCalledTimes(1);
      expect(onCloseTab).not.toHaveBeenCalled();
    });

    it("clicking Close Tab does not call onRestart", () => {
      const onRestart = vi.fn();
      const onCloseTab = vi.fn();
      render(
        <CrashOverlay
          {...defaultProps}
          onRestart={onRestart}
          onCloseTab={onCloseTab}
        />,
      );

      fireEvent.click(screen.getByTestId("crash-overlay-close"));

      expect(onCloseTab).toHaveBeenCalledTimes(1);
      expect(onRestart).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it("Restart button has type=button", () => {
      render(<CrashOverlay {...defaultProps} />);

      expect(screen.getByTestId("crash-overlay-restart")).toHaveAttribute(
        "type",
        "button",
      );
    });

    it("Close Tab button has type=button", () => {
      render(<CrashOverlay {...defaultProps} />);

      expect(screen.getByTestId("crash-overlay-close")).toHaveAttribute(
        "type",
        "button",
      );
    });

    it("error icon has aria-hidden=true", () => {
      render(<CrashOverlay {...defaultProps} />);

      const icon = screen.getByText("!");
      expect(icon).toHaveAttribute("aria-hidden", "true");
    });
  });

  describe("reason variations", () => {
    it("renders long reason text", () => {
      const longReason =
        "Error: SIGSEGV received at 0xdeadbeef\nStack trace:\n  at main.go:42\n  at handler.go:100";
      render(<CrashOverlay {...defaultProps} reason={longReason} />);

      const reasonEl = screen.getByTestId("crash-overlay-reason");
      expect(reasonEl).toBeInTheDocument();
      // textContent collapses whitespace; verify key fragments are present
      expect(reasonEl).toHaveTextContent(
        "Error: SIGSEGV received at 0xdeadbeef",
      );
      expect(reasonEl).toHaveTextContent("at handler.go:100");
    });

    it("renders special characters in reason", () => {
      const specialReason = 'Error: unexpected token "<" in JSON';
      render(<CrashOverlay {...defaultProps} reason={specialReason} />);

      const reasonEl = screen.getByTestId("crash-overlay-reason");
      expect(reasonEl).toHaveTextContent(specialReason);
    });
  });
});
