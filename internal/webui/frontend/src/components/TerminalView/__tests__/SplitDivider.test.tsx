/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SplitDivider component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { createRef } from "react";

import "@testing-library/jest-dom";
import { SplitDivider } from "../SplitDivider";

function renderDivider(
  onRatioChange = vi.fn(),
  containerWidth = 1000,
  containerLeft = 0,
) {
  const containerRef = createRef<HTMLDivElement>();

  const result = render(
    <div>
      <div
        ref={containerRef}
        data-testid="container"
        style={{ width: containerWidth }}
      />
      <SplitDivider onRatioChange={onRatioChange} containerRef={containerRef} />
    </div>,
  );

  // Mock getBoundingClientRect on the container
  const container = screen.getByTestId("container");
  vi.spyOn(container, "getBoundingClientRect").mockReturnValue({
    left: containerLeft,
    top: 0,
    right: containerLeft + containerWidth,
    bottom: 500,
    width: containerWidth,
    height: 500,
    x: containerLeft,
    y: 0,
    toJSON: () => {},
  });

  return { ...result, onRatioChange, container };
}

describe("SplitDivider", () => {
  describe("rendering", () => {
    it("renders with correct role and aria attributes", () => {
      renderDivider();

      const divider = screen.getByTestId("split-divider");
      expect(divider).toBeInTheDocument();
      expect(divider).toHaveAttribute("role", "separator");
      expect(divider).toHaveAttribute("aria-orientation", "vertical");
      expect(divider).toHaveAttribute("aria-label", "Resize split panes");
    });
  });

  describe("double-click reset", () => {
    it("calls onRatioChange with 0.5 (default) on double-click", () => {
      const onRatioChange = vi.fn();
      renderDivider(onRatioChange);

      const divider = screen.getByTestId("split-divider");
      fireEvent.doubleClick(divider);

      expect(onRatioChange).toHaveBeenCalledTimes(1);
      expect(onRatioChange).toHaveBeenCalledWith(0.5);
    });
  });

  describe("pointer drag", () => {
    it("calls onRatioChange with computed ratio during drag", () => {
      const onRatioChange = vi.fn();
      renderDivider(onRatioChange, 1000, 0);

      const divider = screen.getByTestId("split-divider");

      // Mock setPointerCapture
      divider.setPointerCapture = vi.fn();

      // Start drag
      fireEvent.pointerDown(divider, { pointerId: 1 });

      // Simulate pointer move at clientX=300 (ratio = 300/1000 = 0.3)
      fireEvent(
        document,
        new PointerEvent("pointermove", {
          clientX: 300,
          bubbles: true,
        }),
      );

      expect(onRatioChange).toHaveBeenCalledWith(0.3);
    });

    it("clamps ratio to MIN_SPLIT_RATIO (0.2) when dragged too far left", () => {
      const onRatioChange = vi.fn();
      renderDivider(onRatioChange, 1000, 0);

      const divider = screen.getByTestId("split-divider");
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 1 });

      // Simulate pointer move at clientX=50 (ratio = 50/1000 = 0.05, clamped to 0.2)
      fireEvent(
        document,
        new PointerEvent("pointermove", {
          clientX: 50,
          bubbles: true,
        }),
      );

      expect(onRatioChange).toHaveBeenCalledWith(0.2);
    });

    it("clamps ratio to MAX_SPLIT_RATIO (0.8) when dragged too far right", () => {
      const onRatioChange = vi.fn();
      renderDivider(onRatioChange, 1000, 0);

      const divider = screen.getByTestId("split-divider");
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 1 });

      // Simulate pointer move at clientX=950 (ratio = 950/1000 = 0.95, clamped to 0.8)
      fireEvent(
        document,
        new PointerEvent("pointermove", {
          clientX: 950,
          bubbles: true,
        }),
      );

      expect(onRatioChange).toHaveBeenCalledWith(0.8);
    });

    it("stops updating ratio after pointer up", () => {
      const onRatioChange = vi.fn();
      renderDivider(onRatioChange, 1000, 0);

      const divider = screen.getByTestId("split-divider");
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 1 });

      // First move
      fireEvent(
        document,
        new PointerEvent("pointermove", {
          clientX: 400,
          bubbles: true,
        }),
      );
      expect(onRatioChange).toHaveBeenCalledTimes(1);

      // Release
      fireEvent(document, new PointerEvent("pointerup", { bubbles: true }));

      // Second move after release should not trigger
      onRatioChange.mockClear();
      fireEvent(
        document,
        new PointerEvent("pointermove", {
          clientX: 600,
          bubbles: true,
        }),
      );
      expect(onRatioChange).not.toHaveBeenCalled();
    });

    it("accounts for container offset when computing ratio", () => {
      const onRatioChange = vi.fn();
      // Container starts at x=200, width=800
      renderDivider(onRatioChange, 800, 200);

      const divider = screen.getByTestId("split-divider");
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 1 });

      // clientX=600 => x relative to container = 600 - 200 = 400
      // ratio = 400 / 800 = 0.5
      fireEvent(
        document,
        new PointerEvent("pointermove", {
          clientX: 600,
          bubbles: true,
        }),
      );

      expect(onRatioChange).toHaveBeenCalledWith(0.5);
    });

    it("sets cursor to col-resize on pointer down", () => {
      renderDivider();

      const divider = screen.getByTestId("split-divider");
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 1 });

      expect(document.body.style.cursor).toBe("col-resize");
      expect(document.body.style.userSelect).toBe("none");
    });

    it("resets cursor on pointer up", () => {
      renderDivider();

      const divider = screen.getByTestId("split-divider");
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 1 });
      expect(document.body.style.cursor).toBe("col-resize");

      fireEvent(document, new PointerEvent("pointerup", { bubbles: true }));

      expect(document.body.style.cursor).toBe("");
      expect(document.body.style.userSelect).toBe("");
    });
  });
});
