/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceSwitcher component.
 */

import {
  render as rtlRender,
  screen,
  fireEvent,
  waitFor,
} from "@testing-library/react";
import type { RenderOptions } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { WorkspaceSummary } from "@/api/workspace";
import { KeyboardShortcutProvider } from "@/hooks";

import { WorkspaceSwitcher } from "../WorkspaceSwitcher";

const Wrapper = ({ children }: { children: React.ReactNode }) => (
  <KeyboardShortcutProvider>{children}</KeyboardShortcutProvider>
);

function render(
  ui: React.ReactElement,
  options?: Omit<RenderOptions, "wrapper">,
) {
  return rtlRender(ui, { wrapper: Wrapper, ...options });
}

// scrollIntoView is not available in jsdom
Element.prototype.scrollIntoView = vi.fn();

/**
 * Helper to create mock WorkspaceSummary entries.
 */
function createWorkspace(
  overrides?: Partial<WorkspaceSummary>,
): WorkspaceSummary {
  return {
    id: "ws-test",
    name: "test-workspace",
    path: "/home/user/workspace",
    active: false,
    repo_count: 1,
    is_default: false,
    ...overrides,
  };
}

/**
 * Standard set of workspaces for most tests.
 */
function createWorkspaces(): WorkspaceSummary[] {
  return [
    createWorkspace({
      id: "ws-alpha",
      name: "alpha",
      path: "/home/user/alpha",
      repo_count: 3,
    }),
    createWorkspace({
      id: "ws-beta",
      name: "beta",
      path: "/home/user/beta",
      repo_count: 1,
    }),
    createWorkspace({
      id: "ws-gamma",
      name: "gamma",
      path: "/home/user/gamma-project",
      repo_count: 5,
    }),
  ];
}

describe("WorkspaceSwitcher", () => {
  describe("rendering", () => {
    it("renders nothing when isOpen is false", () => {
      const { container } = render(
        <WorkspaceSwitcher
          isOpen={false}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // Component returns null; nothing in the container
      expect(container.innerHTML).toBe("");
      // Also confirm the overlay is not present anywhere (portal would be on body)
      expect(
        document.querySelector("[class*=overlay]"),
      ).not.toBeInTheDocument();
    });

    it("renders overlay and search input when isOpen is true", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // Search input should be present
      expect(screen.getByTestId("search-input-field")).toBeInTheDocument();
      expect(
        screen.getByPlaceholderText("Switch workspace..."),
      ).toBeInTheDocument();

      // All workspace names should be listed
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
    });

    it("shows workspace paths", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      expect(screen.getByText("/home/user/alpha")).toBeInTheDocument();
      expect(screen.getByText("/home/user/beta")).toBeInTheDocument();
      expect(screen.getByText("/home/user/gamma-project")).toBeInTheDocument();
    });

    it("shows repo counts", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      expect(screen.getByText("3 repos")).toBeInTheDocument();
      expect(screen.getByText("1 repo")).toBeInTheDocument();
      expect(screen.getByText("5 repos")).toBeInTheDocument();
    });

    it("shows active indicator for the active workspace", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-beta"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // The active item should have the checkmark
      const buttons = screen.getAllByRole("button");
      // Find the button that contains "beta"
      const betaButton = buttons.find((b) => b.textContent?.includes("beta"));
      expect(betaButton).toBeDefined();
      expect(betaButton!.className).toMatch(/active/);
      // Check for the checkmark character
      expect(betaButton!.textContent).toContain("\u2713");
    });

    it("does not show active indicator for non-active workspaces", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-beta"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const buttons = screen.getAllByRole("button");
      const alphaButton = buttons.find(
        (b) =>
          b.textContent?.includes("alpha") && !b.textContent?.includes("beta"),
      );
      expect(alphaButton).toBeDefined();
      expect(alphaButton!.className).not.toMatch(/active/);
    });

    it("shows shortcut hints for first 9 workspaces", () => {
      // Create 10 workspaces
      const workspaces = Array.from({ length: 10 }, (_, i) =>
        createWorkspace({
          name: `ws-${i}`,
          path: `/home/user/ws-${i}`,
          repo_count: i + 1,
        }),
      );

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={workspaces}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const buttons = screen.getAllByRole("button");
      // workspace items (excluding the clear button from SearchInput if present)
      const workspaceButtons = buttons.filter((b) =>
        b.hasAttribute("data-workspace-item"),
      );

      // First 9 should have shortcut hints containing their number
      for (let i = 0; i < 9; i++) {
        expect(workspaceButtons[i].textContent).toContain(`${i + 1}`);
      }

      // The 10th workspace should NOT have a shortcut hint number "10"
      // It should only contain the repo count and name
      const tenthButton = workspaceButtons[9];
      expect(tenthButton).toBeDefined();
      // The shortcut hint class should not be present for index >= 9
      expect(tenthButton.querySelector("[class*=shortcutHint]")).toBeNull();
    });

    it("shows no active indicator when activeWorkspaceId is null", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const buttons = screen.getAllByRole("button");
      const workspaceButtons = buttons.filter((b) =>
        b.hasAttribute("data-workspace-item"),
      );
      for (const button of workspaceButtons) {
        expect(button.className).not.toMatch(/active/);
      }
    });
  });

  describe("filtering", () => {
    it("filters workspaces by name substring", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "alp" } });

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.queryByText("beta")).not.toBeInTheDocument();
      expect(screen.queryByText("gamma")).not.toBeInTheDocument();
    });

    it("filters workspaces by path substring", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "gamma-project" } });

      expect(screen.queryByText("alpha")).not.toBeInTheDocument();
      expect(screen.queryByText("beta")).not.toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
    });

    it("is case-insensitive", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "BETA" } });

      expect(screen.getByText("beta")).toBeInTheDocument();
      expect(screen.queryByText("alpha")).not.toBeInTheDocument();
    });

    it("shows 'No workspaces found' when filter matches nothing", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "nonexistent" } });

      expect(screen.getByText("No workspaces found")).toBeInTheDocument();
    });

    it("shows all workspaces when search is empty", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
    });

    it("matches multiple workspaces when search term matches several", () => {
      const workspaces = [
        createWorkspace({ name: "my-project", path: "/a" }),
        createWorkspace({ name: "other", path: "/b/my-stuff" }),
        createWorkspace({ name: "unrelated", path: "/c" }),
      ];

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={workspaces}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "my" } });

      expect(screen.getByText("my-project")).toBeInTheDocument();
      expect(screen.getByText("other")).toBeInTheDocument();
      expect(screen.queryByText("unrelated")).not.toBeInTheDocument();
    });
  });

  describe("interactions", () => {
    it("calls onSelect and onClose when a workspace is clicked", () => {
      const onSelect = vi.fn();
      const onClose = vi.fn();

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={onSelect}
          onClose={onClose}
        />,
      );

      const buttons = screen.getAllByRole("button");
      const betaButton = buttons.find((b) => b.textContent?.includes("beta"));
      fireEvent.click(betaButton!);

      expect(onSelect).toHaveBeenCalledTimes(1);
      expect(onSelect).toHaveBeenCalledWith("ws-beta");
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("calls onClose when Escape is pressed (via escape layer)", () => {
      const onClose = vi.fn();

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={onClose}
        />,
      );

      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("does not call onClose when dialog content is clicked", () => {
      const onClose = vi.fn();

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={onClose}
        />,
      );

      // Click on the dialog (inner) element — stopPropagation prevents onClose
      const dialog = document.querySelector("[class*=dialog]");
      expect(dialog).toBeInTheDocument();
      fireEvent.click(dialog!);

      expect(onClose).not.toHaveBeenCalled();
    });

    it("resets search when reopened", () => {
      const { rerender } = render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // Type a search term
      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "alpha" } });
      expect(screen.queryByText("beta")).not.toBeInTheDocument();

      // Close
      rerender(
        <WorkspaceSwitcher
          isOpen={false}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // Reopen
      rerender(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // All workspaces should be visible (search reset)
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
      expect(screen.getByText("gamma")).toBeInTheDocument();
    });
  });

  describe("keyboard navigation", () => {
    it("ArrowDown moves highlight to next item", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const overlay = document.querySelector("[class*=overlay]")!;

      // Initially first item is highlighted
      const workspaceButtons = document.querySelectorAll(
        "[data-workspace-item]",
      );
      expect(workspaceButtons[0].className).toMatch(/highlighted/);

      // Press ArrowDown
      fireEvent.keyDown(overlay, { key: "ArrowDown" });

      // Second item should now be highlighted
      const updatedButtons = document.querySelectorAll("[data-workspace-item]");
      expect(updatedButtons[0].className).not.toMatch(/highlighted/);
      expect(updatedButtons[1].className).toMatch(/highlighted/);
    });

    it("ArrowUp moves highlight to previous item", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const overlay = document.querySelector("[class*=overlay]")!;

      // Move down first, then up
      fireEvent.keyDown(overlay, { key: "ArrowDown" });
      fireEvent.keyDown(overlay, { key: "ArrowUp" });

      const buttons = document.querySelectorAll("[data-workspace-item]");
      expect(buttons[0].className).toMatch(/highlighted/);
    });

    it("ArrowDown wraps from last to first", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const overlay = document.querySelector("[class*=overlay]")!;

      // Move to last item (3 workspaces, so 2 ArrowDowns)
      fireEvent.keyDown(overlay, { key: "ArrowDown" });
      fireEvent.keyDown(overlay, { key: "ArrowDown" });

      // Now at last; pressing ArrowDown should wrap to first
      fireEvent.keyDown(overlay, { key: "ArrowDown" });

      const buttons = document.querySelectorAll("[data-workspace-item]");
      expect(buttons[0].className).toMatch(/highlighted/);
    });

    it("ArrowUp wraps from first to last", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const overlay = document.querySelector("[class*=overlay]")!;

      // Press ArrowUp from first item — should wrap to last
      fireEvent.keyDown(overlay, { key: "ArrowUp" });

      const buttons = document.querySelectorAll("[data-workspace-item]");
      expect(buttons[2].className).toMatch(/highlighted/);
    });

    it("Enter selects the highlighted workspace", () => {
      const onSelect = vi.fn();
      const onClose = vi.fn();

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={onSelect}
          onClose={onClose}
        />,
      );

      const overlay = document.querySelector("[class*=overlay]")!;

      // Move to second item and press Enter
      fireEvent.keyDown(overlay, { key: "ArrowDown" });
      fireEvent.keyDown(overlay, { key: "Enter" });

      expect(onSelect).toHaveBeenCalledTimes(1);
      expect(onSelect).toHaveBeenCalledWith("ws-beta");
      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it("Enter with no filtered results does nothing", () => {
      const onSelect = vi.fn();

      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={onSelect}
          onClose={vi.fn()}
        />,
      );

      // Filter to nothing
      const input = screen.getByTestId("search-input-field");
      fireEvent.change(input, { target: { value: "nonexistent" } });

      const overlay = document.querySelector("[class*=overlay]")!;
      fireEvent.keyDown(overlay, { key: "Enter" });

      expect(onSelect).not.toHaveBeenCalled();
    });

    it("mouseEnter on an item updates the highlight", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const buttons = document.querySelectorAll("[data-workspace-item]");

      // Hover over the third item
      fireEvent.mouseEnter(buttons[2]);

      expect(buttons[2].className).toMatch(/highlighted/);
      expect(buttons[0].className).not.toMatch(/highlighted/);
    });
  });

  describe("empty state", () => {
    it("shows 'No workspaces found' when workspaces array is empty", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={[]}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      expect(screen.getByText("No workspaces found")).toBeInTheDocument();
    });
  });

  describe("workspace management actions", () => {
    it("renders no overflow button when neither onRename nor onDelete is provided", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      expect(
        document.querySelector("[data-testid^='workspace-switcher-overflow-']"),
      ).toBeNull();
    });

    it("renders an overflow button per workspace when onRename or onDelete is provided", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      expect(
        screen.getByTestId("workspace-switcher-overflow-ws-alpha"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("workspace-switcher-overflow-ws-gamma"),
      ).toBeInTheDocument();
    });

    it("clicking the overflow button opens the context menu without firing onSelect", () => {
      const onSelect = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={onSelect}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );

      expect(screen.getByTestId("workspace-context-menu")).toBeInTheDocument();
      expect(onSelect).not.toHaveBeenCalled();
    });

    it("right-click on a workspace item opens the context menu", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      const items = document.querySelectorAll("[data-workspace-item]");
      fireEvent.contextMenu(items[1]);

      expect(screen.getByTestId("workspace-context-menu")).toBeInTheDocument();
    });

    it("right-click does not open the context menu when management callbacks are absent", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      const items = document.querySelectorAll("[data-workspace-item]");
      fireEvent.contextMenu(items[1]);

      expect(
        screen.queryByTestId("workspace-context-menu"),
      ).not.toBeInTheDocument();
    });

    it("Rename swaps the item to an input pre-filled with the workspace name", () => {
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId(
        "workspace-switcher-rename-input",
      ) as HTMLInputElement;
      expect(input).toBeInTheDocument();
      expect(input.value).toBe("beta");
    });

    it("Enter inside the rename input calls onRename with (wsId, trimmedName) and closes edit mode on resolve", async () => {
      const onRename = vi.fn().mockResolvedValue(undefined);
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={onRename}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId("workspace-switcher-rename-input");
      fireEvent.change(input, { target: { value: "  renamed-beta  " } });
      fireEvent.keyDown(input, { key: "Enter" });

      await waitFor(() => {
        expect(onRename).toHaveBeenCalledWith("ws-beta", "renamed-beta");
      });
      await waitFor(() => {
        expect(
          screen.queryByTestId("workspace-switcher-rename-input"),
        ).not.toBeInTheDocument();
      });
    });

    it("submitting an empty name shows an inline error and does not call onRename", () => {
      const onRename = vi.fn().mockResolvedValue(undefined);
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={onRename}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId("workspace-switcher-rename-input");
      fireEvent.change(input, { target: { value: "   " } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(
        screen.getByTestId("workspace-switcher-rename-error"),
      ).toHaveTextContent("Name cannot be empty");
      expect(onRename).not.toHaveBeenCalled();
      expect(
        screen.getByTestId("workspace-switcher-rename-input"),
      ).toBeInTheDocument();
    });

    it("submitting an unchanged (trimmed) name exits edit mode without calling onRename", () => {
      const onRename = vi.fn().mockResolvedValue(undefined);
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={onRename}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId("workspace-switcher-rename-input");
      fireEvent.change(input, { target: { value: "  beta  " } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onRename).not.toHaveBeenCalled();
      expect(
        screen.queryByTestId("workspace-switcher-rename-input"),
      ).not.toBeInTheDocument();
    });

    it("rename API rejection shows inline error and preserves edit mode", async () => {
      const onRename = vi
        .fn()
        .mockRejectedValue(new Error("Name already taken"));
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={onRename}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId("workspace-switcher-rename-input");
      fireEvent.change(input, { target: { value: "alpha" } });
      fireEvent.keyDown(input, { key: "Enter" });

      await waitFor(() => {
        expect(
          screen.getByTestId("workspace-switcher-rename-error"),
        ).toHaveTextContent("Name already taken");
      });
      expect(
        screen.getByTestId("workspace-switcher-rename-input"),
      ).toBeInTheDocument();
    });

    it("Escape inside the rename input cancels without calling onRename", () => {
      const onRename = vi.fn().mockResolvedValue(undefined);
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={onRename}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const input = screen.getByTestId("workspace-switcher-rename-input");
      fireEvent.change(input, { target: { value: "renamed" } });
      fireEvent.keyDown(input, { key: "Escape" });

      expect(onRename).not.toHaveBeenCalled();
      expect(
        screen.queryByTestId("workspace-switcher-rename-input"),
      ).not.toBeInTheDocument();
    });

    it("Escape during rename cancels the rename without closing the switcher", () => {
      const onClose = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={onClose}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      // The escape-layer registration is suspended during rename so the
      // document-level Escape would not also trigger onClose.
      fireEvent.keyDown(document, { key: "Escape" });

      expect(onClose).not.toHaveBeenCalled();
    });

    it("Remove from the context menu fires onDelete with (wsId, wsName) and closes the switcher", () => {
      const onDelete = vi.fn();
      const onClose = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={onClose}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={onDelete}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-gamma"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-remove"));

      expect(onDelete).toHaveBeenCalledWith("ws-gamma", "gamma");
      expect(onClose).toHaveBeenCalled();
    });

    it("Set as default fires onSetDefault when target is not the default", () => {
      const onSetDefault = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
          onSetDefault={onSetDefault}
          onClearDefault={vi.fn()}
          defaultWorkspaceId="ws-alpha"
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-default"));

      expect(onSetDefault).toHaveBeenCalledWith("beta");
    });

    it("Clear default fires onClearDefault when target is already the default", () => {
      const onClearDefault = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={vi.fn()}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
          onSetDefault={vi.fn()}
          onClearDefault={onClearDefault}
          defaultWorkspaceId="ws-beta"
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-default"));

      expect(onClearDefault).toHaveBeenCalled();
    });

    it("ArrowDown/Enter do not fire workspace navigation while a rename is in progress", () => {
      const onSelect = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={onSelect}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-beta"),
      );
      fireEvent.click(screen.getByTestId("workspace-context-menu-rename"));

      const overlay = document.querySelector("[class*=overlay]")!;
      fireEvent.keyDown(overlay, { key: "ArrowDown" });
      fireEvent.keyDown(overlay, { key: "Enter" });

      expect(onSelect).not.toHaveBeenCalled();
    });

    it("does not switch workspace when clicking the overflow button (stops propagation)", () => {
      const onSelect = vi.fn();
      render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId="ws-alpha"
          onSelect={onSelect}
          onClose={vi.fn()}
          onRename={vi.fn().mockResolvedValue(undefined)}
          onDelete={vi.fn()}
        />,
      );

      fireEvent.click(
        screen.getByTestId("workspace-switcher-overflow-ws-gamma"),
      );

      expect(onSelect).not.toHaveBeenCalled();
    });
  });

  describe("portal rendering", () => {
    let portalContainer: Element | null;

    beforeEach(() => {
      portalContainer = null;
    });

    it("renders into document.body via portal", () => {
      const { container } = render(
        <WorkspaceSwitcher
          isOpen={true}
          workspaces={createWorkspaces()}
          activeWorkspaceId={null}
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />,
      );

      // The overlay should NOT be inside the render container (it's portaled)
      const overlayInContainer = container.querySelector("[class*=overlay]");
      expect(overlayInContainer).toBeNull();

      // The overlay SHOULD be in document.body
      portalContainer = document.body.querySelector("[class*=overlay]");
      expect(portalContainer).not.toBeNull();
    });
  });
});
