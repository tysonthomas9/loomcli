/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for DependencySearchPicker component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";
import type { Issue } from "@/types";

// scrollIntoView is not available in jsdom
Element.prototype.scrollIntoView = vi.fn();

import {
  DependencySearchPicker,
  type DependencySearchPickerProps,
} from "../DependencySearchPicker";

// Mock the hooks barrel - provide both useDebounce and useIssueSearch
const mockSearch = vi.fn();
const mockResults: Issue[] = [];
let mockIsLoading = false;

vi.mock("@/hooks", async (importOriginal) => {
  const original = await importOriginal<typeof import("@/hooks")>();
  return {
    ...original,
    useDebounce: (value: string) => value,
    useIssueSearch: () => ({
      results: mockResults,
      isLoading: mockIsLoading,
      search: mockSearch,
      query: "",
    }),
    useRegisterEscapeLayer: vi.fn(),
    useKeyboardShortcuts: vi.fn(() => ({
      isCheatsheetOpen: false,
      toggleCheatsheet: vi.fn(),
      closeCheatsheet: vi.fn(),
    })),
    KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
      children,
    LAYER_CONFIRM_DIALOG: 60,
    LAYER_TOAST: 50,
    LAYER_CHEATSHEET: 45,
    LAYER_MODAL: 40,
    LAYER_TERMINAL_PANEL: 30,
    LAYER_AGENT_PANEL: 20,
    LAYER_ISSUE_PANEL: 10,
    LAYER_TERMINAL_SEARCH: 5,
  };
});

/** Helper to create a test issue */
function createIssue(id: string, title: string): Issue {
  return {
    id,
    title,
    priority: 2,
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
  } as Issue;
}

// Default test props
function defaultProps(
  overrides?: Partial<DependencySearchPickerProps>,
): DependencySearchPickerProps {
  return {
    issueId: "current-issue",
    existingDependencyIds: [],
    onSelect: vi.fn(),
    onCancel: vi.fn(),
    ...overrides,
  };
}

/** Helper to set mock results for tests */
function setMockResults(issues: Issue[]) {
  // Mutate the array in place so the mock reference stays the same
  mockResults.length = 0;
  mockResults.push(...issues);
}

describe("DependencySearchPicker", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setMockResults([]);
    mockIsLoading = false;
  });

  describe("rendering", () => {
    it("renders search input", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(
        screen.getByTestId("dependency-search-picker"),
      ).toBeInTheDocument();
      expect(screen.getByTestId("dependency-search-input")).toBeInTheDocument();
    });

    it("renders cancel and add buttons", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(screen.getByTestId("cancel-add-dependency")).toBeInTheDocument();
      expect(screen.getByTestId("confirm-add-dependency")).toBeInTheDocument();
    });

    it("focuses input on mount", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(screen.getByTestId("dependency-search-input")).toHaveFocus();
    });

    it("has placeholder text", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(screen.getByTestId("dependency-search-input")).toHaveAttribute(
        "placeholder",
        "Search by ID or title...",
      );
    });

    it("has accessible aria-label on input", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(screen.getByTestId("dependency-search-input")).toHaveAttribute(
        "aria-label",
        "Search issues",
      );
    });

    it("disables confirm button when input is empty", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(screen.getByTestId("confirm-add-dependency")).toBeDisabled();
    });

    it("enables confirm button when input has value", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "some-id" },
      });

      expect(screen.getByTestId("confirm-add-dependency")).not.toBeDisabled();
    });
  });

  describe("search results", () => {
    it("shows dropdown when query has content and results exist", () => {
      setMockResults([
        createIssue("issue-1", "Fix login bug"),
        createIssue("issue-2", "Add feature"),
      ]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "fix" },
      });

      expect(screen.getByTestId("search-results-dropdown")).toBeInTheDocument();
      expect(screen.getByTestId("search-result-issue-1")).toBeInTheDocument();
      expect(screen.getByTestId("search-result-issue-2")).toBeInTheDocument();
    });

    it("shows no results message when search matches nothing", () => {
      setMockResults([]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "nonexistent" },
      });

      expect(screen.getByTestId("no-search-results")).toBeInTheDocument();
    });

    it("does not show dropdown when input is empty", () => {
      render(<DependencySearchPicker {...defaultProps()} />);

      expect(
        screen.queryByTestId("search-results-dropdown"),
      ).not.toBeInTheDocument();
    });

    it("displays issue ID and title in results", () => {
      setMockResults([createIssue("issue-1", "Fix login bug")]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "fix" },
      });

      expect(screen.getByText("issue-1")).toBeInTheDocument();
      expect(screen.getByText("Fix login bug")).toBeInTheDocument();
    });
  });

  describe("filtering", () => {
    it("excludes current issue from results", () => {
      setMockResults([
        createIssue("current-issue", "Current issue"),
        createIssue("other-issue", "Other issue"),
      ]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue" },
      });

      expect(
        screen.queryByTestId("search-result-current-issue"),
      ).not.toBeInTheDocument();
      expect(
        screen.getByTestId("search-result-other-issue"),
      ).toBeInTheDocument();
    });

    it("excludes existing dependencies from results", () => {
      setMockResults([
        createIssue("dep-1", "Existing dep"),
        createIssue("dep-2", "New dep"),
      ]);

      render(
        <DependencySearchPicker
          {...defaultProps({ existingDependencyIds: ["dep-1"] })}
        />,
      );

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "dep" },
      });

      expect(
        screen.queryByTestId("search-result-dep-1"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("search-result-dep-2")).toBeInTheDocument();
    });
  });

  describe("selecting a result", () => {
    it("calls onSelect when clicking a result", () => {
      const onSelect = vi.fn();
      setMockResults([createIssue("issue-1", "Fix login bug")]);

      render(<DependencySearchPicker {...defaultProps({ onSelect })} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "fix" },
      });
      fireEvent.click(screen.getByTestId("search-result-issue-1"));

      expect(onSelect).toHaveBeenCalledWith("issue-1");
    });

    it("calls onSelect with direct ID when Enter pressed with no focused result", () => {
      const onSelect = vi.fn();

      render(<DependencySearchPicker {...defaultProps({ onSelect })} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "manual-id" },
      });
      fireEvent.keyDown(screen.getByTestId("dependency-search-input"), {
        key: "Enter",
      });

      expect(onSelect).toHaveBeenCalledWith("manual-id");
    });

    it("calls onSelect via confirm button", () => {
      const onSelect = vi.fn();

      render(<DependencySearchPicker {...defaultProps({ onSelect })} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "direct-id" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      expect(onSelect).toHaveBeenCalledWith("direct-id");
    });
  });

  describe("keyboard navigation", () => {
    it("ArrowDown moves focus to first result", () => {
      setMockResults([
        createIssue("issue-1", "First"),
        createIssue("issue-2", "Second"),
      ]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue" },
      });
      fireEvent.keyDown(screen.getByTestId("dependency-search-input"), {
        key: "ArrowDown",
      });

      const result = screen.getByTestId("search-result-issue-1");
      expect(result).toHaveAttribute("aria-selected", "true");
    });

    it("ArrowDown wraps around to first result", () => {
      setMockResults([
        createIssue("issue-1", "First"),
        createIssue("issue-2", "Second"),
      ]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue" },
      });

      const input = screen.getByTestId("dependency-search-input");
      // Move to index 0
      fireEvent.keyDown(input, { key: "ArrowDown" });
      // Move to index 1
      fireEvent.keyDown(input, { key: "ArrowDown" });
      // Wrap to index 0
      fireEvent.keyDown(input, { key: "ArrowDown" });

      const first = screen.getByTestId("search-result-issue-1");
      expect(first).toHaveAttribute("aria-selected", "true");
    });

    it("ArrowUp wraps around to last result", () => {
      setMockResults([
        createIssue("issue-1", "First"),
        createIssue("issue-2", "Second"),
      ]);

      render(<DependencySearchPicker {...defaultProps()} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue" },
      });

      const input = screen.getByTestId("dependency-search-input");
      // ArrowUp from -1 wraps to last
      fireEvent.keyDown(input, { key: "ArrowUp" });

      const last = screen.getByTestId("search-result-issue-2");
      expect(last).toHaveAttribute("aria-selected", "true");
    });

    it("Enter selects focused result", () => {
      const onSelect = vi.fn();
      setMockResults([
        createIssue("issue-1", "First"),
        createIssue("issue-2", "Second"),
      ]);

      render(<DependencySearchPicker {...defaultProps({ onSelect })} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue" },
      });

      const input = screen.getByTestId("dependency-search-input");
      // Move to first result
      fireEvent.keyDown(input, { key: "ArrowDown" });
      // Select it
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onSelect).toHaveBeenCalledWith("issue-1");
    });

    it("Escape closes dropdown first, then cancels", () => {
      const onCancel = vi.fn();
      setMockResults([createIssue("issue-1", "First")]);

      render(<DependencySearchPicker {...defaultProps({ onCancel })} />);

      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue" },
      });

      // Dropdown is showing
      expect(screen.getByTestId("search-results-dropdown")).toBeInTheDocument();

      // First Escape closes dropdown
      fireEvent.keyDown(screen.getByTestId("dependency-search-input"), {
        key: "Escape",
      });
      expect(
        screen.queryByTestId("search-results-dropdown"),
      ).not.toBeInTheDocument();
      expect(onCancel).not.toHaveBeenCalled();

      // Second Escape cancels the picker
      fireEvent.keyDown(screen.getByTestId("dependency-search-input"), {
        key: "Escape",
      });
      expect(onCancel).toHaveBeenCalledTimes(1);
    });
  });

  describe("cancel", () => {
    it("calls onCancel when cancel button is clicked", () => {
      const onCancel = vi.fn();
      render(<DependencySearchPicker {...defaultProps({ onCancel })} />);

      fireEvent.click(screen.getByTestId("cancel-add-dependency"));

      expect(onCancel).toHaveBeenCalledTimes(1);
    });
  });

  describe("saving state", () => {
    it("disables input when isSaving", () => {
      render(<DependencySearchPicker {...defaultProps({ isSaving: true })} />);

      expect(screen.getByTestId("dependency-search-input")).toBeDisabled();
    });

    it("disables cancel button when isSaving", () => {
      render(<DependencySearchPicker {...defaultProps({ isSaving: true })} />);

      expect(screen.getByTestId("cancel-add-dependency")).toBeDisabled();
    });

    it("shows Adding... text when isSaving", () => {
      render(<DependencySearchPicker {...defaultProps({ isSaving: true })} />);

      expect(screen.getByTestId("confirm-add-dependency")).toHaveTextContent(
        "Adding...",
      );
    });
  });
});
