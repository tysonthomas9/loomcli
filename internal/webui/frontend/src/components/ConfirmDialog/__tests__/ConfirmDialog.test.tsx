/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ConfirmDialog component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { ConfirmDialog } from "../ConfirmDialog";

describe("ConfirmDialog", () => {
  describe("rendering", () => {
    it("does not render when isOpen is false", () => {
      render(
        <ConfirmDialog
          isOpen={false}
          title="Confirm"
          message="Are you sure?"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("confirm-dialog-overlay"),
      ).not.toBeInTheDocument();
    });

    it("renders title and message when open", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Close Tab"
          message="This will terminate the session."
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByText("Close Tab")).toBeInTheDocument();
      expect(
        screen.getByText("This will terminate the session."),
      ).toBeInTheDocument();
    });

    it("renders default confirm and cancel labels", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("confirm-dialog-confirm")).toHaveTextContent(
        "Confirm",
      );
      expect(screen.getByTestId("confirm-dialog-cancel")).toHaveTextContent(
        "Cancel",
      );
    });

    it("renders custom labels", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          confirmLabel="Yes, close it"
          cancelLabel="Keep open"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("confirm-dialog-confirm")).toHaveTextContent(
        "Yes, close it",
      );
      expect(screen.getByTestId("confirm-dialog-cancel")).toHaveTextContent(
        "Keep open",
      );
    });
  });

  describe("interactions", () => {
    it("calls onConfirm when confirm button clicked", () => {
      const onConfirm = vi.fn();
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={onConfirm}
          onCancel={vi.fn()}
        />,
      );

      fireEvent.click(screen.getByTestId("confirm-dialog-confirm"));
      expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it("calls onCancel when cancel button clicked", () => {
      const onCancel = vi.fn();
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByTestId("confirm-dialog-cancel"));
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("calls onCancel when Escape pressed", () => {
      const onCancel = vi.fn();
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      fireEvent.keyDown(document, { key: "Escape" });
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("calls onCancel when backdrop clicked", () => {
      const onCancel = vi.fn();
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByTestId("confirm-dialog-overlay"));
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("does not call onCancel when dialog content clicked", () => {
      const onCancel = vi.fn();
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={onCancel}
        />,
      );

      // Click on the dialog element (not the overlay)
      const dialog = screen.getByRole("alertdialog");
      fireEvent.click(dialog);
      expect(onCancel).not.toHaveBeenCalled();
    });
  });

  describe("variants", () => {
    it("danger variant applies correct class to confirm button", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          variant="danger"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const confirmButton = screen.getByTestId("confirm-dialog-confirm");
      // CSS modules mangle class names, so check for pattern containing 'Danger'
      expect(confirmButton.className).toMatch(/confirmDanger|Danger/i);
    });

    it("default variant does not apply danger class to confirm button", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          variant="default"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const confirmButton = screen.getByTestId("confirm-dialog-confirm");
      expect(confirmButton.className).not.toMatch(/confirmDanger|Danger/);
    });
  });

  describe("accessibility", () => {
    it("has alertdialog role", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Confirm Action"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      const dialog = screen.getByRole("alertdialog");
      expect(dialog).toBeInTheDocument();
      expect(dialog).toHaveAttribute("aria-modal", "true");
      expect(dialog).toHaveAttribute("aria-label", "Confirm Action");
    });

    it("buttons have type=button", () => {
      render(
        <ConfirmDialog
          isOpen={true}
          title="Title"
          message="Message"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByTestId("confirm-dialog-confirm")).toHaveAttribute(
        "type",
        "button",
      );
      expect(screen.getByTestId("confirm-dialog-cancel")).toHaveAttribute(
        "type",
        "button",
      );
    });
  });
});
