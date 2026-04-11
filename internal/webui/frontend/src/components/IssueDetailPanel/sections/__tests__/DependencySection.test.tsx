/**
 * @vitest-environment jsdom
 */

/**
 * DependencySection component tests.
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
import type { IssueWithDependencyMetadata } from "@/types";

import {
  DependencySection,
  type DependencySectionProps,
} from "../DependencySection";

// Mock useIssueSearch used by DependencySearchPicker
vi.mock("@/hooks/issues", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/issues")>("@/hooks/issues");
  return {
    ...actual,
    useIssueSearch: () => ({
      results: [],
      isLoading: false,
      search: vi.fn(),
      query: "",
    }),
  };
});

// Helper to create test dependencies
function createDependency(
  id: string,
  title: string,
  status: "open" | "in_progress" | "closed" = "open",
  dependencyType: string = "blocks",
): IssueWithDependencyMetadata {
  return {
    id,
    title,
    status,
    dependency_type: dependencyType,
    description: "",
    issue_type: "task",
    priority: 2,
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    created_by: "test-user",
  };
}

// Default test props
function defaultProps(
  overrides?: Partial<DependencySectionProps>,
): DependencySectionProps {
  return {
    issueId: "test-issue-1",
    dependencies: [],
    onAddDependency: vi.fn().mockResolvedValue(undefined),
    onRemoveDependency: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe("DependencySection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("rendering", () => {
    it("renders empty state when no dependencies", () => {
      render(<DependencySection {...defaultProps()} />);

      expect(screen.getByTestId("dependency-section")).toBeInTheDocument();
      expect(screen.getByText("Blocked By")).toBeInTheDocument();
      expect(screen.getByTestId("no-dependencies")).toHaveTextContent(
        "No blocking dependencies",
      );
    });

    it("renders dependency count in header when dependencies exist", () => {
      const deps = [
        createDependency("dep-1", "First dep"),
        createDependency("dep-2", "Second dep"),
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByText("Blocked By (2)")).toBeInTheDocument();
    });

    it("renders list of dependencies with IDs and titles", () => {
      const deps = [
        createDependency("dep-1", "First dependency"),
        createDependency("dep-2", "Second dependency"),
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByTestId("dependency-list")).toBeInTheDocument();
      expect(screen.getByTestId("dependency-item-dep-1")).toBeInTheDocument();
      expect(screen.getByTestId("dependency-item-dep-2")).toBeInTheDocument();
      expect(screen.getByText("dep-1")).toBeInTheDocument();
      expect(screen.getByText("First dependency")).toBeInTheDocument();
      expect(screen.getByText("dep-2")).toBeInTheDocument();
      expect(screen.getByText("Second dependency")).toBeInTheDocument();
    });

    it("renders remove buttons for each dependency", () => {
      const deps = [createDependency("dep-1", "Test dep")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByTestId("remove-dependency-dep-1")).toBeInTheDocument();
    });

    it("shows dependency type badge", () => {
      const deps = [
        createDependency("dep-1", "Test dep", "open", "parent-child"),
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByText("parent-child")).toBeInTheDocument();
    });

    it("applies closed styling to closed dependencies", () => {
      const deps = [createDependency("dep-1", "Closed dep", "closed")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId("dependency-item-dep-1");
      expect(item.className).toContain("dependencyClosed");
    });

    it("shows add button when not disabled", () => {
      render(<DependencySection {...defaultProps()} />);

      expect(screen.getByTestId("add-dependency-button")).toBeInTheDocument();
    });

    it("hides add button when disabled", () => {
      render(<DependencySection {...defaultProps({ disabled: true })} />);

      expect(
        screen.queryByTestId("add-dependency-button"),
      ).not.toBeInTheDocument();
    });

    it("hides remove buttons when disabled", () => {
      const deps = [createDependency("dep-1", "Test dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, disabled: true })}
        />,
      );

      expect(
        screen.queryByTestId("remove-dependency-dep-1"),
      ).not.toBeInTheDocument();
    });
  });

  describe("adding dependencies", () => {
    it("shows add form when add button is clicked", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));

      expect(screen.getByTestId("add-dependency-form")).toBeInTheDocument();
      expect(screen.getByTestId("dependency-search-input")).toBeInTheDocument();
      expect(screen.getByTestId("confirm-add-dependency")).toBeInTheDocument();
      expect(screen.getByTestId("cancel-add-dependency")).toBeInTheDocument();
    });

    it("focuses input when entering add mode", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));

      expect(screen.getByTestId("dependency-search-input")).toHaveFocus();
    });

    it("hides add button when in add mode", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));

      expect(
        screen.queryByTestId("add-dependency-button"),
      ).not.toBeInTheDocument();
    });

    it("cancels add mode when cancel button is clicked", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.click(screen.getByTestId("cancel-add-dependency"));

      expect(
        screen.queryByTestId("add-dependency-form"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("add-dependency-button")).toBeInTheDocument();
    });

    it("cancels add mode when Escape is pressed", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.keyDown(screen.getByTestId("dependency-search-input"), {
        key: "Escape",
      });

      expect(
        screen.queryByTestId("add-dependency-form"),
      ).not.toBeInTheDocument();
    });

    it("calls onAddDependency when confirm button is clicked with typed ID", async () => {
      const onAddDependency = vi.fn().mockResolvedValue(undefined);
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "new-dep-id" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      await waitFor(() => {
        expect(onAddDependency).toHaveBeenCalledWith("new-dep-id", "blocks");
      });
    });

    it("submits on Enter key", async () => {
      const onAddDependency = vi.fn().mockResolvedValue(undefined);
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "new-dep-id" },
      });
      fireEvent.keyDown(screen.getByTestId("dependency-search-input"), {
        key: "Enter",
      });

      await waitFor(() => {
        expect(onAddDependency).toHaveBeenCalledWith("new-dep-id", "blocks");
      });
    });

    it("resets form after successful add", async () => {
      const onAddDependency = vi.fn().mockResolvedValue(undefined);
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "new-dep-id" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      await waitFor(() => {
        expect(
          screen.queryByTestId("add-dependency-form"),
        ).not.toBeInTheDocument();
      });
    });

    it("shows error when adding self as dependency", async () => {
      render(<DependencySection {...defaultProps({ issueId: "issue-1" })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue-1" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      await waitFor(() => {
        expect(screen.getByTestId("dependency-error")).toHaveTextContent(
          "Cannot add self as dependency",
        );
      });
    });

    it("shows error when adding duplicate dependency", async () => {
      const deps = [createDependency("existing-dep", "Existing")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "existing-dep" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      await waitFor(() => {
        expect(screen.getByTestId("dependency-error")).toHaveTextContent(
          "Already a dependency",
        );
      });
    });

    it("shows error from API failure", async () => {
      const onAddDependency = vi
        .fn()
        .mockRejectedValue(new Error("Issue not found"));
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "nonexistent" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      await waitFor(() => {
        expect(screen.getByTestId("dependency-error")).toHaveTextContent(
          "Issue not found",
        );
      });
    });

    it("shows loading state while adding", async () => {
      let resolveAdd: () => void;
      const onAddDependency = vi.fn().mockImplementation(
        () =>
          new Promise((resolve) => {
            resolveAdd = resolve;
          }),
      );
      render(<DependencySection {...defaultProps({ onAddDependency })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "new-dep" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      expect(screen.getByTestId("confirm-add-dependency")).toHaveTextContent(
        "Adding...",
      );
      expect(screen.getByTestId("confirm-add-dependency")).toBeDisabled();

      // Clean up the pending promise
      await act(async () => {
        resolveAdd!();
      });
    });

    it("disables confirm button when input is empty", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));

      expect(screen.getByTestId("confirm-add-dependency")).toBeDisabled();
    });
  });

  describe("removing dependencies", () => {
    it("calls onRemoveDependency when remove button is clicked", async () => {
      const onRemoveDependency = vi.fn().mockResolvedValue(undefined);
      const deps = [createDependency("dep-1", "Test dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onRemoveDependency })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-dependency-dep-1"));

      await waitFor(() => {
        expect(onRemoveDependency).toHaveBeenCalledWith("dep-1");
      });
    });

    it("shows error from removal failure", async () => {
      const onRemoveDependency = vi
        .fn()
        .mockRejectedValue(new Error("Failed to remove"));
      const deps = [createDependency("dep-1", "Test dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onRemoveDependency })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-dependency-dep-1"));

      await waitFor(() => {
        expect(screen.getByTestId("dependency-error")).toHaveTextContent(
          "Failed to remove",
        );
      });
    });

    it("shows loading state while removing", async () => {
      let resolveRemove: () => void;
      const onRemoveDependency = vi.fn().mockImplementation(
        () =>
          new Promise((resolve) => {
            resolveRemove = resolve;
          }),
      );
      const deps = [createDependency("dep-1", "Test dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onRemoveDependency })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-dependency-dep-1"));

      const item = screen.getByTestId("dependency-item-dep-1");
      expect(item.className).toContain("removing");

      // Clean up the pending promise
      await act(async () => {
        resolveRemove!();
      });
    });

    it("disables all buttons during removal", async () => {
      let resolveRemove: () => void;
      const onRemoveDependency = vi.fn().mockImplementation(
        () =>
          new Promise((resolve) => {
            resolveRemove = resolve;
          }),
      );
      const deps = [
        createDependency("dep-1", "First"),
        createDependency("dep-2", "Second"),
      ];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onRemoveDependency })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-dependency-dep-1"));

      expect(screen.getByTestId("remove-dependency-dep-2")).toBeDisabled();
      expect(screen.getByTestId("add-dependency-button")).toBeDisabled();

      // Clean up the pending promise
      await act(async () => {
        resolveRemove!();
      });
    });
  });

  describe("accessibility", () => {
    it("has aria-label on remove buttons", () => {
      const deps = [createDependency("dep-1", "Test dep")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      expect(screen.getByTestId("remove-dependency-dep-1")).toHaveAttribute(
        "aria-label",
        "Remove dependency dep-1",
      );
    });

    it("has aria-label on add button", () => {
      render(<DependencySection {...defaultProps()} />);

      expect(screen.getByTestId("add-dependency-button")).toHaveAttribute(
        "aria-label",
        "Add dependency",
      );
    });

    it("has aria-label on search input", () => {
      render(<DependencySection {...defaultProps()} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));

      expect(screen.getByTestId("dependency-search-input")).toHaveAttribute(
        "aria-label",
        "Search issues",
      );
    });

    it('error has role="alert"', async () => {
      render(<DependencySection {...defaultProps({ issueId: "issue-1" })} />);

      fireEvent.click(screen.getByTestId("add-dependency-button"));
      fireEvent.change(screen.getByTestId("dependency-search-input"), {
        target: { value: "issue-1" },
      });
      fireEvent.click(screen.getByTestId("confirm-add-dependency"));

      await waitFor(() => {
        expect(screen.getByRole("alert")).toBeInTheDocument();
      });
    });
  });

  describe("custom className", () => {
    it("applies custom className", () => {
      render(
        <DependencySection {...defaultProps({ className: "custom-class" })} />,
      );

      expect(screen.getByTestId("dependency-section").className).toContain(
        "custom-class",
      );
    });
  });

  describe("navigation via chips", () => {
    it("calls onNavigateToIssue when a dependency chip is clicked", () => {
      const onNavigateToIssue = vi.fn();
      const deps = [createDependency("dep-1", "First dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onNavigateToIssue })}
        />,
      );

      fireEvent.click(screen.getByTestId("dependency-item-dep-1"));

      expect(onNavigateToIssue).toHaveBeenCalledWith(deps[0]);
    });

    it("calls onNavigateToIssue on Enter key press", () => {
      const onNavigateToIssue = vi.fn();
      const deps = [createDependency("dep-1", "First dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onNavigateToIssue })}
        />,
      );

      fireEvent.keyDown(screen.getByTestId("dependency-item-dep-1"), {
        key: "Enter",
      });

      expect(onNavigateToIssue).toHaveBeenCalledWith(deps[0]);
    });

    it("calls onNavigateToIssue on Space key press", () => {
      const onNavigateToIssue = vi.fn();
      const deps = [createDependency("dep-1", "First dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onNavigateToIssue })}
        />,
      );

      fireEvent.keyDown(screen.getByTestId("dependency-item-dep-1"), {
        key: " ",
      });

      expect(onNavigateToIssue).toHaveBeenCalledWith(deps[0]);
    });

    it("does not navigate when onNavigateToIssue is not provided", () => {
      const deps = [createDependency("dep-1", "First dep")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId("dependency-item-dep-1");
      // Should not have role="button" when not clickable
      expect(item).not.toHaveAttribute("role", "button");
    });

    it("has role=button and tabIndex=0 when clickable", () => {
      const onNavigateToIssue = vi.fn();
      const deps = [createDependency("dep-1", "First dep")];
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, onNavigateToIssue })}
        />,
      );

      const item = screen.getByTestId("dependency-item-dep-1");
      expect(item).toHaveAttribute("role", "button");
      expect(item).toHaveAttribute("tabindex", "0");
    });

    it("remove button click does NOT trigger navigation (stopPropagation)", async () => {
      const onNavigateToIssue = vi.fn();
      const onRemoveDependency = vi.fn().mockResolvedValue(undefined);
      const deps = [createDependency("dep-1", "Test dep")];
      render(
        <DependencySection
          {...defaultProps({
            dependencies: deps,
            onNavigateToIssue,
            onRemoveDependency,
          })}
        />,
      );

      fireEvent.click(screen.getByTestId("remove-dependency-dep-1"));

      await waitFor(() => {
        expect(onRemoveDependency).toHaveBeenCalledWith("dep-1");
      });
      expect(onNavigateToIssue).not.toHaveBeenCalled();
    });
  });

  describe("status dots", () => {
    it("renders status dot for open dependency", () => {
      const deps = [createDependency("dep-1", "Open dep", "open")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId("dependency-item-dep-1");
      const dot = item.querySelector("[aria-label='open']");
      expect(dot).toBeInTheDocument();
      expect(dot?.className).toContain("statusDot");
    });

    it("renders status dot for closed dependency", () => {
      const deps = [createDependency("dep-1", "Closed dep", "closed")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId("dependency-item-dep-1");
      const dot = item.querySelector("[aria-label='closed']");
      expect(dot).toBeInTheDocument();
      expect(dot?.className).toContain("statusDotClosed");
    });

    it("renders status dot for in_progress dependency", () => {
      const deps = [createDependency("dep-1", "WIP dep", "in_progress")];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId("dependency-item-dep-1");
      const dot = item.querySelector("[aria-label='in_progress']");
      expect(dot).toBeInTheDocument();
      expect(dot?.className).toContain("statusDotInProgress");
    });

    it("renders status dot for blocked dependency", () => {
      const deps: IssueWithDependencyMetadata[] = [
        {
          ...createDependency("dep-1", "Blocked dep"),
          status: "blocked",
        },
      ];
      render(<DependencySection {...defaultProps({ dependencies: deps })} />);

      const item = screen.getByTestId("dependency-item-dep-1");
      const dot = item.querySelector("[aria-label='blocked']");
      expect(dot).toBeInTheDocument();
      expect(dot?.className).toContain("statusDotBlocked");
    });
  });

  describe("depth limit", () => {
    function createManyDeps(count: number): IssueWithDependencyMetadata[] {
      return Array.from({ length: count }, (_, i) =>
        createDependency(`dep-${i + 1}`, `Dep ${i + 1}`),
      );
    }

    it("shows all dependencies when count <= depthLimit", () => {
      const deps = createManyDeps(5);
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, depthLimit: 5 })}
        />,
      );

      for (let i = 1; i <= 5; i++) {
        expect(
          screen.getByTestId(`dependency-item-dep-${i}`),
        ).toBeInTheDocument();
      }
      expect(
        screen.queryByTestId("show-more-dependencies"),
      ).not.toBeInTheDocument();
    });

    it("shows only depthLimit items when count > depthLimit", () => {
      const deps = createManyDeps(8);
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, depthLimit: 5 })}
        />,
      );

      for (let i = 1; i <= 5; i++) {
        expect(
          screen.getByTestId(`dependency-item-dep-${i}`),
        ).toBeInTheDocument();
      }
      expect(
        screen.queryByTestId("dependency-item-dep-6"),
      ).not.toBeInTheDocument();
    });

    it("shows 'Show all (N)' button when count > depthLimit", () => {
      const deps = createManyDeps(8);
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, depthLimit: 5 })}
        />,
      );

      const btn = screen.getByTestId("show-more-dependencies");
      expect(btn).toHaveTextContent("Show all (8)");
    });

    it("expands to show all items on 'Show all' click", () => {
      const deps = createManyDeps(8);
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, depthLimit: 5 })}
        />,
      );

      fireEvent.click(screen.getByTestId("show-more-dependencies"));

      for (let i = 1; i <= 8; i++) {
        expect(
          screen.getByTestId(`dependency-item-dep-${i}`),
        ).toBeInTheDocument();
      }
    });

    it("collapses back on 'Show less' click", () => {
      const deps = createManyDeps(8);
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, depthLimit: 5 })}
        />,
      );

      // Expand
      fireEvent.click(screen.getByTestId("show-more-dependencies"));
      expect(screen.getByTestId("show-more-dependencies")).toHaveTextContent(
        "Show less",
      );

      // Collapse
      fireEvent.click(screen.getByTestId("show-more-dependencies"));
      expect(
        screen.queryByTestId("dependency-item-dep-6"),
      ).not.toBeInTheDocument();
      expect(screen.getByTestId("show-more-dependencies")).toHaveTextContent(
        "Show all (8)",
      );
    });

    it("does not show toggle when count equals depthLimit", () => {
      const deps = createManyDeps(5);
      render(
        <DependencySection
          {...defaultProps({ dependencies: deps, depthLimit: 5 })}
        />,
      );

      expect(
        screen.queryByTestId("show-more-dependencies"),
      ).not.toBeInTheDocument();
    });

    it("resets expanded state when issueId changes", () => {
      const deps = createManyDeps(8);
      const { rerender } = render(
        <DependencySection
          {...defaultProps({
            issueId: "issue-1",
            dependencies: deps,
            depthLimit: 5,
          })}
        />,
      );

      // Expand
      fireEvent.click(screen.getByTestId("show-more-dependencies"));
      expect(screen.getByTestId("dependency-item-dep-6")).toBeInTheDocument();

      // Rerender with different issueId
      rerender(
        <DependencySection
          {...defaultProps({
            issueId: "issue-2",
            dependencies: deps,
            depthLimit: 5,
          })}
        />,
      );

      // Should be collapsed again
      expect(
        screen.queryByTestId("dependency-item-dep-6"),
      ).not.toBeInTheDocument();
    });
  });
});
