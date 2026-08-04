/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { AetherModal } from "../AetherModal";

function renderModal(
  overrides: Partial<React.ComponentProps<typeof AetherModal>> = {},
) {
  const onClose = vi.fn();
  render(
    <AetherModal
      isOpen
      title="Test modal"
      onClose={onClose}
      overlayTestId="test-modal-overlay"
      closeTestId="test-modal-close"
      {...overrides}
    >
      <p>Modal body</p>
    </AetherModal>,
  );
  return { onClose };
}

describe("AetherModal: backdrop dismiss", () => {
  it("calls onClose when the overlay backdrop is clicked directly", () => {
    const { onClose } = renderModal();
    const overlay = screen.getByTestId("test-modal-overlay");

    fireEvent.click(overlay, { target: overlay, currentTarget: overlay });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not call onClose when clicking inside the dismiss buffer around the dialog", () => {
    const { onClose } = renderModal();
    const dialog = screen.getByRole("dialog");
    const dialogShell = dialog.parentElement;

    expect(dialogShell).not.toBeNull();
    fireEvent.click(dialogShell!);

    expect(onClose).not.toHaveBeenCalled();
  });

  it("does not call onClose when clicking dialog content", () => {
    const { onClose } = renderModal();

    fireEvent.click(screen.getByRole("dialog"));

    expect(onClose).not.toHaveBeenCalled();
  });

  it("does not call onClose on backdrop click when overlay dismiss is disabled", () => {
    const { onClose } = renderModal({ disableOverlayDismiss: true });
    const overlay = screen.getByTestId("test-modal-overlay");

    fireEvent.click(overlay, { target: overlay, currentTarget: overlay });

    expect(onClose).not.toHaveBeenCalled();
  });

  it("calls onOverlayClick instead of onClose when provided", () => {
    const onClose = vi.fn();
    const onOverlayClick = vi.fn();
    render(
      <AetherModal
        isOpen
        title="Test modal"
        onClose={onClose}
        onOverlayClick={onOverlayClick}
        overlayTestId="test-modal-overlay"
      >
        <p>Modal body</p>
      </AetherModal>,
    );

    const overlay = screen.getByTestId("test-modal-overlay");
    fireEvent.click(overlay, { target: overlay, currentTarget: overlay });

    expect(onOverlayClick).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe("AetherModal: explicit close controls", () => {
  it("calls onClose when the close button is clicked", () => {
    const { onClose } = renderModal();

    fireEvent.click(screen.getByTestId("test-modal-close"));

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
