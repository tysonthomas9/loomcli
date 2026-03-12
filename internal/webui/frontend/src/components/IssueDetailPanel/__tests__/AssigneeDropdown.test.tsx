/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for AssigneeDropdown component.
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

import { AssigneeDropdown } from "../AssigneeDropdown";

// Mock useRecentAssignees hook
const mockAddRecentAssignee = vi.fn();
const mockClearRecentAssignees = vi.fn();
let mockRecentAssignees: string[] = [];

vi.mock("@/hooks", () => ({
  useRecentAssignees: () => ({
    recentAssignees: mockRecentAssignees,
    addRecentAssignee: mockAddRecentAssignee,
    clearRecentAssignees: mockClearRecentAssignees,
  }),
}));

describe("AssigneeDropdown", () => {
  const defaultProps = {
    assignee: "[H] Alice" as string | undefined,
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockRecentAssignees = [];
  });

  describe("Display", () => {
    it('renders "Unassigned" when assignee is undefined', () => {
      render(<AssigneeDropdown {...defaultProps} assignee={undefined} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Unassigned");
    });

    it('renders "Unassigned" when assignee is empty string', () => {
      render(<AssigneeDropdown {...defaultProps} assignee="" />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Unassigned");
    });

    it("renders current assignee name with [H] prefix stripped", () => {
      render(<AssigneeDropdown {...defaultProps} assignee="[H] Alice" />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Alice");
    });

    it("renders assignee name without [H] prefix as-is", () => {
      render(<AssigneeDropdown {...defaultProps} assignee="Bot" />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Bot");
    });

    it("strips [H] prefix with extra spaces", () => {
      render(<AssigneeDropdown {...defaultProps} assignee="[H]  Bob" />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Bob");
    });

    it("renders dropdown arrow", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("\u25BE");
    });

    it("applies custom className", () => {
      render(<AssigneeDropdown {...defaultProps} className="custom-class" />);
      const container = screen.getByTestId(
        "assignee-dropdown-trigger",
      ).parentElement;
      expect(container).toHaveClass("custom-class");
    });

    it("sets data-unassigned attribute when no assignee", () => {
      render(<AssigneeDropdown {...defaultProps} assignee={undefined} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveAttribute("data-unassigned");
    });

    it("does not set data-unassigned attribute when assignee is set", () => {
      render(<AssigneeDropdown {...defaultProps} assignee="[H] Alice" />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).not.toHaveAttribute("data-unassigned");
    });
  });

  describe("Dropdown behavior", () => {
    it("opens dropdown menu on click", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-dropdown-menu")).toBeInTheDocument();
    });

    it("shows input field when open", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      expect(screen.getByTestId("assignee-input")).toBeInTheDocument();
      expect(screen.getByTestId("assignee-input")).toHaveAttribute(
        "placeholder",
        "Type a name...",
      );
    });

    it("shows submit button when open", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      expect(screen.getByTestId("assignee-submit")).toBeInTheDocument();
      expect(screen.getByTestId("assignee-submit")).toHaveTextContent("Assign");
    });

    it("submit button is disabled when input is empty", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      expect(screen.getByTestId("assignee-submit")).toBeDisabled();
    });

    it("submit button is enabled when input has text", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });
      expect(screen.getByTestId("assignee-submit")).not.toBeDisabled();
    });

    it("closes dropdown on Escape key", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-dropdown-menu")).toBeInTheDocument();

      fireEvent.keyDown(document, { key: "Escape" });
      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown on click outside", () => {
      render(
        <div>
          <AssigneeDropdown {...defaultProps} />
          <button data-testid="outside-button">Outside</button>
        </div>,
      );
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-dropdown-menu")).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId("outside-button"));
      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("toggles dropdown on repeated clicks", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-dropdown-menu")).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-dropdown-menu")).toBeInTheDocument();
    });

    it("returns focus to trigger when closed with Escape", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      fireEvent.click(trigger);

      fireEvent.keyDown(document, { key: "Escape" });
      expect(document.activeElement).toBe(trigger);
    });

    it("does not open when disabled", () => {
      render(<AssigneeDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("does not open when saving", () => {
      render(<AssigneeDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("clears input value when reopening", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      // Close
      fireEvent.keyDown(document, { key: "Escape" });

      // Reopen
      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-input")).toHaveValue("");
    });
  });

  describe("Input submission", () => {
    it("calls onSave with [H] prefix when submitting via button click", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("[H] Bob");
      });
    });

    it("calls onSave with [H] prefix when submitting via Enter key", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      const input = screen.getByTestId("assignee-input");
      fireEvent.change(input, { target: { value: "Charlie" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("[H] Charlie");
      });
    });

    it("does not call onSave when submitting empty input", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it("does not call onSave when submitting whitespace-only input", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "   " },
      });

      await act(async () => {
        fireEvent.keyDown(screen.getByTestId("assignee-input"), {
          key: "Enter",
        });
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it("closes dropdown after successful submission", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("adds name to recent assignees on submission", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Diana" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      expect(mockAddRecentAssignee).toHaveBeenCalledWith("Diana");
    });
  });

  describe("Recent assignees", () => {
    it("shows recent assignees section when recent names exist", () => {
      mockRecentAssignees = ["Alice", "Bob"];
      render(<AssigneeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      expect(screen.getByText("Recent")).toBeInTheDocument();
      expect(screen.getByTestId("recent-assignee-Alice")).toBeInTheDocument();
      expect(screen.getByTestId("recent-assignee-Bob")).toBeInTheDocument();
    });

    it("does not show recent section when no recent names", () => {
      mockRecentAssignees = [];
      render(<AssigneeDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      expect(screen.queryByText("Recent")).not.toBeInTheDocument();
    });

    it("calls onSave with [H] prefix when clicking a recent name", async () => {
      mockRecentAssignees = ["Alice", "Bob"];
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("recent-assignee-Bob"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("[H] Bob");
      });
    });

    it("adds recent name to recent assignees when clicked", async () => {
      mockRecentAssignees = ["Alice", "Bob"];
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("recent-assignee-Alice"));
      });

      expect(mockAddRecentAssignee).toHaveBeenCalledWith("Alice");
    });

    it("closes dropdown after clicking a recent name", async () => {
      mockRecentAssignees = ["Alice"];
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("recent-assignee-Alice"));
      });

      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Unassign", () => {
    it("shows Unassign button when assignee is set", () => {
      render(<AssigneeDropdown {...defaultProps} assignee="[H] Alice" />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      expect(screen.getByTestId("assignee-unassign")).toBeInTheDocument();
      expect(screen.getByTestId("assignee-unassign")).toHaveTextContent(
        "Unassign",
      );
    });

    it("does not show Unassign button when no assignee", () => {
      render(<AssigneeDropdown {...defaultProps} assignee={undefined} />);
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      expect(screen.queryByTestId("assignee-unassign")).not.toBeInTheDocument();
    });

    it("calls onSave with empty string when clicking Unassign", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-unassign"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("");
      });
    });

    it("does not add to recent assignees when unassigning", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-unassign"));
      });

      expect(mockAddRecentAssignee).not.toHaveBeenCalled();
    });

    it("closes dropdown after unassigning", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-unassign"));
      });

      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Escape key", () => {
    it("closes dropdown without calling onSave", () => {
      const onSave = vi.fn();
      render(<AssigneeDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      fireEvent.keyDown(document, { key: "Escape" });

      expect(
        screen.queryByTestId("assignee-dropdown-menu"),
      ).not.toBeInTheDocument();
      expect(onSave).not.toHaveBeenCalled();
    });

    it("clears input value on Escape", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      fireEvent.keyDown(document, { key: "Escape" });

      // Reopen and verify input is cleared
      fireEvent.click(trigger);
      expect(screen.getByTestId("assignee-input")).toHaveValue("");
    });
  });

  describe("Optimistic updates", () => {
    it("updates display immediately before save completes", async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      // Should show Bob immediately before save completes
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Bob");

      await act(async () => {
        resolvePromise!();
      });
    });

    it("shows Unassigned immediately when unassigning", async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-unassign"));
      });

      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveTextContent("Unassigned");

      await act(async () => {
        resolvePromise!();
      });
    });
  });

  describe("Error handling", () => {
    it("reverts to previous assignee on save failure", async () => {
      let rejectPromise: (error: Error) => void;
      const savePromise = new Promise<void>((_, reject) => {
        rejectPromise = reject;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      // Initially shows optimistic value
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveTextContent(
        "Bob",
      );

      // Reject the promise
      await act(async () => {
        rejectPromise!(new Error("Save failed"));
      });

      // After error, should revert to Alice
      await waitFor(() => {
        expect(
          screen.getByTestId("assignee-dropdown-trigger"),
        ).toHaveTextContent("Alice");
      });
    });

    it("displays error message on save failure", async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Network error"));
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("assignee-error")).toHaveTextContent(
          "Network error",
        );
      });
    });

    it("displays generic error for non-Error exceptions", async () => {
      const onSave = vi.fn().mockRejectedValue("string error");
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("assignee-error")).toHaveTextContent(
          "Failed to update assignee",
        );
      });
    });

    it('error has role="alert"', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Save failed"));
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(screen.getByRole("alert")).toHaveTextContent("Save failed");
      });
    });

    it("clears error when dropdown is opened", async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error("First error"))
        .mockResolvedValueOnce(undefined);

      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      // First attempt fails
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("assignee-error")).toBeInTheDocument();
      });

      // Open dropdown again - error should be cleared
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      expect(screen.queryByTestId("assignee-error")).not.toBeInTheDocument();
    });

    it("allows retry after failure", async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error("First error"))
        .mockResolvedValueOnce(undefined);

      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee="[H] Alice"
          onSave={onSave}
        />,
      );

      // First attempt fails
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("assignee-error")).toBeInTheDocument();
      });

      // Retry should succeed
      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(screen.queryByTestId("assignee-error")).not.toBeInTheDocument();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
    });

    it("reverts to unassigned on failed unassign-then-assign", async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Failed"));
      render(
        <AssigneeDropdown
          {...defaultProps}
          assignee={undefined}
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("assignee-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("assignee-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("assignee-submit"));
      });

      await waitFor(() => {
        expect(
          screen.getByTestId("assignee-dropdown-trigger"),
        ).toHaveTextContent("Unassigned");
      });
    });
  });

  describe("Loading state", () => {
    it("disables trigger when isSaving is true", () => {
      render(<AssigneeDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });

    it("shows saving indicator when isSaving is true", () => {
      render(<AssigneeDropdown {...defaultProps} isSaving />);
      expect(screen.getByTestId("assignee-saving")).toBeInTheDocument();
    });

    it("has aria-label on saving indicator", () => {
      render(<AssigneeDropdown {...defaultProps} isSaving />);
      const savingIndicator = screen.getByTestId("assignee-saving");
      expect(savingIndicator).toHaveAttribute("aria-label", "Saving...");
    });

    it("applies data-saving attribute to trigger", () => {
      render(<AssigneeDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveAttribute("data-saving", "true");
    });

    it("does not show saving indicator when not saving", () => {
      render(<AssigneeDropdown {...defaultProps} isSaving={false} />);
      expect(screen.queryByTestId("assignee-saving")).not.toBeInTheDocument();
    });

    it("does not apply data-saving attribute when not saving", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).not.toHaveAttribute("data-saving");
    });

    it("disables trigger when disabled prop is true", () => {
      render(<AssigneeDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });

    it("disables trigger when both disabled and isSaving are true", () => {
      render(<AssigneeDropdown {...defaultProps} disabled isSaving />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });
  });

  describe("Accessibility", () => {
    it("has aria-expanded attribute reflecting dropdown state", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");

      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    it('has aria-haspopup="true" on trigger', () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "true");
    });

    it("has aria-label on trigger with assignee name", () => {
      render(<AssigneeDropdown {...defaultProps} assignee="[H] Alice" />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Assignee: Alice. Click to change.",
      );
    });

    it("has aria-label on trigger with Unassigned", () => {
      render(<AssigneeDropdown {...defaultProps} assignee={undefined} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Assignee: Unassigned. Click to change.",
      );
    });

    it('trigger is a button with type="button"', () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      expect(trigger.tagName).toBe("BUTTON");
      expect(trigger).toHaveAttribute("type", "button");
    });
  });

  describe("Props sync", () => {
    it("syncs optimistic assignee when prop changes", () => {
      const { rerender } = render(
        <AssigneeDropdown {...defaultProps} assignee="[H] Alice" />,
      );
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveTextContent(
        "Alice",
      );

      rerender(<AssigneeDropdown {...defaultProps} assignee="[H] Bob" />);
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveTextContent(
        "Bob",
      );
    });

    it("syncs to Unassigned when prop changes to undefined", () => {
      const { rerender } = render(
        <AssigneeDropdown {...defaultProps} assignee="[H] Alice" />,
      );
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveTextContent(
        "Alice",
      );

      rerender(<AssigneeDropdown {...defaultProps} assignee={undefined} />);
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveTextContent(
        "Unassigned",
      );
    });

    it("updates aria-label when assignee prop changes", () => {
      const { rerender } = render(
        <AssigneeDropdown {...defaultProps} assignee="[H] Alice" />,
      );
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveAttribute(
        "aria-label",
        "Assignee: Alice. Click to change.",
      );

      rerender(<AssigneeDropdown {...defaultProps} assignee="[H] Bob" />);
      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveAttribute(
        "aria-label",
        "Assignee: Bob. Click to change.",
      );
    });
  });

  describe("Edge cases", () => {
    it("handles rapid assignee changes", () => {
      const { rerender } = render(
        <AssigneeDropdown {...defaultProps} assignee="[H] Alice" />,
      );

      rerender(<AssigneeDropdown {...defaultProps} assignee="[H] Bob" />);
      rerender(<AssigneeDropdown {...defaultProps} assignee="[H] Charlie" />);
      rerender(<AssigneeDropdown {...defaultProps} assignee={undefined} />);
      rerender(<AssigneeDropdown {...defaultProps} assignee="[H] Diana" />);

      expect(screen.getByTestId("assignee-dropdown-trigger")).toHaveTextContent(
        "Diana",
      );
    });

    it("handles undefined className gracefully", () => {
      render(<AssigneeDropdown {...defaultProps} className={undefined} />);
      const container = screen.getByTestId(
        "assignee-dropdown-trigger",
      ).parentElement;
      expect(container).toBeInTheDocument();
    });

    it("renders person icon in trigger", () => {
      render(<AssigneeDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("assignee-dropdown-trigger");
      const svg = trigger.querySelector("svg");
      expect(svg).toBeInTheDocument();
    });
  });
});
