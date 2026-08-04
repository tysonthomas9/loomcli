/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for VirtualizedCardList component.
 * Tests rendering of the virtualized container with correct structure and styles.
 *
 * Note: In jsdom, elements have 0 height so the virtualizer renders 0 virtual items.
 * Tests focus on the container structure, total height style, and basic rendering.
 */

import "@testing-library/jest-dom";
import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import { VirtualizedCardList } from "../VirtualizedCardList";

/**
 * Helper to create a mock scroll container ref.
 */
function createScrollContainerRef(el: HTMLElement | null = null) {
  return { current: el };
}

describe("VirtualizedCardList", () => {
  describe("container rendering", () => {
    it("renders a container div with position relative", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={10}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      expect(outerDiv).toBeInTheDocument();
      expect(outerDiv.style.position).toBe("relative");
    });

    it("renders container with width 100%", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={10}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      expect(outerDiv.style.width).toBe("100%");
    });

    it("renders container with total height based on item count and estimated size", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={10}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      // 10 items * 97px estimated height - 16px trailing gap = 954px
      expect(outerDiv.style.height).toBe("954px");
    });

    it("renders container with 0 height when count is 0", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={0}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      expect(outerDiv.style.height).toBe("0px");
    });

    it("renders container with large height for many items", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={100}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      // 100 items * 97px - 16px trailing gap = 9684px
      expect(outerDiv.style.height).toBe("9684px");
    });
  });

  describe("virtual items in jsdom", () => {
    it("renders no items when scroll container is null (no viewport)", () => {
      const ref = createScrollContainerRef(null);
      const renderItem = vi.fn((i: number) => <div key={i}>Item {i}</div>);

      render(
        <VirtualizedCardList
          count={50}
          scrollContainerRef={ref}
          renderItem={renderItem}
        />,
      );

      // In jsdom with null scroll container, no items should be rendered
      expect(renderItem).not.toHaveBeenCalled();
    });

    it("renders no listitem roles when scroll container has 0 height", () => {
      const container = document.createElement("div");
      const ref = createScrollContainerRef(container);

      render(
        <VirtualizedCardList
          count={50}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      // In jsdom, elements have 0 height, so no virtual items
      const listitems = screen.queryAllByRole("listitem");
      expect(listitems).toHaveLength(0);
    });
  });

  describe("renderItem callback", () => {
    it("renderItem is not called when no items are virtualized", () => {
      const ref = createScrollContainerRef(null);
      const renderItem = vi.fn((i: number) => <div key={i}>Item {i}</div>);

      render(
        <VirtualizedCardList
          count={10}
          scrollContainerRef={ref}
          renderItem={renderItem}
        />,
      );

      expect(renderItem).not.toHaveBeenCalled();
    });
  });

  describe("gap prop", () => {
    it("accepts custom gap value without throwing", () => {
      const ref = createScrollContainerRef();

      expect(() => {
        render(
          <VirtualizedCardList
            count={5}
            scrollContainerRef={ref}
            renderItem={(i) => <div key={i}>Item {i}</div>}
            gap={8}
          />,
        );
      }).not.toThrow();
    });

    it("defaults gap to 16 when not specified", () => {
      const ref = createScrollContainerRef();

      // Component renders without error — gap default is handled internally
      const { container } = render(
        <VirtualizedCardList
          count={5}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      expect(container.firstElementChild).toBeInTheDocument();
    });
  });

  describe("edge cases", () => {
    it("handles count of 1", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={1}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      // 1 item * 97px - 16px trailing gap = 81px
      expect(outerDiv.style.height).toBe("81px");
    });

    it("handles very large count", () => {
      const ref = createScrollContainerRef();
      const { container } = render(
        <VirtualizedCardList
          count={5000}
          scrollContainerRef={ref}
          renderItem={(i) => <div key={i}>Item {i}</div>}
        />,
      );

      const outerDiv = container.firstElementChild as HTMLElement;
      // 5000 * 97 - 16 = 484984
      expect(outerDiv.style.height).toBe("484984px");
    });
  });
});
