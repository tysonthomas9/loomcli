/**
 * @vitest-environment jsdom
 */
import React from "react";
import { render, act } from "@testing-library/react";
import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";

import {
  KeyboardShortcutProvider,
  useRegisterEscapeLayer,
  resetEscapeRegistry,
  LAYER_MODAL,
  LAYER_ISSUE_PANEL,
} from "../useKeyboardShortcuts";

describe("useKeyboardShortcuts", () => {
  afterEach(() => {
    resetEscapeRegistry();
  });

  describe("Escape layer registry isolation", () => {
    it("two independent providers have separate escape layer registries", () => {
      const handlerA = vi.fn();
      const handlerB = vi.fn();

      function LayerA() {
        useRegisterEscapeLayer(LAYER_MODAL, handlerA);
        return null;
      }

      function LayerB() {
        useRegisterEscapeLayer(LAYER_ISSUE_PANEL, handlerB);
        return null;
      }

      // Mount two separate providers
      const { unmount: unmountA } = render(
        <KeyboardShortcutProvider>
          <LayerA />
        </KeyboardShortcutProvider>,
      );

      const { unmount: unmountB } = render(
        <KeyboardShortcutProvider>
          <LayerB />
        </KeyboardShortcutProvider>,
      );

      // Dispatch Escape
      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      // Both handlers should fire because both providers listen on the document
      // Each provider's highest-priority handler fires independently
      expect(handlerA).toHaveBeenCalledTimes(1);
      expect(handlerB).toHaveBeenCalledTimes(1);

      unmountA();
      unmountB();
    });

    it("unmounting a provider cleans up its escape listener", () => {
      const handler = vi.fn();

      function Layer() {
        useRegisterEscapeLayer(LAYER_MODAL, handler);
        return null;
      }

      const { unmount } = render(
        <KeyboardShortcutProvider>
          <Layer />
        </KeyboardShortcutProvider>,
      );

      // Unmount the provider
      unmount();

      // Dispatch Escape — handler should NOT fire
      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      expect(handler).not.toHaveBeenCalled();
    });

    it("module-level fallback works for standalone useRegisterEscapeLayer", () => {
      const handler = vi.fn();

      // Render WITHOUT a provider
      function StandaloneLayer() {
        useRegisterEscapeLayer(LAYER_MODAL, handler);
        return null;
      }

      const { unmount } = render(<StandaloneLayer />);

      // Dispatch Escape — handler should fire via fallback
      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      expect(handler).toHaveBeenCalledTimes(1);

      unmount();
    });
  });

  describe("Layer priority", () => {
    it("highest priority handler fires on Escape", () => {
      const lowHandler = vi.fn();
      const highHandler = vi.fn();

      function LowLayer() {
        useRegisterEscapeLayer(LAYER_ISSUE_PANEL, lowHandler);
        return null;
      }

      function HighLayer() {
        useRegisterEscapeLayer(LAYER_MODAL, highHandler);
        return null;
      }

      const { unmount } = render(
        <KeyboardShortcutProvider>
          <LowLayer />
          <HighLayer />
        </KeyboardShortcutProvider>,
      );

      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      expect(highHandler).toHaveBeenCalledTimes(1);
      expect(lowHandler).not.toHaveBeenCalled();

      unmount();
    });

    it("inactive layers are not registered", () => {
      const handler = vi.fn();

      function InactiveLayer() {
        useRegisterEscapeLayer(LAYER_MODAL, handler, false);
        return null;
      }

      const { unmount } = render(
        <KeyboardShortcutProvider>
          <InactiveLayer />
        </KeyboardShortcutProvider>,
      );

      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      expect(handler).not.toHaveBeenCalled();

      unmount();
    });
  });

  describe("afterEach module state reset", () => {
    it("first test registers a layer", () => {
      const handler = vi.fn();

      function Layer() {
        useRegisterEscapeLayer(LAYER_MODAL, handler);
        return null;
      }

      const { unmount } = render(<Layer />);

      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      expect(handler).toHaveBeenCalledTimes(1);
      unmount();
      // afterEach calls resetEscapeRegistry()
    });

    it("second test does not see the first test's layer", () => {
      const handler = vi.fn();

      // This handler should not be called — if it is, state leaked from previous test
      // Instead, register a fresh handler
      function FreshLayer() {
        useRegisterEscapeLayer(LAYER_ISSUE_PANEL, handler);
        return null;
      }

      const { unmount } = render(<FreshLayer />);

      act(() => {
        document.dispatchEvent(
          new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
        );
      });

      // Only this test's handler should fire (1 time, not 2)
      expect(handler).toHaveBeenCalledTimes(1);
      unmount();
    });
  });
});
