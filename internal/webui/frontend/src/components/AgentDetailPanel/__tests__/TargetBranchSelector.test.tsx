/**
 * @vitest-environment jsdom
 */
import "@testing-library/jest-dom";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

import { TargetBranchSelector } from "../TargetBranchSelector";

describe("TargetBranchSelector", () => {
  const defaultProps = {
    currentTarget: "main",
    isWorkspace: false,
    onUpdate: vi.fn().mockResolvedValue(undefined),
    loading: false,
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("Read-only mode (non-workspace)", () => {
    it("displays the current target branch name", () => {
      render(<TargetBranchSelector {...defaultProps} />);
      expect(screen.getByText("main")).toBeInTheDocument();
    });

    it("does not show Change button", () => {
      render(<TargetBranchSelector {...defaultProps} />);
      expect(screen.queryByText("Change")).not.toBeInTheDocument();
    });

    it("does not show input field", () => {
      render(<TargetBranchSelector {...defaultProps} />);
      expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    });
  });

  describe("Workspace mode (not editing)", () => {
    it("displays the current target branch name", () => {
      render(<TargetBranchSelector {...defaultProps} isWorkspace={true} />);
      expect(screen.getByText("main")).toBeInTheDocument();
    });

    it("shows Change button", () => {
      render(<TargetBranchSelector {...defaultProps} isWorkspace={true} />);
      expect(screen.getByText("Change")).toBeInTheDocument();
    });

    it("disables Change button when loading", () => {
      render(
        <TargetBranchSelector
          {...defaultProps}
          isWorkspace={true}
          loading={true}
        />,
      );
      expect(screen.getByText("Change")).toBeDisabled();
    });

    it("enters edit mode when Change is clicked", () => {
      render(<TargetBranchSelector {...defaultProps} isWorkspace={true} />);
      fireEvent.click(screen.getByText("Change"));
      expect(screen.getByRole("textbox")).toBeInTheDocument();
    });
  });

  describe("Edit mode", () => {
    function renderInEditMode(overrides: Partial<typeof defaultProps> = {}) {
      const props = { ...defaultProps, isWorkspace: true, ...overrides };
      const result = render(<TargetBranchSelector {...props} />);
      fireEvent.click(screen.getByText("Change"));
      return result;
    }

    it("shows input with current target value", () => {
      renderInEditMode();
      const input = screen.getByRole("textbox") as HTMLInputElement;
      expect(input.value).toBe("main");
    });

    it("shows Save and Cancel buttons", () => {
      renderInEditMode();
      expect(screen.getByText("Save")).toBeInTheDocument();
      expect(screen.getByText("Cancel")).toBeInTheDocument();
    });

    it("calls onUpdate when Save is clicked with a changed value", async () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      renderInEditMode({ onUpdate });
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "develop" } });
      await act(async () => {
        fireEvent.click(screen.getByText("Save"));
      });
      await waitFor(() => {
        expect(onUpdate).toHaveBeenCalledWith("develop");
      });
    });

    it("does not call onUpdate when value is unchanged", async () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      renderInEditMode({ onUpdate });
      // Don't change the value, just click Save
      await act(async () => {
        fireEvent.click(screen.getByText("Save"));
      });
      expect(onUpdate).not.toHaveBeenCalled();
    });

    it("does not call onUpdate when value is whitespace only", async () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      renderInEditMode({ onUpdate });
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "   " } });
      await act(async () => {
        fireEvent.click(screen.getByText("Save"));
      });
      expect(onUpdate).not.toHaveBeenCalled();
    });

    it("trims whitespace before submitting", async () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      renderInEditMode({ onUpdate });
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "  develop  " } });
      await act(async () => {
        fireEvent.click(screen.getByText("Save"));
      });
      await waitFor(() => {
        expect(onUpdate).toHaveBeenCalledWith("develop");
      });
    });

    it("submits on Enter key", async () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      renderInEditMode({ onUpdate });
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "release" } });
      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });
      await waitFor(() => {
        expect(onUpdate).toHaveBeenCalledWith("release");
      });
    });

    it("cancels on Escape key and reverts value", () => {
      renderInEditMode();
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "changed" } });
      fireEvent.keyDown(input, { key: "Escape" });
      // Should exit edit mode, showing the original branch name
      expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
      expect(screen.getByText("main")).toBeInTheDocument();
    });

    it("cancels when Cancel button is clicked", () => {
      renderInEditMode();
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "changed" } });
      fireEvent.click(screen.getByText("Cancel"));
      expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
      expect(screen.getByText("main")).toBeInTheDocument();
    });

    it("disables Save button when value is empty", () => {
      renderInEditMode();
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "" } });
      expect(screen.getByText("Save")).toBeDisabled();
    });

    it("disables input when loading", () => {
      // First enter edit mode without loading, then rerender with loading
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      const { rerender } = render(
        <TargetBranchSelector
          currentTarget="main"
          isWorkspace={true}
          onUpdate={onUpdate}
          loading={false}
        />,
      );
      fireEvent.click(screen.getByText("Change"));
      expect(screen.getByRole("textbox")).toBeInTheDocument();
      rerender(
        <TargetBranchSelector
          currentTarget="main"
          isWorkspace={true}
          onUpdate={onUpdate}
          loading={true}
        />,
      );
      expect(screen.getByRole("textbox")).toBeDisabled();
    });

    it("shows '...' text on Save button when loading", () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      const { rerender } = render(
        <TargetBranchSelector
          currentTarget="main"
          isWorkspace={true}
          onUpdate={onUpdate}
          loading={false}
        />,
      );
      fireEvent.click(screen.getByText("Change"));
      rerender(
        <TargetBranchSelector
          currentTarget="main"
          isWorkspace={true}
          onUpdate={onUpdate}
          loading={true}
        />,
      );
      expect(screen.getByText("...")).toBeInTheDocument();
    });

    it("disables Cancel button when loading", () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      const { rerender } = render(
        <TargetBranchSelector
          currentTarget="main"
          isWorkspace={true}
          onUpdate={onUpdate}
          loading={false}
        />,
      );
      fireEvent.click(screen.getByText("Change"));
      rerender(
        <TargetBranchSelector
          currentTarget="main"
          isWorkspace={true}
          onUpdate={onUpdate}
          loading={true}
        />,
      );
      expect(screen.getByText("Cancel")).toBeDisabled();
    });

    it("exits edit mode after successful save", async () => {
      const onUpdate = vi.fn().mockResolvedValue(undefined);
      renderInEditMode({ onUpdate });
      const input = screen.getByRole("textbox");
      fireEvent.change(input, { target: { value: "develop" } });
      await act(async () => {
        fireEvent.click(screen.getByText("Save"));
      });
      await waitFor(() => {
        expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
      });
    });
  });
});
