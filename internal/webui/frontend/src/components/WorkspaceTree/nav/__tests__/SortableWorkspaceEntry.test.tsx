/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SortableWorkspaceEntry component.
 * Verifies drag handle, display/edit modes, keyboard shortcuts,
 * context menu, and overflow button.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { WorkspaceSummary } from "@/api/workspace";
import type { SortableWorkspaceEntryProps } from "../SortableWorkspaceEntry";
import { SortableWorkspaceEntry } from "../SortableWorkspaceEntry";

// Mock @dnd-kit/sortable
vi.mock("@dnd-kit/sortable", () => ({
  useSortable: ({ id }: { id: string }) => ({
    attributes: { "data-sortable-id": id },
    listeners: {},
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
}));

function makeWorkspace(
  overrides: Partial<WorkspaceSummary> = {},
): WorkspaceSummary {
  return {
    id: "ws-dev",
    name: "dev",
    path: "/home/user/dev",
    active: true,
    repo_count: 3,
    is_default: false,
    ...overrides,
  };
}

function defaultProps(
  overrides: Partial<SortableWorkspaceEntryProps> = {},
): SortableWorkspaceEntryProps {
  return {
    ws: makeWorkspace(),
    isActive: false,
    isEditing: false,
    draftName: "",
    isSaving: false,
    renameError: null,
    renameInputRef: { current: null },
    onClick: vi.fn(),
    onDraftChange: vi.fn(),
    onSaveRename: vi.fn(),
    onRenameKeyDown: vi.fn(),
    onContextMenu: vi.fn(),
    onOverflowClick: vi.fn(),
    onMoveUp: vi.fn(),
    onMoveDown: vi.fn(),
    ...overrides,
  };
}

describe("SortableWorkspaceEntry", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("display mode", () => {
    it("renders workspace name as a link", () => {
      render(<SortableWorkspaceEntry {...defaultProps()} />);

      const link = screen.getByLabelText("Switch to workspace dev");
      expect(link).toBeInTheDocument();
      expect(link).toHaveTextContent("dev");
    });

    it("renders link with correct href", () => {
      render(<SortableWorkspaceEntry {...defaultProps()} />);

      const link = screen.getByLabelText("Switch to workspace dev");
      expect(link).toHaveAttribute("href", "/ws/ws-dev/");
    });

    it("uses workspace ID in href", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            ws: makeWorkspace({ id: "ws-custom", name: "my workspace" }),
          })}
        />,
      );

      const link = screen.getByLabelText("Switch to workspace my workspace");
      expect(link).toHaveAttribute("href", "/ws/ws-custom/");
    });

    it("displays repo count", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({ ws: makeWorkspace({ repo_count: 5 }) })}
        />,
      );

      expect(screen.getByText("5")).toBeInTheDocument();
    });

    it("shows active badge when workspace is active", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({ ws: makeWorkspace({ active: true }) })}
        />,
      );

      expect(screen.getByText("active")).toBeInTheDocument();
    });

    it("does not show active badge when workspace is inactive", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({ ws: makeWorkspace({ active: false }) })}
        />,
      );

      expect(screen.queryByText("active")).not.toBeInTheDocument();
    });
  });

  describe("click handling", () => {
    it("calls onClick with workspace name when clicking the entry", () => {
      const onClick = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onClick })} />,
      );

      fireEvent.click(container.firstChild as HTMLElement);
      expect(onClick).toHaveBeenCalledWith("dev");
    });

    it("calls onClick with workspace name when clicking the link", () => {
      const onClick = vi.fn();
      render(<SortableWorkspaceEntry {...defaultProps({ onClick })} />);

      const link = screen.getByLabelText("Switch to workspace dev");
      fireEvent.click(link);
      expect(onClick).toHaveBeenCalledWith("dev");
    });

    it("does not call onClick when in editing mode", () => {
      const onClick = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry
          {...defaultProps({ onClick, isEditing: true, draftName: "dev" })}
        />,
      );

      fireEvent.click(container.firstChild as HTMLElement);
      expect(onClick).not.toHaveBeenCalled();
    });

    it("calls onClick on Enter key press", () => {
      const onClick = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onClick })} />,
      );

      fireEvent.keyDown(container.firstChild as HTMLElement, { key: "Enter" });
      expect(onClick).toHaveBeenCalledWith("dev");
    });

    it("calls onClick on Space key press", () => {
      const onClick = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onClick })} />,
      );

      fireEvent.keyDown(container.firstChild as HTMLElement, { key: " " });
      expect(onClick).toHaveBeenCalledWith("dev");
    });
  });

  describe("keyboard move shortcuts", () => {
    it("calls onMoveUp on Alt+ArrowUp", () => {
      const onMoveUp = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onMoveUp })} />,
      );

      fireEvent.keyDown(container.firstChild as HTMLElement, {
        key: "ArrowUp",
        altKey: true,
      });
      expect(onMoveUp).toHaveBeenCalledTimes(1);
    });

    it("calls onMoveDown on Alt+ArrowDown", () => {
      const onMoveDown = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onMoveDown })} />,
      );

      fireEvent.keyDown(container.firstChild as HTMLElement, {
        key: "ArrowDown",
        altKey: true,
      });
      expect(onMoveDown).toHaveBeenCalledTimes(1);
    });

    it("does not call onMoveUp on ArrowUp without Alt", () => {
      const onMoveUp = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onMoveUp })} />,
      );

      fireEvent.keyDown(container.firstChild as HTMLElement, {
        key: "ArrowUp",
      });
      expect(onMoveUp).not.toHaveBeenCalled();
    });

    it("handles undefined onMoveUp gracefully", () => {
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onMoveUp: undefined })} />,
      );

      // Should not throw
      fireEvent.keyDown(container.firstChild as HTMLElement, {
        key: "ArrowUp",
        altKey: true,
      });
    });
  });

  describe("edit mode", () => {
    it("renders rename input when isEditing is true", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({ isEditing: true, draftName: "new-name" })}
        />,
      );

      const input = screen.getByTestId("workspace-rename-input");
      expect(input).toBeInTheDocument();
      expect(input).toHaveValue("new-name");
    });

    it("calls onDraftChange when typing in rename input", () => {
      const onDraftChange = vi.fn();
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            isEditing: true,
            draftName: "dev",
            onDraftChange,
          })}
        />,
      );

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.change(input, { target: { value: "new-dev" } });
      expect(onDraftChange).toHaveBeenCalledWith("new-dev");
    });

    it("calls onSaveRename on blur", () => {
      const onSaveRename = vi.fn();
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            isEditing: true,
            draftName: "dev",
            onSaveRename,
          })}
        />,
      );

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.blur(input);
      expect(onSaveRename).toHaveBeenCalledTimes(1);
    });

    it("calls onRenameKeyDown on key press in rename input", () => {
      const onRenameKeyDown = vi.fn();
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            isEditing: true,
            draftName: "dev",
            onRenameKeyDown,
          })}
        />,
      );

      const input = screen.getByTestId("workspace-rename-input");
      fireEvent.keyDown(input, { key: "Enter" });
      expect(onRenameKeyDown).toHaveBeenCalled();
    });

    it("disables rename input when isSaving is true", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            isEditing: true,
            draftName: "dev",
            isSaving: true,
          })}
        />,
      );

      expect(screen.getByTestId("workspace-rename-input")).toBeDisabled();
    });

    it("shows rename error when renameError is provided", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            isEditing: true,
            draftName: "dev",
            renameError: "Name already taken",
          })}
        />,
      );

      const error = screen.getByTestId("workspace-rename-error");
      expect(error).toBeInTheDocument();
      expect(error).toHaveTextContent("Name already taken");
      expect(error).toHaveAttribute("role", "alert");
    });

    it("does not show rename error when renameError is null", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({
            isEditing: true,
            draftName: "dev",
            renameError: null,
          })}
        />,
      );

      expect(
        screen.queryByTestId("workspace-rename-error"),
      ).not.toBeInTheDocument();
    });

    it("hides overflow button when in editing mode", () => {
      render(
        <SortableWorkspaceEntry
          {...defaultProps({ isEditing: true, draftName: "dev" })}
        />,
      );

      expect(
        screen.queryByTestId("workspace-overflow-dev"),
      ).not.toBeInTheDocument();
    });
  });

  describe("overflow and context menu", () => {
    it("renders overflow button with correct test id", () => {
      render(<SortableWorkspaceEntry {...defaultProps()} />);

      expect(screen.getByTestId("workspace-overflow-dev")).toBeInTheDocument();
    });

    it("calls onOverflowClick when overflow button is clicked", () => {
      const onOverflowClick = vi.fn();
      render(<SortableWorkspaceEntry {...defaultProps({ onOverflowClick })} />);

      fireEvent.click(screen.getByTestId("workspace-overflow-dev"));
      expect(onOverflowClick).toHaveBeenCalled();
      expect(onOverflowClick.mock.calls[0][1]).toBe("dev");
    });

    it("calls onContextMenu on right-click", () => {
      const onContextMenu = vi.fn();
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ onContextMenu })} />,
      );

      fireEvent.contextMenu(container.firstChild as HTMLElement);
      expect(onContextMenu).toHaveBeenCalled();
      expect(onContextMenu.mock.calls[0][1]).toBe("dev");
    });
  });

  describe("drag handle", () => {
    it("renders drag handle with correct aria-label", () => {
      render(<SortableWorkspaceEntry {...defaultProps()} />);

      expect(screen.getByLabelText("Drag to reorder dev")).toBeInTheDocument();
    });
  });

  describe("active state", () => {
    it("sets data-current attribute when isActive is true", () => {
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ isActive: true })} />,
      );

      expect(container.firstChild).toHaveAttribute("data-current", "true");
    });

    it("sets data-current to false when isActive is false", () => {
      const { container } = render(
        <SortableWorkspaceEntry {...defaultProps({ isActive: false })} />,
      );

      expect(container.firstChild).toHaveAttribute("data-current", "false");
    });
  });
});
