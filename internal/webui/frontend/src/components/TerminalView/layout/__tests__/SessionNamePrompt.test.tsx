/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionNamePrompt component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { SessionNamePrompt } from "../SessionNamePrompt";

// Mock CSS module
vi.mock("../SessionNamePrompt.module.css", () => ({
  default: {
    overlay: "overlay",
    open: "open",
    modal: "modal",
    header: "header",
    title: "title",
    subtitle: "subtitle",
    content: "content",
    inputGroup: "inputGroup",
    label: "label",
    input: "input",
    inputError: "inputError",
    errorText: "errorText",
    footer: "footer",
    buttonPrimary: "buttonPrimary",
    buttonSecondary: "buttonSecondary",
  },
}));

// Mock the escape layer hook
vi.mock("@/hooks", () => ({
  useRegisterEscapeLayer: vi.fn(),
  LAYER_MODAL: 40,
}));

const defaultProps = {
  isOpen: true,
  existingNames: [] as string[],
  onConfirm: vi.fn(),
  onCancel: vi.fn(),
};

describe("SessionNamePrompt", () => {
  describe("rendering", () => {
    it("renders the overlay with correct testid", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(
        screen.getByTestId("session-name-prompt-overlay"),
      ).toBeInTheDocument();
    });

    it("renders the modal dialog", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(
        screen.getByTestId("session-name-prompt-modal"),
      ).toBeInTheDocument();
    });

    it("renders the title 'New Terminal Session'", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByText("New Terminal Session")).toBeInTheDocument();
    });

    it("renders the subtitle", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(
        screen.getByText("Enter a name for the new session"),
      ).toBeInTheDocument();
    });

    it("renders the input field", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByTestId("session-name-input")).toBeInTheDocument();
    });

    it("renders Cancel and Create buttons", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(
        screen.getByTestId("session-name-cancel-button"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("session-name-confirm-button"),
      ).toBeInTheDocument();
    });

    it("shows placeholder text", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByTestId("session-name-input")).toHaveAttribute(
        "placeholder",
        "e.g. auth-redesign",
      );
    });
  });

  describe("open/closed state", () => {
    it("sets aria-hidden=false when isOpen is true", () => {
      render(<SessionNamePrompt {...defaultProps} isOpen={true} />);

      expect(screen.getByTestId("session-name-prompt-overlay")).toHaveAttribute(
        "aria-hidden",
        "false",
      );
    });

    it("sets aria-hidden=true when isOpen is false", () => {
      render(<SessionNamePrompt {...defaultProps} isOpen={false} />);

      expect(screen.getByTestId("session-name-prompt-overlay")).toHaveAttribute(
        "aria-hidden",
        "true",
      );
    });
  });

  describe("input validation", () => {
    it("Create button is disabled when input is empty", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByTestId("session-name-confirm-button")).toBeDisabled();
    });

    it("Create button is enabled with valid input", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "my-session" },
      });

      expect(screen.getByTestId("session-name-confirm-button")).toBeEnabled();
    });

    it("shows error for invalid characters", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "invalid name!" },
      });

      expect(screen.getByTestId("session-name-error")).toBeInTheDocument();
      expect(
        screen.getByText(
          "Only letters, numbers, hyphens, and underscores are allowed",
        ),
      ).toBeInTheDocument();
    });

    it("shows error for duplicate name", () => {
      render(
        <SessionNamePrompt
          {...defaultProps}
          existingNames={["existing-session"]}
        />,
      );

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "existing-session" },
      });

      expect(screen.getByTestId("session-name-error")).toBeInTheDocument();
      expect(screen.getByText("Session already exists")).toBeInTheDocument();
    });

    it("Create button is disabled for invalid characters", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "has spaces" },
      });

      expect(screen.getByTestId("session-name-confirm-button")).toBeDisabled();
    });

    it("Create button is disabled for duplicate name", () => {
      render(<SessionNamePrompt {...defaultProps} existingNames={["taken"]} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "taken" },
      });

      expect(screen.getByTestId("session-name-confirm-button")).toBeDisabled();
    });

    it("no error shown when input is empty", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(
        screen.queryByTestId("session-name-error"),
      ).not.toBeInTheDocument();
    });

    it("accepts valid names with letters, numbers, hyphens, underscores", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "my_session-123" },
      });

      expect(
        screen.queryByTestId("session-name-error"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("session-name-confirm-button")).toBeEnabled();
    });
  });

  describe("form submission", () => {
    it("calls onConfirm with trimmed name on form submit", () => {
      const onConfirm = vi.fn();
      render(<SessionNamePrompt {...defaultProps} onConfirm={onConfirm} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "  my-session  " },
      });

      fireEvent.submit(
        screen.getByTestId("session-name-confirm-button").closest("form")!,
      );

      expect(onConfirm).toHaveBeenCalledWith("my-session");
    });

    it("does NOT call onConfirm when input has invalid characters", () => {
      const onConfirm = vi.fn();
      render(<SessionNamePrompt {...defaultProps} onConfirm={onConfirm} />);

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "bad name!" },
      });

      fireEvent.submit(
        screen.getByTestId("session-name-confirm-button").closest("form")!,
      );

      expect(onConfirm).not.toHaveBeenCalled();
    });

    it("does NOT call onConfirm when name is a duplicate", () => {
      const onConfirm = vi.fn();
      render(
        <SessionNamePrompt
          {...defaultProps}
          onConfirm={onConfirm}
          existingNames={["dupe"]}
        />,
      );

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "dupe" },
      });

      fireEvent.submit(
        screen.getByTestId("session-name-confirm-button").closest("form")!,
      );

      expect(onConfirm).not.toHaveBeenCalled();
    });

    it("does NOT call onConfirm when input is empty", () => {
      const onConfirm = vi.fn();
      render(<SessionNamePrompt {...defaultProps} onConfirm={onConfirm} />);

      fireEvent.submit(
        screen.getByTestId("session-name-confirm-button").closest("form")!,
      );

      expect(onConfirm).not.toHaveBeenCalled();
    });
  });

  describe("cancel", () => {
    it("calls onCancel when Cancel button is clicked", () => {
      const onCancel = vi.fn();
      render(<SessionNamePrompt {...defaultProps} onCancel={onCancel} />);

      fireEvent.click(screen.getByTestId("session-name-cancel-button"));

      expect(onCancel).toHaveBeenCalledTimes(1);
    });
  });

  describe("accessibility", () => {
    it('modal has role="dialog"', () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it('modal has aria-modal="true"', () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByRole("dialog")).toHaveAttribute("aria-modal", "true");
    });

    it("modal has aria-labelledby pointing to the title", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByRole("dialog")).toHaveAttribute(
        "aria-labelledby",
        "session-name-prompt-title",
      );
    });

    it("input has a label", () => {
      render(<SessionNamePrompt {...defaultProps} />);

      expect(screen.getByLabelText("Session name")).toBeInTheDocument();
    });
  });

  describe("input reset on open", () => {
    it("clears input value when reopened", () => {
      const { rerender } = render(
        <SessionNamePrompt {...defaultProps} isOpen={true} />,
      );

      fireEvent.change(screen.getByTestId("session-name-input"), {
        target: { value: "typed-value" },
      });

      rerender(<SessionNamePrompt {...defaultProps} isOpen={false} />);
      rerender(<SessionNamePrompt {...defaultProps} isOpen={true} />);

      expect(screen.getByTestId("session-name-input")).toHaveValue("");
    });
  });
});
