/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for OwnerDropdown component.
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

import { OwnerDropdown } from "../OwnerDropdown";

// Mock useRecentOwners hook
const mockAddRecentOwner = vi.fn();
const mockClearRecentOwners = vi.fn();
let mockRecentOwners: string[] = [];

vi.mock("@/hooks/useRecentOwners", () => ({
  useRecentOwners: () => ({
    recentOwners: mockRecentOwners,
    addRecentOwner: mockAddRecentOwner,
    clearRecentOwners: mockClearRecentOwners,
  }),
}));

describe("OwnerDropdown", () => {
  const defaultProps = {
    owner: "Alice" as string | undefined,
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockRecentOwners = [];
  });

  describe("Display", () => {
    it('renders "No owner" when owner is undefined', () => {
      render(<OwnerDropdown {...defaultProps} owner={undefined} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveTextContent("No owner");
    });

    it('renders "No owner" when owner is empty string', () => {
      render(<OwnerDropdown {...defaultProps} owner="" />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveTextContent("No owner");
    });

    it("renders current owner name in trigger button", () => {
      render(<OwnerDropdown {...defaultProps} owner="Alice" />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveTextContent("Alice");
    });

    it("renders dropdown arrow", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveTextContent("\u25BE");
    });

    it("applies custom className", () => {
      render(<OwnerDropdown {...defaultProps} className="custom-class" />);
      const container = screen.getByTestId(
        "owner-dropdown-trigger",
      ).parentElement;
      expect(container).toHaveClass("custom-class");
    });

    it("sets data-unset attribute when no owner", () => {
      render(<OwnerDropdown {...defaultProps} owner={undefined} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveAttribute("data-unset");
    });

    it("does not set data-unset attribute when owner is set", () => {
      render(<OwnerDropdown {...defaultProps} owner="Alice" />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).not.toHaveAttribute("data-unset");
    });
  });

  describe("Dropdown behavior", () => {
    it("opens dropdown menu on click", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-dropdown-menu")).toBeInTheDocument();
    });

    it("shows input field when open", () => {
      render(<OwnerDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      expect(screen.getByTestId("owner-input")).toBeInTheDocument();
      expect(screen.getByTestId("owner-input")).toHaveAttribute(
        "placeholder",
        "Type owner name...",
      );
    });

    it("shows submit button when open", () => {
      render(<OwnerDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      expect(screen.getByTestId("owner-submit")).toBeInTheDocument();
      expect(screen.getByTestId("owner-submit")).toHaveTextContent("Set");
    });

    it("submit button is disabled when input is empty", () => {
      render(<OwnerDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      expect(screen.getByTestId("owner-submit")).toBeDisabled();
    });

    it("submit button is enabled when input has text", () => {
      render(<OwnerDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });
      expect(screen.getByTestId("owner-submit")).not.toBeDisabled();
    });

    it("closes dropdown on Escape key", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-dropdown-menu")).toBeInTheDocument();

      fireEvent.keyDown(document, { key: "Escape" });
      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown on click outside", () => {
      render(
        <div>
          <OwnerDropdown {...defaultProps} />
          <button data-testid="outside-button">Outside</button>
        </div>,
      );
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-dropdown-menu")).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId("outside-button"));
      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("toggles dropdown on repeated clicks", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-dropdown-menu")).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-dropdown-menu")).toBeInTheDocument();
    });

    it("returns focus to trigger when closed with Escape", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      fireEvent.click(trigger);

      fireEvent.keyDown(document, { key: "Escape" });
      expect(document.activeElement).toBe(trigger);
    });

    it("does not open when disabled", () => {
      render(<OwnerDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("does not open when saving", () => {
      render(<OwnerDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("clears input value when reopening", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      // Close
      fireEvent.keyDown(document, { key: "Escape" });

      // Reopen
      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-input")).toHaveValue("");
    });
  });

  describe("Input submission", () => {
    it("calls onSave with owner name when submitting via button click", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("Bob");
      });
    });

    it("calls onSave with owner name when submitting via Enter key", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      const input = screen.getByTestId("owner-input");
      fireEvent.change(input, { target: { value: "Charlie" } });

      await act(async () => {
        fireEvent.keyDown(input, { key: "Enter" });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("Charlie");
      });
    });

    it("does not call onSave when submitting empty input", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it("does not call onSave when submitting whitespace-only input", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "   " },
      });

      await act(async () => {
        fireEvent.keyDown(screen.getByTestId("owner-input"), {
          key: "Enter",
        });
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it("closes dropdown after successful submission", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("adds name to recent owners on submission", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Diana" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      expect(mockAddRecentOwner).toHaveBeenCalledWith("Diana");
    });
  });

  describe("Recent owners", () => {
    it("shows recent owners section when recent names exist", () => {
      mockRecentOwners = ["Alice", "Bob"];
      render(<OwnerDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      expect(screen.getByText("Recent")).toBeInTheDocument();
      expect(screen.getByTestId("recent-owner-Alice")).toBeInTheDocument();
      expect(screen.getByTestId("recent-owner-Bob")).toBeInTheDocument();
    });

    it("does not show recent section when no recent names", () => {
      mockRecentOwners = [];
      render(<OwnerDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      expect(screen.queryByText("Recent")).not.toBeInTheDocument();
    });

    it("calls onSave with name when clicking a recent owner", async () => {
      mockRecentOwners = ["Alice", "Bob"];
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("recent-owner-Bob"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("Bob");
      });
    });

    it("adds recent name to recent owners when clicked", async () => {
      mockRecentOwners = ["Alice", "Bob"];
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("recent-owner-Alice"));
      });

      expect(mockAddRecentOwner).toHaveBeenCalledWith("Alice");
    });

    it("closes dropdown after clicking a recent name", async () => {
      mockRecentOwners = ["Alice"];
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("recent-owner-Alice"));
      });

      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Remove owner", () => {
    it("shows Remove owner button when owner is set", () => {
      render(<OwnerDropdown {...defaultProps} owner="Alice" />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      expect(screen.getByTestId("owner-remove")).toBeInTheDocument();
      expect(screen.getByTestId("owner-remove")).toHaveTextContent(
        "Remove owner",
      );
    });

    it("does not show Remove owner button when no owner", () => {
      render(<OwnerDropdown {...defaultProps} owner={undefined} />);
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      expect(screen.queryByTestId("owner-remove")).not.toBeInTheDocument();
    });

    it("calls onSave with empty string when clicking Remove owner", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-remove"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("");
      });
    });

    it("does not add to recent owners when removing", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-remove"));
      });

      expect(mockAddRecentOwner).not.toHaveBeenCalled();
    });

    it("closes dropdown after removing owner", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-remove"));
      });

      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Escape key", () => {
    it("closes dropdown without calling onSave", () => {
      const onSave = vi.fn();
      render(<OwnerDropdown {...defaultProps} onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      fireEvent.keyDown(document, { key: "Escape" });

      expect(
        screen.queryByTestId("owner-dropdown-menu"),
      ).not.toBeInTheDocument();
      expect(onSave).not.toHaveBeenCalled();
    });

    it("clears input value on Escape", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      fireEvent.keyDown(document, { key: "Escape" });

      // Reopen and verify input is cleared
      fireEvent.click(trigger);
      expect(screen.getByTestId("owner-input")).toHaveValue("");
    });
  });

  describe("Optimistic updates", () => {
    it("updates display immediately before save completes", async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      // Should show Bob immediately before save completes
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveTextContent("Bob");

      await act(async () => {
        resolvePromise!();
      });
    });

    it("shows No owner immediately when removing owner", async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-remove"));
      });

      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveTextContent("No owner");

      await act(async () => {
        resolvePromise!();
      });
    });
  });

  describe("Error handling", () => {
    it("reverts to previous owner on save failure", async () => {
      let rejectPromise: (error: Error) => void;
      const savePromise = new Promise<void>((_, reject) => {
        rejectPromise = reject;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      // Initially shows optimistic value
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
        "Bob",
      );

      // Reject the promise
      await act(async () => {
        rejectPromise!(new Error("Save failed"));
      });

      // After error, should revert to Alice
      await waitFor(() => {
        expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
          "Alice",
        );
      });
    });

    it("displays error message on save failure", async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Network error"));
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("owner-error")).toHaveTextContent(
          "Network error",
        );
      });
    });

    it("displays generic error for non-Error exceptions", async () => {
      const onSave = vi.fn().mockRejectedValue("string error");
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("owner-error")).toHaveTextContent(
          "Failed to update owner",
        );
      });
    });

    it('error has role="alert"', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Save failed"));
      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
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

      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("owner-error")).toBeInTheDocument();
      });

      // Open dropdown again - error should be cleared
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      expect(screen.queryByTestId("owner-error")).not.toBeInTheDocument();
    });

    it("allows retry after failure", async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error("First error"))
        .mockResolvedValueOnce(undefined);

      render(<OwnerDropdown {...defaultProps} owner="Alice" onSave={onSave} />);

      // First attempt fails
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("owner-error")).toBeInTheDocument();
      });

      // Retry should succeed
      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(screen.queryByTestId("owner-error")).not.toBeInTheDocument();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
    });

    it("reverts to no owner on failed set-from-unset", async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Failed"));
      render(
        <OwnerDropdown {...defaultProps} owner={undefined} onSave={onSave} />,
      );

      fireEvent.click(screen.getByTestId("owner-dropdown-trigger"));
      fireEvent.change(screen.getByTestId("owner-input"), {
        target: { value: "Bob" },
      });

      await act(async () => {
        fireEvent.click(screen.getByTestId("owner-submit"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
          "No owner",
        );
      });
    });
  });

  describe("Loading state", () => {
    it("disables trigger when isSaving is true", () => {
      render(<OwnerDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });

    it("shows saving indicator when isSaving is true", () => {
      render(<OwnerDropdown {...defaultProps} isSaving />);
      expect(screen.getByTestId("owner-saving")).toBeInTheDocument();
    });

    it("has aria-label on saving indicator", () => {
      render(<OwnerDropdown {...defaultProps} isSaving />);
      const savingIndicator = screen.getByTestId("owner-saving");
      expect(savingIndicator).toHaveAttribute("aria-label", "Saving...");
    });

    it("applies data-saving attribute to trigger", () => {
      render(<OwnerDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveAttribute("data-saving", "true");
    });

    it("does not show saving indicator when not saving", () => {
      render(<OwnerDropdown {...defaultProps} isSaving={false} />);
      expect(screen.queryByTestId("owner-saving")).not.toBeInTheDocument();
    });

    it("does not apply data-saving attribute when not saving", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).not.toHaveAttribute("data-saving");
    });

    it("disables trigger when disabled prop is true", () => {
      render(<OwnerDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });

    it("disables trigger when both disabled and isSaving are true", () => {
      render(<OwnerDropdown {...defaultProps} disabled isSaving />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });
  });

  describe("Accessibility", () => {
    it("has aria-expanded attribute reflecting dropdown state", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");

      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    it('has aria-haspopup="true" on trigger', () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "true");
    });

    it("has aria-label on trigger with owner name", () => {
      render(<OwnerDropdown {...defaultProps} owner="Alice" />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Owner: Alice. Click to change.",
      );
    });

    it("has aria-label on trigger with No owner", () => {
      render(<OwnerDropdown {...defaultProps} owner={undefined} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Owner: No owner. Click to change.",
      );
    });

    it('trigger is a button with type="button"', () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      expect(trigger.tagName).toBe("BUTTON");
      expect(trigger).toHaveAttribute("type", "button");
    });
  });

  describe("Props sync", () => {
    it("syncs optimistic owner when prop changes", () => {
      const { rerender } = render(
        <OwnerDropdown {...defaultProps} owner="Alice" />,
      );
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
        "Alice",
      );

      rerender(<OwnerDropdown {...defaultProps} owner="Bob" />);
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
        "Bob",
      );
    });

    it("syncs to No owner when prop changes to undefined", () => {
      const { rerender } = render(
        <OwnerDropdown {...defaultProps} owner="Alice" />,
      );
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
        "Alice",
      );

      rerender(<OwnerDropdown {...defaultProps} owner={undefined} />);
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
        "No owner",
      );
    });

    it("updates aria-label when owner prop changes", () => {
      const { rerender } = render(
        <OwnerDropdown {...defaultProps} owner="Alice" />,
      );
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveAttribute(
        "aria-label",
        "Owner: Alice. Click to change.",
      );

      rerender(<OwnerDropdown {...defaultProps} owner="Bob" />);
      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveAttribute(
        "aria-label",
        "Owner: Bob. Click to change.",
      );
    });
  });

  describe("Edge cases", () => {
    it("handles rapid owner changes", () => {
      const { rerender } = render(
        <OwnerDropdown {...defaultProps} owner="Alice" />,
      );

      rerender(<OwnerDropdown {...defaultProps} owner="Bob" />);
      rerender(<OwnerDropdown {...defaultProps} owner="Charlie" />);
      rerender(<OwnerDropdown {...defaultProps} owner={undefined} />);
      rerender(<OwnerDropdown {...defaultProps} owner="Diana" />);

      expect(screen.getByTestId("owner-dropdown-trigger")).toHaveTextContent(
        "Diana",
      );
    });

    it("handles undefined className gracefully", () => {
      render(<OwnerDropdown {...defaultProps} className={undefined} />);
      const container = screen.getByTestId(
        "owner-dropdown-trigger",
      ).parentElement;
      expect(container).toBeInTheDocument();
    });

    it("renders shield icon in trigger", () => {
      render(<OwnerDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("owner-dropdown-trigger");
      const svg = trigger.querySelector("svg");
      expect(svg).toBeInTheDocument();
    });
  });
});
