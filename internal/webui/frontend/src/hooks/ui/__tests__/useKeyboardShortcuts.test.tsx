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
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  KeyboardShortcutProvider,
  useRegisterEscapeLayer,
  LAYER_MODAL,
  LAYER_ISSUE_PANEL,
} from "../useKeyboardShortcuts";

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

    it("does not steal Ctrl+K from wterm input", () => {
      const onWorkspaceSwitcher = vi.fn();

      render(
        <KeyboardShortcutProvider onWorkspaceSwitcher={onWorkspaceSwitcher}>
          <div className="wterm" data-testid="terminal-target" />
        </KeyboardShortcutProvider>,
      );

      fireEvent.keyDown(screen.getByTestId("terminal-target"), {
        key: "k",
        ctrlKey: true,
      });

      expect(onWorkspaceSwitcher).not.toHaveBeenCalled();
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

    it("does not steal positional shortcuts from wterm input", () => {
      const onWorkspacePositionalSwitch = vi.fn();

      render(
        <KeyboardShortcutProvider
          onWorkspacePositionalSwitch={onWorkspacePositionalSwitch}
        >
          <div className="wterm" data-testid="terminal-target" />
        </KeyboardShortcutProvider>,
      );

      fireEvent.keyDown(screen.getByTestId("terminal-target"), {
        key: "2",
        metaKey: true,
        shiftKey: true,
      });

      expect(onWorkspacePositionalSwitch).not.toHaveBeenCalled();
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

    it("plain digit keys do not switch views from wterm input", () => {
      const onViewChange = vi.fn();

      render(
        <KeyboardShortcutProvider onViewChange={onViewChange}>
          <div className="wterm" data-testid="terminal-target" />
        </KeyboardShortcutProvider>,
      );

      fireEvent.keyDown(screen.getByTestId("terminal-target"), { key: "1" });

      expect(onViewChange).not.toHaveBeenCalled();
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

// ---------------------------------------------------------------------------
// Helper component for escape layer tests
// ---------------------------------------------------------------------------
function EscapeLayerTestComponent({
  handler,
  priority,
  active = true,
}: {
  handler: () => void;
  priority: number;
  active?: boolean;
}) {
  useRegisterEscapeLayer(priority, handler, active);
  return null;
}

describe("Escape layer registry isolation", () => {
  it("two independent providers have separate escape layer registries", () => {
    const handler1 = vi.fn();
    const handler2 = vi.fn();

    // Provider 1 with a modal-level handler
    render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent handler={handler1} priority={LAYER_MODAL} />
      </KeyboardShortcutProvider>,
    );

    // Provider 2 with a panel-level handler
    render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent
          handler={handler2}
          priority={LAYER_ISSUE_PANEL}
        />
      </KeyboardShortcutProvider>,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    // Both fire independently — each provider has its own registry
    expect(handler1).toHaveBeenCalledTimes(1);
    expect(handler2).toHaveBeenCalledTimes(1);
  });

  it("unmounting a provider cleans up its escape listener", () => {
    const handler = vi.fn();

    const { unmount } = render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent handler={handler} priority={LAYER_MODAL} />
      </KeyboardShortcutProvider>,
    );

    unmount();

    fireEvent.keyDown(document, { key: "Escape" });

    expect(handler).not.toHaveBeenCalled();
  });

  it("unmounting one provider does not affect another", () => {
    const handler1 = vi.fn();
    const handler2 = vi.fn();

    const { unmount: unmount1 } = render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent handler={handler1} priority={LAYER_MODAL} />
      </KeyboardShortcutProvider>,
    );

    render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent
          handler={handler2}
          priority={LAYER_ISSUE_PANEL}
        />
      </KeyboardShortcutProvider>,
    );

    // Unmount provider 1
    unmount1();

    fireEvent.keyDown(document, { key: "Escape" });

    // Provider 1's handler should not fire (cleaned up)
    expect(handler1).not.toHaveBeenCalled();
    // Provider 2's handler should still fire
    expect(handler2).toHaveBeenCalledTimes(1);
  });

  it("throws when used outside KeyboardShortcutProvider", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    const handler = vi.fn();

    expect(() =>
      render(
        <EscapeLayerTestComponent handler={handler} priority={LAYER_MODAL} />,
      ),
    ).toThrow(
      "useRegisterEscapeLayer must be used within a KeyboardShortcutProvider",
    );

    spy.mockRestore();
  });

  it("priority chain works within a single provider", () => {
    const lowHandler = vi.fn();
    const highHandler = vi.fn();

    render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent
          handler={lowHandler}
          priority={LAYER_ISSUE_PANEL}
        />
        <EscapeLayerTestComponent
          handler={highHandler}
          priority={LAYER_MODAL}
        />
      </KeyboardShortcutProvider>,
    );

    fireEvent.keyDown(document, { key: "Escape" });

    // Only the highest-priority layer fires
    expect(highHandler).toHaveBeenCalledTimes(1);
    expect(lowHandler).not.toHaveBeenCalled();
  });

  it("deactivating a layer removes it from the registry", () => {
    const handler = vi.fn();

    const { rerender } = render(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent
          handler={handler}
          priority={LAYER_MODAL}
          active={true}
        />
      </KeyboardShortcutProvider>,
    );

    // Layer is active — handler should fire
    fireEvent.keyDown(document, { key: "Escape" });
    expect(handler).toHaveBeenCalledTimes(1);

    handler.mockClear();

    // Re-render with active=false — layer should be removed
    rerender(
      <KeyboardShortcutProvider>
        <EscapeLayerTestComponent
          handler={handler}
          priority={LAYER_MODAL}
          active={false}
        />
      </KeyboardShortcutProvider>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    expect(handler).not.toHaveBeenCalled();
  });

  it("rapid mount/unmount cycles do not leak layers", () => {
    const handler = vi.fn();

    // Mount and unmount a provider with an escape layer 5 times
    for (let i = 0; i < 5; i++) {
      const { unmount } = render(
        <KeyboardShortcutProvider>
          <EscapeLayerTestComponent handler={handler} priority={LAYER_MODAL} />
        </KeyboardShortcutProvider>,
      );
      unmount();
    }

    // After all unmounts, no handler should fire
    fireEvent.keyDown(document, { key: "Escape" });
    expect(handler).not.toHaveBeenCalled();
  });
});
