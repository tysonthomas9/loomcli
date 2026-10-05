/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ConfirmDialog component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { KeyboardShortcutProvider } from "@/hooks";

import { ConfirmDialog } from "../ConfirmDialog";

function renderWithProvider(ui: React.ReactElement) {
  return render(<KeyboardShortcutProvider>{ui}</KeyboardShortcutProvider>);
}

describe("ConfirmDialog", () => {
  describe("rendering", () => {
    it("does not render when isOpen is false", () => {
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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

    it("renders ReactNode message content", () => {
      renderWithProvider(
        <ConfirmDialog
          isOpen={true}
          title="Confirm"
          message={
            <ul>
              <li>Branch A: 2 commits</li>
              <li>Branch B: 1 commit</li>
            </ul>
          }
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );

      expect(screen.getByText("Branch A: 2 commits")).toBeInTheDocument();
      expect(screen.getByText("Branch B: 1 commit")).toBeInTheDocument();
    });
  });

  describe("interactions", () => {
    it("calls onConfirm when confirm button clicked", () => {
      const onConfirm = vi.fn();
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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
      renderWithProvider(
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

// jsdom does not cascade CSS modules, so this reads the rules (SB4).
describe("ConfirmDialog danger button CSS", () => {
  // Comments out, then each rule as its selectors and declarations, in order.
  const rules = readFileSync(
    resolve(__dirname, "../ConfirmDialog.module.css"),
    "utf8",
  )
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("}")
    .map((r) => r.split("{"))
    .filter((r) => r.length === 2)
    .map(([sel, body], i) => ({
      i,
      selectors: sel!.split(",").map((x) => x.trim()),
      // The last background declaration in a rule is the one that applies.
      background: [...body!.matchAll(/(?:^|;)\s*background:\s*([^;]+)/g)]
        .at(-1)?.[1]
        ?.trim(),
    }));
  // The last rule for a selector that sets a background wins the cascade.
  const winner = (selector: string) =>
    rules.filter((r) => r.selectors.includes(selector) && r.background).at(-1);

  it("keeps the danger colour on hover and keyboard focus, never the primary blue", () => {
    const primaryHover = winner(".confirmButton:hover")!;
    expect(primaryHover.background).toBe("var(--color-primary-hover)");
    for (const state of [
      ".confirmDanger:hover",
      ".confirmDanger:focus-visible",
    ]) {
      const danger = winner(state);
      expect(danger?.background, state).toBe("var(--color-danger)");
      // Same specificity as .confirmButton:hover, so it must come later.
      expect(danger!.i, state).toBeGreaterThan(primaryHover.i);
    }
  });
});
