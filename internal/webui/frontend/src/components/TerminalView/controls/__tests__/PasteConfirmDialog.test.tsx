/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for PasteConfirmDialog component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import { PasteConfirmDialog } from "../PasteConfirmDialog";

// Mock CSS module
vi.mock("../PasteConfirmDialog.module.css", () => ({
  default: {
    overlay: "overlay",
    dialog: "dialog",
    header: "header",
    title: "title",
    subtitle: "subtitle",
    preview: "preview",
    previewText: "previewText",
    truncated: "truncated",
    footer: "footer",
    buttonPrimary: "buttonPrimary",
    buttonSecondary: "buttonSecondary",
  },
}));

describe("PasteConfirmDialog", () => {
  let onConfirm: ReturnType<typeof vi.fn>;
  let onCancel: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    onConfirm = vi.fn();
    onCancel = vi.fn();
  });

  // ── Rendering ────────────────────────────────────────────────────────────

  describe("rendering", () => {
    it("renders dialog with text preview when isOpen is true", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"line1\nline2\nline3"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByRole("alertdialog")).toBeInTheDocument();
      const pre = screen.getByRole("alertdialog").querySelector("pre");
      expect(pre).toBeInTheDocument();
      expect(pre!.textContent).toContain("line1\nline2\nline3");
    });

    it("does not render when isOpen is false", () => {
      const { container } = render(
        <PasteConfirmDialog
          isOpen={false}
          text={"line1\nline2"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(container.innerHTML).toBe("");
    });

    it("displays the correct line count in the title", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb\nc\nd\ne"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByText("Paste 5 lines?")).toBeInTheDocument();
    });

    it("displays subtitle describing the paste action", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(
        screen.getByText(
          "You are about to paste multi-line text into the terminal.",
        ),
      ).toBeInTheDocument();
    });

    it("renders Paste and Cancel buttons", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByRole("button", { name: "Paste" })).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Cancel" }),
      ).toBeInTheDocument();
    });
  });

  // ── Truncation ───────────────────────────────────────────────────────────

  describe("truncation", () => {
    it("shows all lines when 10 or fewer", () => {
      const text = Array.from({ length: 10 }, (_, i) => `line${i + 1}`).join(
        "\n",
      );
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={text}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByText(/line10/)).toBeInTheDocument();
      expect(screen.queryByText(/more line/)).not.toBeInTheDocument();
    });

    it("truncates text longer than 10 lines and shows remaining count", () => {
      const text = Array.from({ length: 15 }, (_, i) => `line${i + 1}`).join(
        "\n",
      );
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={text}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      // Should show first 10 lines
      expect(screen.getByText(/line10/)).toBeInTheDocument();
      // Should not show line 11+
      expect(screen.queryByText(/line11/)).not.toBeInTheDocument();
      // Should show truncation message
      expect(screen.getByText("... and 5 more lines")).toBeInTheDocument();
    });

    it("uses singular 'line' when exactly 1 extra line", () => {
      const text = Array.from({ length: 11 }, (_, i) => `line${i + 1}`).join(
        "\n",
      );
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={text}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByText("... and 1 more line")).toBeInTheDocument();
    });

    it("shows correct total line count in title even when truncated", () => {
      const text = Array.from({ length: 25 }, (_, i) => `line${i + 1}`).join(
        "\n",
      );
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={text}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByText("Paste 25 lines?")).toBeInTheDocument();
      expect(screen.getByText("... and 15 more lines")).toBeInTheDocument();
    });
  });

  // ── Button interactions ──────────────────────────────────────────────────

  describe("button interactions", () => {
    it("calls onConfirm when Paste button is clicked", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByRole("button", { name: "Paste" }));
      expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it("calls onCancel when Cancel button is clicked", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("calls onCancel when overlay is clicked", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      // Click the overlay (the outermost div)
      const overlay = screen.getByRole("alertdialog").parentElement!;
      fireEvent.click(overlay);
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("does not call onCancel when dialog body is clicked (stopPropagation)", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      fireEvent.click(screen.getByRole("alertdialog"));
      expect(onCancel).not.toHaveBeenCalled();
    });
  });

  // ── Keyboard interactions ────────────────────────────────────────────────

  describe("keyboard interactions", () => {
    it("calls onCancel when Escape key is pressed", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      const dialog = screen.getByRole("alertdialog");
      fireEvent.keyDown(dialog, { key: "Escape" });
      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("calls onConfirm when Enter key is pressed", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      const dialog = screen.getByRole("alertdialog");
      fireEvent.keyDown(dialog, { key: "Enter" });
      expect(onConfirm).toHaveBeenCalledTimes(1);
    });

    it("does not trigger callbacks for other keys", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      const dialog = screen.getByRole("alertdialog");
      fireEvent.keyDown(dialog, { key: "Tab" });
      fireEvent.keyDown(dialog, { key: "a" });

      expect(onConfirm).not.toHaveBeenCalled();
      expect(onCancel).not.toHaveBeenCalled();
    });
  });

  // ── Accessibility ────────────────────────────────────────────────────────

  describe("accessibility", () => {
    it('has role="alertdialog"', () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    });

    it("has aria-labelledby pointing to title", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      const dialog = screen.getByRole("alertdialog");
      expect(dialog).toHaveAttribute("aria-labelledby", "paste-dialog-title");
    });

    it("has aria-describedby pointing to description", () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      const dialog = screen.getByRole("alertdialog");
      expect(dialog).toHaveAttribute("aria-describedby", "paste-dialog-desc");
    });

    it('buttons have type="button"', () => {
      render(
        <PasteConfirmDialog
          isOpen={true}
          text={"a\nb"}
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      );

      const buttons = screen.getAllByRole("button");
      buttons.forEach((button) => {
        expect(button).toHaveAttribute("type", "button");
      });
    });
  });
});
