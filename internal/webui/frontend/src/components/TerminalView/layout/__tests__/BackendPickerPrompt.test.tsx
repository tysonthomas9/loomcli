/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BackendPickerPrompt component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { BackendPickerPrompt } from "../BackendPickerPrompt";

// Mock CSS module
vi.mock("../BackendPickerPrompt.module.css", () => ({
  default: {
    overlay: "overlay",
    open: "open",
    modal: "modal",
    header: "header",
    title: "title",
    subtitle: "subtitle",
    content: "content",
    selectGroup: "selectGroup",
    label: "label",
    select: "select",
    loadingText: "loadingText",
    emptyText: "emptyText",
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

// Mock KNOWN_BACKEND_DEFAULTS
vi.mock("@/utils/workspace", () => ({
  KNOWN_BACKEND_DEFAULTS: {
    claude: {
      displayName: "Claude",
      provider: "Anthropic",
      brandColor: "#d4a574",
    },
    codex: { displayName: "Codex", provider: "OpenAI", brandColor: "#10a37f" },
    opencode: {
      displayName: "OpenCode",
      provider: "Open Source",
      brandColor: "#6366f1",
    },
  },
}));

const defaultProps = {
  isOpen: true,
  availableBackends: ["claude", "codex"],
  isLoading: false,
  onSelect: vi.fn(),
  onCancel: vi.fn(),
};

describe("BackendPickerPrompt", () => {
  describe("rendering", () => {
    it("renders the overlay with correct testid", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(
        screen.getByTestId("backend-picker-prompt-overlay"),
      ).toBeInTheDocument();
    });

    it("renders the modal dialog", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(
        screen.getByTestId("backend-picker-prompt-modal"),
      ).toBeInTheDocument();
    });

    it("renders the title 'New Terminal Session'", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByText("New Terminal Session")).toBeInTheDocument();
    });

    it("renders the subtitle", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(
        screen.getByText("Select a backend for the new session"),
      ).toBeInTheDocument();
    });

    it("renders Cancel and Create buttons", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(
        screen.getByTestId("backend-picker-cancel-button"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("backend-picker-create-button"),
      ).toBeInTheDocument();
    });
  });

  describe("open/closed state", () => {
    it("sets aria-hidden=false when isOpen is true", () => {
      render(<BackendPickerPrompt {...defaultProps} isOpen={true} />);

      expect(
        screen.getByTestId("backend-picker-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "false");
    });

    it("sets aria-hidden=true when isOpen is false", () => {
      render(<BackendPickerPrompt {...defaultProps} isOpen={false} />);

      expect(
        screen.getByTestId("backend-picker-prompt-overlay"),
      ).toHaveAttribute("aria-hidden", "true");
    });
  });

  describe("backend select dropdown", () => {
    it("renders the select dropdown with available backends", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      const select = screen.getByTestId("backend-picker-select");
      expect(select).toBeInTheDocument();
    });

    it("displays the display name for known backends", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByText("Claude")).toBeInTheDocument();
      expect(screen.getByText("Codex")).toBeInTheDocument();
    });

    it("defaults to the first available backend", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      const select = screen.getByTestId("backend-picker-select");
      expect(select).toHaveValue("claude");
    });

    it("defaults to the preferred backend when it is available", () => {
      render(
        <BackendPickerPrompt {...defaultProps} preferredBackend="codex" />,
      );

      const select = screen.getByTestId("backend-picker-select");
      expect(select).toHaveValue("codex");
    });

    it("falls back to the first available backend when the preferred backend is unavailable", () => {
      render(
        <BackendPickerPrompt {...defaultProps} preferredBackend="opencode" />,
      );

      const select = screen.getByTestId("backend-picker-select");
      expect(select).toHaveValue("claude");
    });

    it("allows changing the selected backend", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      const select = screen.getByTestId("backend-picker-select");
      fireEvent.change(select, { target: { value: "codex" } });

      expect(select).toHaveValue("codex");
    });

    it("shows raw backend name for unknown backends", () => {
      render(
        <BackendPickerPrompt
          {...defaultProps}
          availableBackends={["custom-backend"]}
        />,
      );

      expect(screen.getByText("custom-backend")).toBeInTheDocument();
    });
  });

  describe("loading state", () => {
    it("shows loading text when isLoading is true", () => {
      render(<BackendPickerPrompt {...defaultProps} isLoading={true} />);

      expect(screen.getByTestId("backend-picker-loading")).toBeInTheDocument();
      expect(screen.getByText("Loading backends...")).toBeInTheDocument();
    });

    it("does NOT render select when loading", () => {
      render(<BackendPickerPrompt {...defaultProps} isLoading={true} />);

      expect(
        screen.queryByTestId("backend-picker-select"),
      ).not.toBeInTheDocument();
    });

    it("Create button is disabled when loading", () => {
      render(<BackendPickerPrompt {...defaultProps} isLoading={true} />);

      expect(screen.getByTestId("backend-picker-create-button")).toBeDisabled();
    });
  });

  describe("empty state", () => {
    it("shows empty text when no backends are available", () => {
      render(<BackendPickerPrompt {...defaultProps} availableBackends={[]} />);

      expect(screen.getByTestId("backend-picker-empty")).toBeInTheDocument();
      expect(screen.getByText("No backends available")).toBeInTheDocument();
    });

    it("does NOT render select when empty", () => {
      render(<BackendPickerPrompt {...defaultProps} availableBackends={[]} />);

      expect(
        screen.queryByTestId("backend-picker-select"),
      ).not.toBeInTheDocument();
    });

    it("Create button is disabled when empty", () => {
      render(<BackendPickerPrompt {...defaultProps} availableBackends={[]} />);

      expect(screen.getByTestId("backend-picker-create-button")).toBeDisabled();
    });
  });

  describe("form submission", () => {
    it("calls onSelect with selected backend on submit", () => {
      const onSelect = vi.fn();
      render(<BackendPickerPrompt {...defaultProps} onSelect={onSelect} />);

      fireEvent.submit(
        screen.getByTestId("backend-picker-create-button").closest("form")!,
      );

      expect(onSelect).toHaveBeenCalledWith("claude");
    });

    it("calls onSelect with changed backend after selection change", () => {
      const onSelect = vi.fn();
      render(<BackendPickerPrompt {...defaultProps} onSelect={onSelect} />);

      fireEvent.change(screen.getByTestId("backend-picker-select"), {
        target: { value: "codex" },
      });

      fireEvent.submit(
        screen.getByTestId("backend-picker-create-button").closest("form")!,
      );

      expect(onSelect).toHaveBeenCalledWith("codex");
    });

    it("Create button is enabled with available backends", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByTestId("backend-picker-create-button")).toBeEnabled();
    });
  });

  describe("cancel", () => {
    it("calls onCancel when Cancel button is clicked", () => {
      const onCancel = vi.fn();
      render(<BackendPickerPrompt {...defaultProps} onCancel={onCancel} />);

      fireEvent.click(screen.getByTestId("backend-picker-cancel-button"));

      expect(onCancel).toHaveBeenCalledTimes(1);
    });
  });

  describe("accessibility", () => {
    it('modal has role="dialog"', () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it('modal has aria-modal="true"', () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByRole("dialog")).toHaveAttribute("aria-modal", "true");
    });

    it("modal has aria-labelledby pointing to the title", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByRole("dialog")).toHaveAttribute(
        "aria-labelledby",
        "backend-picker-prompt-title",
      );
    });

    it("select has a label", () => {
      render(<BackendPickerPrompt {...defaultProps} />);

      expect(screen.getByLabelText("Backend")).toBeInTheDocument();
    });
  });

  describe("reopen behavior", () => {
    it("resets selected to first backend when reopened", () => {
      const { rerender } = render(<BackendPickerPrompt {...defaultProps} />);

      fireEvent.change(screen.getByTestId("backend-picker-select"), {
        target: { value: "codex" },
      });

      rerender(<BackendPickerPrompt {...defaultProps} isOpen={false} />);
      rerender(<BackendPickerPrompt {...defaultProps} isOpen={true} />);

      expect(screen.getByTestId("backend-picker-select")).toHaveValue("claude");
    });
  });
});
