/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  KeyboardShortcutProvider,
  LAYER_TERMINAL_PANEL,
  useRegisterEscapeLayer,
} from "@/hooks/ui/useKeyboardShortcuts";

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

function LowerLayer({ onEscape }: { onEscape: () => void }): null {
  useRegisterEscapeLayer(LAYER_TERMINAL_PANEL, onEscape, true);
  return null;
}

describe("AetherModal: Escape", () => {
  it("closes on Escape through the app's Escape layers, also from a field", () => {
    const onClose = vi.fn();
    render(
      <KeyboardShortcutProvider>
        <AetherModal isOpen title="Test modal" onClose={onClose}>
          <input aria-label="Name" />
        </AetherModal>
      </KeyboardShortcutProvider>,
    );
    const name = screen.getByLabelText("Name");
    name.focus();

    fireEvent.keyDown(name, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("closes before a lower layer (the agents drawer) under it", () => {
    const onClose = vi.fn();
    const onDrawerEscape = vi.fn();
    render(
      <KeyboardShortcutProvider>
        <LowerLayer onEscape={onDrawerEscape} />
        <AetherModal isOpen title="Test modal" onClose={onClose}>
          <p>Modal body</p>
        </AetherModal>
      </KeyboardShortcutProvider>,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onDrawerEscape).not.toHaveBeenCalled();
  });

  it("ignores Escape when closeOnEscape is false, and so do layers under it", () => {
    const onClose = vi.fn();
    const onDrawerEscape = vi.fn();
    render(
      <KeyboardShortcutProvider>
        <LowerLayer onEscape={onDrawerEscape} />
        <AetherModal
          isOpen
          title="Test modal"
          onClose={onClose}
          closeOnEscape={false}
        >
          <p>Modal body</p>
        </AetherModal>
      </KeyboardShortcutProvider>,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    expect(onClose).not.toHaveBeenCalled();
    expect(onDrawerEscape).not.toHaveBeenCalled();
  });
});
