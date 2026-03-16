/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for RepoDropdown component.
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

import { RepoDropdown } from "./RepoDropdown";

describe("RepoDropdown", () => {
  const defaultProps = {
    currentRepo: "frontend" as string | null,
    repos: ["frontend", "backend", "infra"],
    onSave: vi.fn().mockResolvedValue(undefined),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("Display", () => {
    it("renders trigger with repo name when currentRepo is set", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="frontend" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveTextContent("frontend");
    });

    it('renders "No repo" when currentRepo is null', () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveTextContent("No repo");
    });

    it("renders dropdown arrow", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveTextContent("\u25BE");
    });

    it("applies custom className", () => {
      render(<RepoDropdown {...defaultProps} className="custom-class" />);
      const container = screen.getByTestId(
        "repo-dropdown-trigger",
      ).parentElement;
      expect(container).toHaveClass("custom-class");
    });

    it("renders repo icon SVG in trigger", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      const svg = trigger.querySelector("svg");
      expect(svg).toBeInTheDocument();
    });
  });

  describe("Dropdown behavior", () => {
    it("opens dropdown menu on click", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();
    });

    it("closes dropdown on Escape key", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();

      const menu = screen.getByTestId("repo-dropdown-menu");
      fireEvent.keyDown(menu, { key: "Escape" });
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown on click outside", () => {
      render(
        <div>
          <RepoDropdown {...defaultProps} />
          <button data-testid="outside-button">Outside</button>
        </div>,
      );
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId("outside-button"));
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown after selection", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("toggles dropdown on repeated clicks", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();
    });

    it('shows all repo options plus "None" in menu', () => {
      render(<RepoDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      expect(screen.getByTestId("repo-option-none")).toHaveTextContent("None");
      expect(screen.getByTestId("repo-option-frontend")).toHaveTextContent(
        "frontend",
      );
      expect(screen.getByTestId("repo-option-backend")).toHaveTextContent(
        "backend",
      );
      expect(screen.getByTestId("repo-option-infra")).toHaveTextContent(
        "infra",
      );
    });

    it("renders correct number of options (repos + None)", () => {
      render(<RepoDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(4); // None + 3 repos
    });

    it("returns focus to trigger when closed with Escape", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      fireEvent.click(trigger);

      const menu = screen.getByTestId("repo-dropdown-menu");
      fireEvent.keyDown(menu, { key: "Escape" });
      expect(document.activeElement).toBe(trigger);
    });

    it("does not open when disabled", () => {
      render(<RepoDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("does not open when saving", () => {
      render(<RepoDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("Selection", () => {
    it("calls onSave with repo name on option click", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("backend");
      });
    });

    it('calls onSave with null when "None" selected', async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-none"));
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith(null);
      });
    });

    it("shows checkmark on current option", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="frontend" />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const currentOption = screen.getByTestId("repo-option-frontend");
      expect(currentOption).toHaveTextContent("\u2713");
      expect(currentOption).toHaveAttribute("data-selected", "true");

      // Other options should not have checkmark
      expect(screen.getByTestId("repo-option-none")).not.toHaveTextContent(
        "\u2713",
      );
      expect(screen.getByTestId("repo-option-backend")).not.toHaveTextContent(
        "\u2713",
      );
      expect(screen.getByTestId("repo-option-infra")).not.toHaveTextContent(
        "\u2713",
      );
    });

    it('shows checkmark on "None" when currentRepo is null', () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const noneOption = screen.getByTestId("repo-option-none");
      expect(noneOption).toHaveTextContent("\u2713");
      expect(noneOption).toHaveAttribute("data-selected", "true");
    });

    it("updates display immediately (optimistic update)", async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      // Should show "backend" immediately before save completes
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveTextContent("backend");

      await act(async () => {
        resolvePromise!();
      });
    });

    it('shows "No repo" optimistically when selecting None', async () => {
      let resolvePromise: () => void;
      const savePromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);

      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-none"));
      });

      // Should show "No repo" immediately before save completes
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveTextContent("No repo");

      await act(async () => {
        resolvePromise!();
      });
    });

    it("does not call onSave when selecting same repo", async () => {
      const onSave = vi.fn();
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-frontend"));
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it("does not call onSave when selecting None and currentRepo is already null", async () => {
      const onSave = vi.fn();
      render(
        <RepoDropdown {...defaultProps} currentRepo={null} onSave={onSave} />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-none"));
      });

      expect(onSave).not.toHaveBeenCalled();
    });

    it("calls onSave for each different repo value", async () => {
      const repos = ["backend", "infra"];

      for (const targetRepo of repos) {
        const onSave = vi.fn().mockResolvedValue(undefined);
        const { unmount } = render(
          <RepoDropdown
            {...defaultProps}
            currentRepo="frontend"
            onSave={onSave}
          />,
        );

        fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
        await act(async () => {
          fireEvent.click(screen.getByTestId(`repo-option-${targetRepo}`));
        });

        await waitFor(() => {
          expect(onSave).toHaveBeenCalledWith(targetRepo);
        });

        unmount();
      }
    });
  });

  describe("Loading state", () => {
    it("disables trigger when isSaving is true", () => {
      render(<RepoDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });

    it("shows saving indicator when isSaving is true", () => {
      render(<RepoDropdown {...defaultProps} isSaving />);
      expect(screen.getByTestId("repo-saving")).toBeInTheDocument();
    });

    it("has aria-label on saving indicator", () => {
      render(<RepoDropdown {...defaultProps} isSaving />);
      const savingIndicator = screen.getByTestId("repo-saving");
      expect(savingIndicator).toHaveAttribute("aria-label", "Saving...");
    });

    it("applies data-saving attribute to trigger", () => {
      render(<RepoDropdown {...defaultProps} isSaving />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveAttribute("data-saving", "true");
    });

    it("does not show saving indicator when not saving", () => {
      render(<RepoDropdown {...defaultProps} isSaving={false} />);
      expect(screen.queryByTestId("repo-saving")).not.toBeInTheDocument();
    });

    it("does not apply data-saving attribute when not saving", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).not.toHaveAttribute("data-saving");
    });

    it("disables trigger when disabled prop is true", () => {
      render(<RepoDropdown {...defaultProps} disabled />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });

    it("disables trigger when both disabled and isSaving are true", () => {
      render(<RepoDropdown {...defaultProps} disabled isSaving />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toBeDisabled();
    });
  });

  describe("Error handling", () => {
    it("reverts to previous repo on save failure", async () => {
      let rejectPromise: (error: Error) => void;
      const savePromise = new Promise<void>((_, reject) => {
        rejectPromise = reject;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      // Initially shows optimistic value
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "backend",
      );

      // Reject the promise
      await act(async () => {
        rejectPromise!(new Error("Save failed"));
      });

      // After error, should revert
      await waitFor(() => {
        expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
          "frontend",
        );
      });
    });

    it('reverts to "No repo" display when save fails and currentRepo was null', async () => {
      let rejectPromise: (error: Error) => void;
      const savePromise = new Promise<void>((_, reject) => {
        rejectPromise = reject;
      });
      const onSave = vi.fn().mockReturnValue(savePromise);
      render(
        <RepoDropdown {...defaultProps} currentRepo={null} onSave={onSave} />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      // Initially shows optimistic value
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "backend",
      );

      // Reject the promise
      await act(async () => {
        rejectPromise!(new Error("Save failed"));
      });

      // After error, should revert to "No repo"
      await waitFor(() => {
        expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
          "No repo",
        );
      });
    });

    it("displays error message on save failure", async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Network error"));
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("repo-error")).toHaveTextContent(
          "Network error",
        );
      });
    });

    it("displays generic error for non-Error exceptions", async () => {
      const onSave = vi.fn().mockRejectedValue("string error");
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("repo-error")).toHaveTextContent(
          "Failed to update repo",
        );
      });
    });

    it('error has role="alert"', async () => {
      const onSave = vi.fn().mockRejectedValue(new Error("Save failed"));
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
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
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      // First attempt fails
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("repo-error")).toBeInTheDocument();
      });

      // Open dropdown again - error should be cleared
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      expect(screen.queryByTestId("repo-error")).not.toBeInTheDocument();
    });

    it("allows retry after failure", async () => {
      const onSave = vi
        .fn()
        .mockRejectedValueOnce(new Error("First error"))
        .mockResolvedValueOnce(undefined);

      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      // First attempt fails
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      await waitFor(() => {
        expect(screen.getByTestId("repo-error")).toBeInTheDocument();
      });

      // Retry should succeed
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      await waitFor(() => {
        expect(screen.queryByTestId("repo-error")).not.toBeInTheDocument();
      });
      expect(onSave).toHaveBeenCalledTimes(2);
    });
  });

  describe("Accessibility", () => {
    it("has aria-expanded attribute reflecting dropdown state", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    it('has aria-haspopup="listbox" on trigger', () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "listbox");
    });

    it("has aria-label on trigger when repo is set", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="frontend" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Repo: frontend. Click to change.",
      );
    });

    it("has aria-label on trigger when repo is null", () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Repo: No repo. Click to change.",
      );
    });

    it('menu has role="listbox"', () => {
      render(<RepoDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      expect(screen.getByRole("listbox")).toBeInTheDocument();
    });

    it("menu has aria-label", () => {
      render(<RepoDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      expect(screen.getByRole("listbox")).toHaveAttribute(
        "aria-label",
        "Select repo",
      );
    });

    it('options have role="option"', () => {
      render(<RepoDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(4); // None + 3 repos
    });

    it('current option has aria-selected="true"', () => {
      render(<RepoDropdown {...defaultProps} currentRepo="frontend" />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const options = screen.getAllByRole("option");
      // Options order: None (0), frontend (1), backend (2), infra (3)
      expect(options[0]).toHaveAttribute("aria-selected", "false");
      expect(options[1]).toHaveAttribute("aria-selected", "true");
      expect(options[2]).toHaveAttribute("aria-selected", "false");
      expect(options[3]).toHaveAttribute("aria-selected", "false");
    });

    it('None option has aria-selected="true" when currentRepo is null', () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const options = screen.getAllByRole("option");
      expect(options[0]).toHaveAttribute("aria-selected", "true");
      expect(options[1]).toHaveAttribute("aria-selected", "false");
    });

    it('trigger is a button with type="button"', () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");
      expect(trigger.tagName).toBe("BUTTON");
      expect(trigger).toHaveAttribute("type", "button");
    });
  });

  describe("Keyboard navigation", () => {
    it("opens dropdown with Enter key", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.keyDown(trigger, { key: "Enter" });
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();
    });

    it("opens dropdown with Space key", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.keyDown(trigger, { key: " " });
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();
    });

    it("navigates down with ArrowDown key", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="frontend" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);

      // Initially focused on current repo (frontend, index 1)
      expect(screen.getByTestId("repo-option-frontend")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("repo-option-backend")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("navigates up with ArrowUp key", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="infra" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);

      // Initially focused on current repo (infra, index 3)
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      expect(screen.getByTestId("repo-option-backend")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      expect(screen.getByTestId("repo-option-frontend")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("stops at first option when pressing ArrowUp", () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      // Focused on None (index 0)
      expect(screen.getByTestId("repo-option-none")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      expect(screen.getByTestId("repo-option-none")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("stops at last option when pressing ArrowDown", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="infra" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("selects focused option with Enter key", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      fireEvent.keyDown(trigger, { key: "ArrowDown" });

      // Now focused on index 3 (infra)
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );

      await act(async () => {
        fireEvent.keyDown(trigger, { key: "Enter" });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith("infra");
      });
    });

    it("selects focused option with Space key", async () => {
      const onSave = vi.fn().mockResolvedValue(undefined);
      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: "ArrowUp" });

      // Now focused on index 0 (None)
      expect(screen.getByTestId("repo-option-none")).toHaveAttribute(
        "data-focused",
        "true",
      );

      await act(async () => {
        fireEvent.keyDown(trigger, { key: " " });
      });

      await waitFor(() => {
        expect(onSave).toHaveBeenCalledWith(null);
      });
    });

    it("closes dropdown with Escape key", () => {
      render(<RepoDropdown {...defaultProps} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();

      const menu = screen.getByTestId("repo-dropdown-menu");
      fireEvent.keyDown(menu, { key: "Escape" });
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();
    });

    it("navigates to first option with Home key", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="infra" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "Home" });
      expect(screen.getByTestId("repo-option-none")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("navigates to last option with End key", () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-option-none")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "End" });
      expect(screen.getByTestId("repo-option-infra")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("sets initial focus to current repo when opening", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="backend" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-option-backend")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("sets initial focus to None when currentRepo is null", () => {
      render(<RepoDropdown {...defaultProps} currentRepo={null} />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("repo-option-none")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("resets focus when dropdown closes", () => {
      render(<RepoDropdown {...defaultProps} currentRepo="frontend" />);
      const trigger = screen.getByTestId("repo-dropdown-trigger");

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("repo-option-backend")).toHaveAttribute(
        "data-focused",
        "true",
      );

      // Close and reopen
      const menu = screen.getByTestId("repo-dropdown-menu");
      fireEvent.keyDown(menu, { key: "Escape" });
      fireEvent.click(trigger);

      // Should be back to current repo
      expect(screen.getByTestId("repo-option-frontend")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });
  });

  describe("Props sync", () => {
    it("syncs optimistic repo when prop changes", () => {
      const { rerender } = render(
        <RepoDropdown {...defaultProps} currentRepo="frontend" />,
      );
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "frontend",
      );

      rerender(<RepoDropdown {...defaultProps} currentRepo="backend" />);
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "backend",
      );
    });

    it("syncs to null when currentRepo prop changes to null", () => {
      const { rerender } = render(
        <RepoDropdown {...defaultProps} currentRepo="frontend" />,
      );
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "frontend",
      );

      rerender(<RepoDropdown {...defaultProps} currentRepo={null} />);
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "No repo",
      );
    });

    it("updates checkmark when currentRepo prop changes", () => {
      const { rerender } = render(
        <RepoDropdown {...defaultProps} currentRepo="frontend" />,
      );

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      expect(screen.getByTestId("repo-option-frontend")).toHaveAttribute(
        "data-selected",
        "true",
      );

      // Close and update repo
      const menu = screen.getByTestId("repo-dropdown-menu");
      fireEvent.keyDown(menu, { key: "Escape" });
      rerender(<RepoDropdown {...defaultProps} currentRepo="backend" />);

      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      expect(screen.getByTestId("repo-option-backend")).toHaveAttribute(
        "data-selected",
        "true",
      );
      expect(screen.getByTestId("repo-option-frontend")).not.toHaveAttribute(
        "data-selected",
      );
    });

    it("updates aria-label when currentRepo prop changes", () => {
      const { rerender } = render(
        <RepoDropdown {...defaultProps} currentRepo="frontend" />,
      );
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveAttribute(
        "aria-label",
        "Repo: frontend. Click to change.",
      );

      rerender(<RepoDropdown {...defaultProps} currentRepo="backend" />);
      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveAttribute(
        "aria-label",
        "Repo: backend. Click to change.",
      );
    });
  });

  describe("Edge cases", () => {
    it("handles rapid repo prop changes", () => {
      const { rerender } = render(
        <RepoDropdown {...defaultProps} currentRepo="frontend" />,
      );

      rerender(<RepoDropdown {...defaultProps} currentRepo="backend" />);
      rerender(<RepoDropdown {...defaultProps} currentRepo="infra" />);
      rerender(<RepoDropdown {...defaultProps} currentRepo={null} />);
      rerender(<RepoDropdown {...defaultProps} currentRepo="frontend" />);

      expect(screen.getByTestId("repo-dropdown-trigger")).toHaveTextContent(
        "frontend",
      );
    });

    it("handles click on menu without selecting (click on menu background)", async () => {
      render(<RepoDropdown {...defaultProps} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const menu = screen.getByTestId("repo-dropdown-menu");
      fireEvent.click(menu);

      // Menu should still be open (click on menu itself, not an option)
      expect(screen.getByTestId("repo-dropdown-menu")).toBeInTheDocument();
    });

    it("does not fire multiple saves for rapid selections", async () => {
      let resolveFirst: () => void;
      const firstPromise = new Promise<void>((resolve) => {
        resolveFirst = resolve;
      });
      const onSave = vi
        .fn()
        .mockReturnValueOnce(firstPromise)
        .mockResolvedValue(undefined);

      render(
        <RepoDropdown
          {...defaultProps}
          currentRepo="frontend"
          onSave={onSave}
        />,
      );

      // First selection
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("repo-option-backend"));
      });

      // Dropdown is closed after first selection
      expect(
        screen.queryByTestId("repo-dropdown-menu"),
      ).not.toBeInTheDocument();

      // Resolve first save
      await act(async () => {
        resolveFirst!();
      });

      expect(onSave).toHaveBeenCalledTimes(1);
    });

    it("handles undefined className gracefully", () => {
      render(<RepoDropdown {...defaultProps} className={undefined} />);
      const container = screen.getByTestId(
        "repo-dropdown-trigger",
      ).parentElement;
      expect(container).toBeInTheDocument();
    });

    it("handles empty repos array", () => {
      render(<RepoDropdown {...defaultProps} repos={[]} currentRepo={null} />);
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      // Should only show "None" option
      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(1);
      expect(screen.getByTestId("repo-option-none")).toHaveTextContent("None");
    });

    it("handles single repo in array", () => {
      render(
        <RepoDropdown
          {...defaultProps}
          repos={["only-repo"]}
          currentRepo="only-repo"
        />,
      );
      fireEvent.click(screen.getByTestId("repo-dropdown-trigger"));

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(2); // None + only-repo
    });
  });
});
