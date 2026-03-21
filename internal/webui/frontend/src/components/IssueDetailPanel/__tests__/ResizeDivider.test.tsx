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
