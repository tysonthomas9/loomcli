/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for KeyboardShortcutProvider workspace-related shortcuts.
 *
 * Tests the new onWorkspaceSwitcher, onWorkspacePositionalSwitch props,
 * and the Cmd/Ctrl+K routing logic.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { KeyboardShortcutProvider } from "../useKeyboardShortcuts";

/**
 * Helper to render the provider with given props.
 * Returns a target div for dispatching keyboard events. Events dispatched
 * on this target bubble up to `document` where the provider's listener
 * lives, and the event.target (a real Element) has `.closest()` which
 * isInputFocused requires.
 */
function renderProvider(
  props: Partial<React.ComponentProps<typeof KeyboardShortcutProvider>> = {},
) {
  const result = render(
    <KeyboardShortcutProvider {...props}>
      <div data-testid="shortcut-target">children</div>
    </KeyboardShortcutProvider>,
  );
  const target = screen.getByTestId("shortcut-target");
  return { ...result, target };
}

describe("KeyboardShortcutProvider", () => {
  afterEach(() => {
    // Clean up any lingering event listeners by unmounting
    // (render's cleanup handles this via vitest's auto-cleanup)
  });

  describe("Cmd/Ctrl+K routing", () => {
    it("calls onWorkspaceSwitcher when provided", () => {
      const onWorkspaceSwitcher = vi.fn();
      const onSearchFocus = vi.fn();

      const { target } = renderProvider({ onWorkspaceSwitcher, onSearchFocus });

      fireEvent.keyDown(target, { key: "k", metaKey: true });

      expect(onWorkspaceSwitcher).toHaveBeenCalledTimes(1);
      expect(onSearchFocus).not.toHaveBeenCalled();
    });

    it("calls onSearchFocus when onWorkspaceSwitcher is NOT provided", () => {
      const onSearchFocus = vi.fn();

      const { target } = renderProvider({ onSearchFocus });

      fireEvent.keyDown(target, { key: "k", metaKey: true });

      expect(onSearchFocus).toHaveBeenCalledTimes(1);
    });

    it("calls onWorkspaceSwitcher with Ctrl+K", () => {
      const onWorkspaceSwitcher = vi.fn();

      const { target } = renderProvider({ onWorkspaceSwitcher });

      fireEvent.keyDown(target, { key: "k", ctrlKey: true });

      expect(onWorkspaceSwitcher).toHaveBeenCalledTimes(1);
    });

    it("calls onSearchFocus with Ctrl+K when no workspace switcher", () => {
      const onSearchFocus = vi.fn();

      const { target } = renderProvider({ onSearchFocus });

      fireEvent.keyDown(target, { key: "k", ctrlKey: true });

      expect(onSearchFocus).toHaveBeenCalledTimes(1);
    });

    it("does nothing when neither onWorkspaceSwitcher nor onSearchFocus provided", () => {
      const { target } = renderProvider({});

      // Should not throw
      expect(() => {
        fireEvent.keyDown(target, { key: "k", metaKey: true });
      }).not.toThrow();
    });

    it("Cmd/Ctrl+K works even when focus is in an input", () => {
      const onWorkspaceSwitcher = vi.fn();

      const { container } = render(
        <KeyboardShortcutProvider onWorkspaceSwitcher={onWorkspaceSwitcher}>
          <input type="text" data-testid="text-input" />
        </KeyboardShortcutProvider>,
      );

      const input = container.querySelector("input")!;
      input.focus();

      fireEvent.keyDown(input, { key: "k", metaKey: true });

      expect(onWorkspaceSwitcher).toHaveBeenCalledTimes(1);
    });
  });

  describe("Cmd/Ctrl+Shift+1-9 workspace positional switching", () => {
    it("calls onWorkspacePositionalSwitch with index 0 for Cmd+Shift+1", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({ onWorkspacePositionalSwitch });

      fireEvent.keyDown(target, {
        key: "1",
        metaKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(1);
      expect(onWorkspacePositionalSwitch).toHaveBeenCalledWith(0);
    });

    it("calls onWorkspacePositionalSwitch with index 4 for Cmd+Shift+5", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({ onWorkspacePositionalSwitch });

      fireEvent.keyDown(target, {
        key: "5",
        metaKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(1);
      expect(onWorkspacePositionalSwitch).toHaveBeenCalledWith(4);
    });

    it("calls onWorkspacePositionalSwitch with index 8 for Cmd+Shift+9", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({ onWorkspacePositionalSwitch });

      fireEvent.keyDown(target, {
        key: "9",
        metaKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(1);
      expect(onWorkspacePositionalSwitch).toHaveBeenCalledWith(8);
    });

    it("works with Ctrl+Shift instead of Cmd+Shift", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({ onWorkspacePositionalSwitch });

      fireEvent.keyDown(target, {
        key: "3",
        ctrlKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(1);
      expect(onWorkspacePositionalSwitch).toHaveBeenCalledWith(2);
    });

    it("does not fire when onWorkspacePositionalSwitch is not provided", () => {
      const { target } = renderProvider({});

      // Without the callback, Cmd+Shift+1 should not throw
      expect(() => {
        fireEvent.keyDown(target, {
          key: "1",
          metaKey: true,
          shiftKey: true,
        });
      }).not.toThrow();
    });

    it("does not fire for Cmd+Shift+0 (out of range)", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({ onWorkspacePositionalSwitch });

      fireEvent.keyDown(target, {
        key: "0",
        metaKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).not.toHaveBeenCalled();
    });

    it("works even when focus is in an input", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { container } = render(
        <KeyboardShortcutProvider
          onWorkspacePositionalSwitch={onWorkspacePositionalSwitch}
        >
          <input type="text" />
        </KeyboardShortcutProvider>,
      );

      const input = container.querySelector("input")!;
      input.focus();

      fireEvent.keyDown(input, {
        key: "2",
        metaKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(1);
      expect(onWorkspacePositionalSwitch).toHaveBeenCalledWith(1);
    });

    it("calls with correct indices for all digits 1-9", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({ onWorkspacePositionalSwitch });

      for (let digit = 1; digit <= 9; digit++) {
        fireEvent.keyDown(target, {
          key: String(digit),
          metaKey: true,
          shiftKey: true,
        });
      }

      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(9);
      for (let digit = 1; digit <= 9; digit++) {
        expect(onWorkspacePositionalSwitch).toHaveBeenCalledWith(digit - 1);
      }
    });
  });

  describe("Cmd/Ctrl+Shift+digit does not interfere with plain digit view switching", () => {
    it("plain digit keys still call onViewChange", () => {
      const onViewChange = vi.fn();
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({
        onViewChange,
        onWorkspacePositionalSwitch,
      });

      // Plain "1" should trigger view switch, not workspace switch
      fireEvent.keyDown(target, { key: "1" });

      expect(onViewChange).toHaveBeenCalledTimes(1);
      expect(onViewChange).toHaveBeenCalledWith("kanban");
      expect(onWorkspacePositionalSwitch).not.toHaveBeenCalled();
    });

    it("Cmd+Shift+1 does not call onViewChange", () => {
      const onViewChange = vi.fn();
      const onWorkspacePositionalSwitch = vi.fn();

      const { target } = renderProvider({
        onViewChange,
        onWorkspacePositionalSwitch,
      });

      fireEvent.keyDown(target, {
        key: "1",
        metaKey: true,
        shiftKey: true,
      });

      expect(onViewChange).not.toHaveBeenCalled();
      expect(onWorkspacePositionalSwitch).toHaveBeenCalledTimes(1);
    });
  });
});
