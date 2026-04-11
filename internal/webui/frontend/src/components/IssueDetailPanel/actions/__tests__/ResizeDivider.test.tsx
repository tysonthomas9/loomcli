/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ResizeDivider component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";

import { ResizeDivider } from "../ResizeDivider";

describe("ResizeDivider", () => {
  describe("ARIA attributes", () => {
    it("renders with role=separator", () => {
      render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      expect(divider).toBeInTheDocument();
    });

    it("renders with aria-orientation=horizontal", () => {
      render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      expect(divider).toHaveAttribute("aria-orientation", "horizontal");
    });

    it("renders aria-valuenow reflecting the ratio as a percentage", () => {
      render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.7}
        />,
      );

      const divider = screen.getByRole("separator");
      expect(divider).toHaveAttribute("aria-valuenow", "70");
    });

    it("renders aria-valuemin and aria-valuemax", () => {
      render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      expect(divider).toHaveAttribute("aria-valuemin", "15");
      expect(divider).toHaveAttribute("aria-valuemax", "85");
    });
  });

  describe("double-click", () => {
    it("calls onDoubleClick handler", () => {
      const onDoubleClick = vi.fn();

      render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={onDoubleClick}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      fireEvent.doubleClick(divider);

      expect(onDoubleClick).toHaveBeenCalledTimes(1);
    });
  });

  describe("unmount during drag", () => {
    it("releases pointer capture on unmount during active drag", () => {
      const { unmount } = render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      const releasePointerCapture = vi.fn();
      divider.releasePointerCapture = releasePointerCapture;
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 42, clientY: 100 });

      unmount();

      expect(releasePointerCapture).toHaveBeenCalledTimes(1);
      expect(releasePointerCapture).toHaveBeenCalledWith(42);
    });

    it("does not release pointer capture on unmount when not dragging", () => {
      const { unmount } = render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      const releasePointerCapture = vi.fn();
      divider.releasePointerCapture = releasePointerCapture;

      unmount();

      expect(releasePointerCapture).not.toHaveBeenCalled();
    });

    it("clears pointer capture state after pointerUp so cleanup does not double-release", () => {
      const { unmount } = render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      const releasePointerCapture = vi.fn();
      divider.releasePointerCapture = releasePointerCapture;
      divider.setPointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 42, clientY: 100 });
      fireEvent.pointerUp(divider, { pointerId: 42, clientY: 120 });

      expect(releasePointerCapture).toHaveBeenCalledTimes(1);

      unmount();

      // Should not have been called again during cleanup
      expect(releasePointerCapture).toHaveBeenCalledTimes(1);
    });

    it("data-dragging attribute is false after unmount and remount", () => {
      const { unmount } = render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      divider.setPointerCapture = vi.fn();
      divider.releasePointerCapture = vi.fn();

      fireEvent.pointerDown(divider, { pointerId: 42, clientY: 100 });
      expect(divider).toHaveAttribute("data-dragging", "true");

      unmount();

      // Remount
      render(
        <ResizeDivider
          onDragDelta={vi.fn()}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const newDivider = screen.getByRole("separator");
      expect(newDivider).toHaveAttribute("data-dragging", "false");
    });
  });

  describe("keyboard support", () => {
    it("ArrowUp triggers onDragDelta(-20)", () => {
      const onDragDelta = vi.fn();

      render(
        <ResizeDivider
          onDragDelta={onDragDelta}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      fireEvent.keyDown(divider, { key: "ArrowUp" });

      expect(onDragDelta).toHaveBeenCalledWith(-20);
    });

    it("ArrowDown triggers onDragDelta(20)", () => {
      const onDragDelta = vi.fn();

      render(
        <ResizeDivider
          onDragDelta={onDragDelta}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      fireEvent.keyDown(divider, { key: "ArrowDown" });

      expect(onDragDelta).toHaveBeenCalledWith(20);
    });

    it("Home triggers large negative delta (-9999)", () => {
      const onDragDelta = vi.fn();

      render(
        <ResizeDivider
          onDragDelta={onDragDelta}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      fireEvent.keyDown(divider, { key: "Home" });

      expect(onDragDelta).toHaveBeenCalledWith(-9999);
    });

    it("End triggers large positive delta (9999)", () => {
      const onDragDelta = vi.fn();

      render(
        <ResizeDivider
          onDragDelta={onDragDelta}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      fireEvent.keyDown(divider, { key: "End" });

      expect(onDragDelta).toHaveBeenCalledWith(9999);
    });

    it("does not trigger onDragDelta for unrelated keys", () => {
      const onDragDelta = vi.fn();

      render(
        <ResizeDivider
          onDragDelta={onDragDelta}
          onDoubleClick={vi.fn()}
          ratio={0.5}
        />,
      );

      const divider = screen.getByRole("separator");
      fireEvent.keyDown(divider, { key: "Tab" });
      fireEvent.keyDown(divider, { key: "Enter" });

      expect(onDragDelta).not.toHaveBeenCalled();
    });
  });
});
