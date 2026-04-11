/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for LabelEditor component.
 */

import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";

import { LabelEditor, type LabelEditorProps } from "../LabelEditor";

// Default test props
function defaultProps(overrides?: Partial<LabelEditorProps>): LabelEditorProps {
  return {
    labels: [],
    onAddLabel: vi.fn().mockResolvedValue(undefined),
    onRemoveLabel: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe("LabelEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("rendering", () => {
    it("renders with the section title", () => {
      render(<LabelEditor {...defaultProps()} />);

      expect(screen.getByTestId("label-editor")).toBeInTheDocument();
      expect(screen.getByText("Labels")).toBeInTheDocument();
    });

    it("renders labels as pills", () => {
      render(
        <LabelEditor
          {...defaultProps({ labels: ["frontend", "bug", "urgent"] })}
        />,
      );

      expect(screen.getByTestId("label-list")).toBeInTheDocument();
      expect(screen.getByText("frontend")).toBeInTheDocument();
      expect(screen.getByText("bug")).toBeInTheDocument();
      expect(screen.getByText("urgent")).toBeInTheDocument();
    });

    it("renders empty state when no labels", () => {
      render(<LabelEditor {...defaultProps()} />);

      expect(screen.getByTestId("no-labels")).toHaveTextContent("No labels");
    });

    it("shows add button when not disabled", () => {
      render(<LabelEditor {...defaultProps()} />);

      expect(screen.getByTestId("add-label-button")).toBeInTheDocument();
    });

    it("hides add button when disabled", () => {
      render(<LabelEditor {...defaultProps({ disabled: true })} />);

      expect(screen.queryByTestId("add-label-button")).not.toBeInTheDocument();
    });

    it("renders remove buttons for each label when not disabled", () => {
      render(
        <LabelEditor {...defaultProps({ labels: ["frontend", "bug"] })} />,
      );

      expect(screen.getByTestId("remove-label-frontend")).toBeInTheDocument();
      expect(screen.getByTestId("remove-label-bug")).toBeInTheDocument();
    });

    it("hides remove buttons when disabled", () => {
      render(
        <LabelEditor
          {...defaultProps({ labels: ["frontend"], disabled: true })}
        />,
      );

      expect(
        screen.queryByTestId("remove-label-frontend"),
      ).not.toBeInTheDocument();
    });
  });

  describe("add label flow", () => {
    it("shows input form when add button is clicked", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));

      expect(screen.getByTestId("add-label-form")).toBeInTheDocument();
      expect(screen.getByTestId("label-input")).toBeInTheDocument();
    });

    it("focuses input when entering add mode", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));

      expect(screen.getByTestId("label-input")).toHaveFocus();
    });

    it("hides add button while in add mode", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));

      expect(screen.queryByTestId("add-label-button")).not.toBeInTheDocument();
    });

    it("calls onAddLabel when Enter is pressed with valid input", async () => {
      const onAddLabel = vi.fn().mockResolvedValue(undefined);
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "new-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(onAddLabel).toHaveBeenCalledWith("new-label");
      });
    });

    it("optimistically adds label before API resolves", async () => {
      let resolveAdd: () => void;
      const onAddLabel = vi.fn().mockImplementation(
        () =>
          new Promise<void>((resolve) => {
            resolveAdd = resolve;
          }),
      );
      render(
        <LabelEditor {...defaultProps({ labels: ["existing"], onAddLabel })} />,
      );

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "new-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      // Optimistically added
      expect(screen.getByText("new-label")).toBeInTheDocument();

      // Clean up
      await act(async () => {
        resolveAdd!();
      });
    });

    it("trims whitespace from label input", async () => {
      const onAddLabel = vi.fn().mockResolvedValue(undefined);
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "  trimmed-label  " },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(onAddLabel).toHaveBeenCalledWith("trimmed-label");
      });
    });

    it("clears input and exits add mode after successful add", async () => {
      const onAddLabel = vi.fn().mockResolvedValue(undefined);
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "new-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(screen.queryByTestId("add-label-form")).not.toBeInTheDocument();
      });
    });
  });

  describe("remove label", () => {
    it("calls onRemoveLabel when remove button is clicked", async () => {
      const onRemoveLabel = vi.fn().mockResolvedValue(undefined);
      render(
        <LabelEditor
          {...defaultProps({ labels: ["frontend"], onRemoveLabel })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-label-frontend"));

      await waitFor(() => {
        expect(onRemoveLabel).toHaveBeenCalledWith("frontend");
      });
    });

    it("optimistically removes label before API resolves", async () => {
      let resolveRemove: () => void;
      const onRemoveLabel = vi.fn().mockImplementation(
        () =>
          new Promise<void>((resolve) => {
            resolveRemove = resolve;
          }),
      );
      render(
        <LabelEditor
          {...defaultProps({
            labels: ["frontend", "bug"],
            onRemoveLabel,
          })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-label-frontend"));

      // Optimistically removed
      expect(screen.queryByText("frontend")).not.toBeInTheDocument();
      expect(screen.getByText("bug")).toBeInTheDocument();

      // Clean up
      await act(async () => {
        resolveRemove!();
      });
    });

    it("has accessible aria-label on remove buttons", () => {
      render(<LabelEditor {...defaultProps({ labels: ["frontend"] })} />);

      expect(screen.getByTestId("remove-label-frontend")).toHaveAttribute(
        "aria-label",
        "Remove label frontend",
      );
    });
  });

  describe("duplicate label prevention", () => {
    it("shows error when adding exact duplicate label", () => {
      render(<LabelEditor {...defaultProps({ labels: ["frontend"] })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "frontend" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(screen.getByTestId("label-error")).toHaveTextContent(
        "Label already exists",
      );
    });

    it("shows error when adding case-insensitive duplicate label", () => {
      render(<LabelEditor {...defaultProps({ labels: ["Frontend"] })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "frontend" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(screen.getByTestId("label-error")).toHaveTextContent(
        "Label already exists",
      );
    });

    it("does not call onAddLabel for duplicate labels", () => {
      const onAddLabel = vi.fn();
      render(
        <LabelEditor {...defaultProps({ labels: ["frontend"], onAddLabel })} />,
      );

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "frontend" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(onAddLabel).not.toHaveBeenCalled();
    });
  });

  describe("empty input rejection", () => {
    it("shows error when pressing Enter with empty input", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(screen.getByTestId("label-error")).toHaveTextContent(
        "Label cannot be empty",
      );
    });

    it("shows error when pressing Enter with whitespace-only input", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "   " },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(screen.getByTestId("label-error")).toHaveTextContent(
        "Label cannot be empty",
      );
    });

    it("does not call onAddLabel for empty input", () => {
      const onAddLabel = vi.fn();
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(onAddLabel).not.toHaveBeenCalled();
    });
  });

  describe("optimistic update + rollback on error", () => {
    it("rolls back added label on API error", async () => {
      const onAddLabel = vi.fn().mockRejectedValue(new Error("Server error"));
      render(
        <LabelEditor {...defaultProps({ labels: ["existing"], onAddLabel })} />,
      );

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "new-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      // Optimistically added immediately
      expect(screen.getByText("new-label")).toBeInTheDocument();

      // Rolled back after error
      await waitFor(() => {
        expect(screen.queryByText("new-label")).not.toBeInTheDocument();
      });
      expect(screen.getByText("existing")).toBeInTheDocument();
    });

    it("shows error message from API failure on add", async () => {
      const onAddLabel = vi.fn().mockRejectedValue(new Error("Server error"));
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "new-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(screen.getByTestId("label-error")).toHaveTextContent(
          "Server error",
        );
      });
    });

    it("shows generic error for non-Error exceptions on add", async () => {
      const onAddLabel = vi.fn().mockRejectedValue("string error");
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "new-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(screen.getByTestId("label-error")).toHaveTextContent(
          "Failed to add label",
        );
      });
    });

    it("rolls back removed label on API error", async () => {
      const onRemoveLabel = vi
        .fn()
        .mockRejectedValue(new Error("Delete failed"));
      render(
        <LabelEditor
          {...defaultProps({ labels: ["frontend"], onRemoveLabel })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-label-frontend"));

      // Optimistically removed immediately
      expect(screen.queryByText("frontend")).not.toBeInTheDocument();

      // Rolled back after error
      await waitFor(() => {
        expect(screen.getByText("frontend")).toBeInTheDocument();
      });
    });

    it("shows error message from API failure on remove", async () => {
      const onRemoveLabel = vi
        .fn()
        .mockRejectedValue(new Error("Delete failed"));
      render(
        <LabelEditor
          {...defaultProps({ labels: ["frontend"], onRemoveLabel })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-label-frontend"));

      await waitFor(() => {
        expect(screen.getByTestId("label-error")).toHaveTextContent(
          "Delete failed",
        );
      });
    });
  });

  describe("keyboard shortcuts", () => {
    it("Enter key submits the label", async () => {
      const onAddLabel = vi.fn().mockResolvedValue(undefined);
      render(<LabelEditor {...defaultProps({ onAddLabel })} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "enter-label" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(onAddLabel).toHaveBeenCalledWith("enter-label");
      });
    });

    it("Escape key cancels add mode", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      expect(screen.getByTestId("add-label-form")).toBeInTheDocument();

      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Escape",
      });

      expect(screen.queryByTestId("add-label-form")).not.toBeInTheDocument();
      expect(screen.getByTestId("add-label-button")).toBeInTheDocument();
    });

    it("Escape key clears input value", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.change(screen.getByTestId("label-input"), {
        target: { value: "partial-input" },
      });
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Escape",
      });

      // Re-enter add mode
      fireEvent.click(screen.getByTestId("add-label-button"));
      const input = screen.getByTestId("label-input") as HTMLInputElement;
      expect(input.value).toBe("");
    });
  });

  describe("accessibility", () => {
    it("add button has aria-label", () => {
      render(<LabelEditor {...defaultProps()} />);

      expect(screen.getByTestId("add-label-button")).toHaveAttribute(
        "aria-label",
        "Add label",
      );
    });

    it("input has aria-label", () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));

      expect(screen.getByTestId("label-input")).toHaveAttribute(
        "aria-label",
        "Label name",
      );
    });

    it('error has role="alert"', () => {
      render(<LabelEditor {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-label-button"));
      fireEvent.keyDown(screen.getByTestId("label-input"), {
        key: "Enter",
      });

      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });

  describe("disabled state", () => {
    it("does not enter add mode when disabled", () => {
      render(<LabelEditor {...defaultProps({ disabled: true })} />);

      // No add button rendered at all
      expect(screen.queryByTestId("add-label-button")).not.toBeInTheDocument();
    });

    it("does not render remove buttons when disabled", () => {
      render(
        <LabelEditor
          {...defaultProps({ labels: ["frontend"], disabled: true })}
        />,
      );

      expect(
        screen.queryByTestId("remove-label-frontend"),
      ).not.toBeInTheDocument();
    });
  });

  describe("props sync", () => {
    it("syncs optimistic labels when prop changes", () => {
      const { rerender } = render(
        <LabelEditor {...defaultProps({ labels: ["old"] })} />,
      );

      expect(screen.getByText("old")).toBeInTheDocument();

      rerender(<LabelEditor {...defaultProps({ labels: ["old", "new"] })} />);

      expect(screen.getByText("old")).toBeInTheDocument();
      expect(screen.getByText("new")).toBeInTheDocument();
    });
  });
});
