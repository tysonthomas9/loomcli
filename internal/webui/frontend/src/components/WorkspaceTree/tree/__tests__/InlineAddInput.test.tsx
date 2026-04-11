/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for InlineAddInput component.
 * Covers rendering, auto-focus, keyboard interactions (Enter/Escape),
 * blur behavior, error display, and submitting state.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { InlineAddInput } from "../InlineAddInput";

describe("InlineAddInput", () => {
  const defaultProps = {
    placeholder: "New task title...",
    onSubmit: vi.fn(() => Promise.resolve()),
    onCancel: vi.fn(),
    isSubmitting: false,
    error: null,
  };

  /** Helper to render with default props and overrides. */
  function renderInput(overrides: Partial<typeof defaultProps> = {}) {
    const props = { ...defaultProps, ...overrides };
    // Reset mocks for each render so tests are isolated
    if (props.onSubmit === defaultProps.onSubmit) {
      defaultProps.onSubmit.mockClear();
    }
    if (props.onCancel === defaultProps.onCancel) {
      defaultProps.onCancel.mockClear();
    }
    return render(<InlineAddInput {...props} />);
  }

  describe("rendering", () => {
    it("renders input with placeholder", () => {
      renderInput({ placeholder: "Enter epic name..." });
      const input = screen.getByTestId("inline-add-input");
      expect(input).toBeInTheDocument();
      expect(input).toHaveAttribute("placeholder", "Enter epic name...");
    });

    it("renders input with type text", () => {
      renderInput();
      const input = screen.getByTestId("inline-add-input");
      expect(input).toHaveAttribute("type", "text");
    });
  });

  describe("auto-focus", () => {
    it("auto-focuses input on mount", () => {
      renderInput();
      const input = screen.getByTestId("inline-add-input");
      expect(input).toHaveFocus();
    });
  });

  describe("Enter key", () => {
    it("calls onSubmit with trimmed value on Enter", () => {
      const onSubmit = vi.fn(() => Promise.resolve());
      renderInput({ onSubmit });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.change(input, { target: { value: "  My New Task  " } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onSubmit).toHaveBeenCalledTimes(1);
      expect(onSubmit).toHaveBeenCalledWith("My New Task");
    });

    it("does not call onSubmit when input is empty on Enter", () => {
      const onSubmit = vi.fn(() => Promise.resolve());
      renderInput({ onSubmit });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.keyDown(input, { key: "Enter" });

      expect(onSubmit).not.toHaveBeenCalled();
    });

    it("does not call onSubmit when input is whitespace-only on Enter", () => {
      const onSubmit = vi.fn(() => Promise.resolve());
      renderInput({ onSubmit });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.change(input, { target: { value: "   " } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onSubmit).not.toHaveBeenCalled();
    });
  });

  describe("Escape key", () => {
    it("calls onCancel on Escape", () => {
      const onCancel = vi.fn();
      renderInput({ onCancel });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.keyDown(input, { key: "Escape" });

      expect(onCancel).toHaveBeenCalledTimes(1);
    });
  });

  describe("blur behavior", () => {
    it("calls onCancel on blur with empty value", () => {
      const onCancel = vi.fn();
      renderInput({ onCancel });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.blur(input);

      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("calls onCancel on blur with whitespace-only value", () => {
      const onCancel = vi.fn();
      renderInput({ onCancel });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.change(input, { target: { value: "   " } });
      fireEvent.blur(input);

      expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it("calls onSubmit on blur with non-empty value", () => {
      const onSubmit = vi.fn(() => Promise.resolve());
      renderInput({ onSubmit });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.change(input, { target: { value: "Task from blur" } });
      fireEvent.blur(input);

      expect(onSubmit).toHaveBeenCalledTimes(1);
      expect(onSubmit).toHaveBeenCalledWith("Task from blur");
    });

    it("does not call onCancel on blur when isSubmitting is true", () => {
      const onCancel = vi.fn();
      renderInput({ onCancel, isSubmitting: true });
      const input = screen.getByTestId("inline-add-input");

      fireEvent.blur(input);

      expect(onCancel).not.toHaveBeenCalled();
    });
  });

  describe("error display", () => {
    it("shows error when error prop is set", () => {
      renderInput({ error: "Title is required" });
      const alert = screen.getByRole("alert");
      expect(alert).toBeInTheDocument();
      expect(alert).toHaveTextContent("Title is required");
    });

    it("does not show error when error is null", () => {
      renderInput({ error: null });
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });
  });

  describe("submitting state", () => {
    it("disables input when isSubmitting is true", () => {
      renderInput({ isSubmitting: true });
      const input = screen.getByTestId("inline-add-input");
      expect(input).toBeDisabled();
    });

    it("shows Creating... text when isSubmitting is true", () => {
      renderInput({ isSubmitting: true });
      expect(screen.getByText("Creating...")).toBeInTheDocument();
    });

    it("does not show Creating... text when isSubmitting is false", () => {
      renderInput({ isSubmitting: false });
      expect(screen.queryByText("Creating...")).not.toBeInTheDocument();
    });

    it("input is enabled when isSubmitting is false", () => {
      renderInput({ isSubmitting: false });
      const input = screen.getByTestId("inline-add-input");
      expect(input).not.toBeDisabled();
    });
  });
});
