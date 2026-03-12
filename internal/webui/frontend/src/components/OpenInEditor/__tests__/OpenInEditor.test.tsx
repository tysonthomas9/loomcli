/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for OpenInEditor component.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import "@testing-library/jest-dom";

import { OpenInEditor } from "../OpenInEditor";

// Mock the useEditors hook
const mockOpenEditor = vi.fn().mockResolvedValue(undefined);
const mockUseEditors = vi.fn();

vi.mock("@/hooks", () => ({
  useEditors: (...args: unknown[]) => mockUseEditors(...args),
}));

const defaultEditors = [
  {
    id: "vscode",
    display_name: "VS Code",
    icon_name: "vscode",
    detected: true,
  },
  { id: "cursor", display_name: "Cursor", icon_name: "cursor", detected: true },
  { id: "vim", display_name: "Vim", icon_name: "vim", detected: true },
];

function setupMock(overrides: Partial<ReturnType<typeof mockUseEditors>> = {}) {
  mockUseEditors.mockReturnValue({
    editors: defaultEditors,
    detectedEditors: defaultEditors,
    isLoading: false,
    error: null,
    refresh: vi.fn(),
    openEditor: mockOpenEditor,
    ...overrides,
  });
}

describe("OpenInEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    setupMock();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("Display", () => {
    it("renders trigger button with 'Open in\u2026' text", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveTextContent("Open in\u2026");
    });

    it("renders dropdown arrow", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveTextContent("\u25BE");
    });

    it("applies custom className", () => {
      render(<OpenInEditor path="/workspace" className="custom-class" />);
      const container = screen.getByTestId(
        "open-in-editor-trigger",
      ).parentElement;
      expect(container).toHaveClass("custom-class");
    });
  });

  describe("Dropdown behavior", () => {
    it("opens dropdown menu on click", () => {
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();
    });

    it("closes dropdown on Escape key", () => {
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();

      fireEvent.keyDown(document, { key: "Escape" });
      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes dropdown on click outside", () => {
      render(
        <div>
          <OpenInEditor path="/workspace" />
          <button data-testid="outside-button">Outside</button>
        </div>,
      );
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();

      fireEvent.mouseDown(screen.getByTestId("outside-button"));
      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();
    });

    it("toggles dropdown on repeated clicks", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();

      fireEvent.click(trigger);
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();
    });

    it("renders all detected editors in menu", () => {
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));

      expect(screen.getByTestId("editor-option-vscode")).toHaveTextContent(
        "VS Code",
      );
      expect(screen.getByTestId("editor-option-cursor")).toHaveTextContent(
        "Cursor",
      );
      expect(screen.getByTestId("editor-option-vim")).toHaveTextContent("Vim");
    });

    it("returns focus to trigger when closed with Escape", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      fireEvent.click(trigger);

      fireEvent.keyDown(document, { key: "Escape" });
      expect(document.activeElement).toBe(trigger);
    });
  });

  describe("Selection", () => {
    it("calls openEditor with selected editor and path", async () => {
      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-vscode"));
      });

      expect(mockOpenEditor).toHaveBeenCalledWith("vscode", "/workspace");
    });

    it("closes dropdown after selection", async () => {
      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();

      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-cursor"));
      });

      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();
    });

    it("shows 'Launching\u2026' state after selection", async () => {
      let resolveOpen: () => void;
      const openPromise = new Promise<void>((resolve) => {
        resolveOpen = resolve;
      });
      mockOpenEditor.mockReturnValue(openPromise);

      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-vscode"));
      });

      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveTextContent("Launching\u2026");
      expect(trigger).toHaveAttribute("data-launching", "true");

      // Resolve and advance timers to clear launching state
      await act(async () => {
        resolveOpen!();
      });
      await act(async () => {
        vi.advanceTimersByTime(1500);
      });

      expect(trigger).toHaveTextContent("Open in\u2026");
      expect(trigger).not.toHaveAttribute("data-launching");
    });

    it("reverts to 'Open in\u2026' after launching timeout", async () => {
      mockOpenEditor.mockResolvedValue(undefined);

      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-vscode"));
      });

      expect(screen.getByTestId("open-in-editor-trigger")).toHaveTextContent(
        "Launching\u2026",
      );

      await act(async () => {
        vi.advanceTimersByTime(1500);
      });

      expect(screen.getByTestId("open-in-editor-trigger")).toHaveTextContent(
        "Open in\u2026",
      );
    });
  });

  describe("Empty and loading states", () => {
    it("shows 'No editors detected' when detectedEditors is empty", () => {
      setupMock({ detectedEditors: [], editors: [] });
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));

      expect(screen.getByText("No editors detected")).toBeInTheDocument();
    });

    it("disables trigger when loading", () => {
      setupMock({ isLoading: true });
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toBeDisabled();
    });

    it("does not open when loading", () => {
      setupMock({ isLoading: true });
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();
    });

    it("disables when path is empty", () => {
      render(<OpenInEditor path="" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toBeDisabled();
    });

    it("does not open when path is empty", () => {
      render(<OpenInEditor path="" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();
    });

    it("disables when useEditors has error", () => {
      setupMock({ error: new Error("fetch failed") });
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toBeDisabled();
    });

    it("sets title attribute when useEditors has error", () => {
      setupMock({ error: new Error("fetch failed") });
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveAttribute("title", "Failed to load editors");
    });
  });

  describe("Error handling", () => {
    it("shows error message briefly on openEditor failure", async () => {
      mockOpenEditor.mockRejectedValue(new Error("Connection refused"));

      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-vscode"));
      });

      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveTextContent("Connection refused");
      expect(trigger).toHaveAttribute("data-error", "true");

      // After timeout, should revert
      await act(async () => {
        vi.advanceTimersByTime(2000);
      });

      expect(trigger).toHaveTextContent("Open in\u2026");
      expect(trigger).not.toHaveAttribute("data-error");
    });

    it("shows generic error for non-Error exceptions", async () => {
      mockOpenEditor.mockRejectedValue("string error");

      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-vscode"));
      });

      expect(screen.getByTestId("open-in-editor-trigger")).toHaveTextContent(
        "Failed to launch",
      );
    });
  });

  describe("Accessibility", () => {
    it("has aria-expanded attribute reflecting dropdown state", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    it('has aria-haspopup="listbox" on trigger', () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "listbox");
    });

    it("has aria-label on trigger", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Open in editor. Click to select.",
      );
    });

    it("has unavailable aria-label when disabled", () => {
      render(<OpenInEditor path="" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger).toHaveAttribute(
        "aria-label",
        "Open in editor (unavailable)",
      );
    });

    it('menu has role="listbox"', () => {
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(screen.getByRole("listbox")).toBeInTheDocument();
    });

    it("menu has aria-label", () => {
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(screen.getByRole("listbox")).toHaveAttribute(
        "aria-label",
        "Select editor",
      );
    });

    it('options have role="option"', () => {
      render(<OpenInEditor path="/workspace" />);
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(3);
    });

    it('trigger is a button with type="button"', () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");
      expect(trigger.tagName).toBe("BUTTON");
      expect(trigger).toHaveAttribute("type", "button");
    });
  });

  describe("Keyboard navigation", () => {
    it("opens dropdown with Enter key", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.keyDown(trigger, { key: "Enter" });
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();
    });

    it("opens dropdown with Space key", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.keyDown(trigger, { key: " " });
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();
    });

    it("navigates down with ArrowDown key", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);

      // Initially focused on first editor (index 0)
      expect(screen.getByTestId("editor-option-vscode")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("editor-option-cursor")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("editor-option-vim")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("navigates up with ArrowUp key", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      // Move to last
      fireEvent.keyDown(trigger, { key: "End" });
      expect(screen.getByTestId("editor-option-vim")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      expect(screen.getByTestId("editor-option-cursor")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("stops at first option when pressing ArrowUp", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("editor-option-vscode")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      expect(screen.getByTestId("editor-option-vscode")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("stops at last option when pressing ArrowDown", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: "End" });
      expect(screen.getByTestId("editor-option-vim")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      expect(screen.getByTestId("editor-option-vim")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("selects focused option with Enter key", async () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: "ArrowDown" });

      // Now focused on cursor (index 1)
      expect(screen.getByTestId("editor-option-cursor")).toHaveAttribute(
        "data-focused",
        "true",
      );

      await act(async () => {
        fireEvent.keyDown(trigger, { key: "Enter" });
      });

      expect(mockOpenEditor).toHaveBeenCalledWith("cursor", "/workspace");
    });

    it("navigates to first option with Home key", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      fireEvent.keyDown(trigger, { key: "End" });
      expect(screen.getByTestId("editor-option-vim")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "Home" });
      expect(screen.getByTestId("editor-option-vscode")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("navigates to last option with End key", () => {
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("editor-option-vscode")).toHaveAttribute(
        "data-focused",
        "true",
      );

      fireEvent.keyDown(trigger, { key: "End" });
      expect(screen.getByTestId("editor-option-vim")).toHaveAttribute(
        "data-focused",
        "true",
      );
    });

    it("does not navigate when no editors detected", () => {
      setupMock({ detectedEditors: [], editors: [] });
      render(<OpenInEditor path="/workspace" />);
      const trigger = screen.getByTestId("open-in-editor-trigger");

      fireEvent.click(trigger);

      // Arrow keys should be no-ops
      fireEvent.keyDown(trigger, { key: "ArrowDown" });
      fireEvent.keyDown(trigger, { key: "ArrowUp" });
      // Should not throw, menu stays open
      expect(screen.getByTestId("open-in-editor-menu")).toBeInTheDocument();
    });
  });

  describe("Edge cases", () => {
    it("handles undefined className gracefully", () => {
      render(<OpenInEditor path="/workspace" className={undefined} />);
      const container = screen.getByTestId(
        "open-in-editor-trigger",
      ).parentElement;
      expect(container).toBeInTheDocument();
    });

    it("ignores clicks during launching state", async () => {
      let resolveOpen: () => void;
      const openPromise = new Promise<void>((resolve) => {
        resolveOpen = resolve;
      });
      mockOpenEditor.mockReturnValue(openPromise);

      render(<OpenInEditor path="/workspace" />);

      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      await act(async () => {
        fireEvent.click(screen.getByTestId("editor-option-vscode"));
      });

      // In launching state — trigger should not open again
      fireEvent.click(screen.getByTestId("open-in-editor-trigger"));
      expect(
        screen.queryByTestId("open-in-editor-menu"),
      ).not.toBeInTheDocument();

      // Cleanup
      await act(async () => {
        resolveOpen!();
      });
      await act(async () => {
        vi.advanceTimersByTime(1500);
      });
    });
  });
});
