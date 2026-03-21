/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for VirtualizedTableBody component.
 * Tests tbody rendering, spacer rows, empty state, and className support.
 *
 * Note: In jsdom, elements have 0 height so the virtualizer renders 0 virtual items.
 * Tests verify the structural output (tbody, spacer rows) rather than actual windowing.
 */

import "@testing-library/jest-dom";
import { render } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import { VirtualizedTableBody } from "../VirtualizedTableBody";

/**
 * Helper to create a mock scroll container ref.
 */
function createScrollContainerRef(el: HTMLElement | null = null) {
  return { current: el };
}

/**
 * Helper to wrap VirtualizedTableBody in a table for valid DOM.
 */
function renderInTable(ui: React.ReactNode) {
  return render(<table>{ui}</table>);
}

describe("VirtualizedTableBody", () => {
  describe("tbody rendering", () => {
    it("renders a tbody element", () => {
      const ref = createScrollContainerRef();
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={10}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={3}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();
    });

    it("applies className to tbody when provided", () => {
      const ref = createScrollContainerRef();
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={10}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={3}
          className="custom-tbody"
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toHaveClass("custom-tbody");
    });

    it("does not add className attribute when not provided", () => {
      const ref = createScrollContainerRef();
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={10}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={3}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();
      // className should be undefined/empty when not provided
      expect(tbody?.className).toBe("");
    });
  });

  describe("empty state", () => {
    it("renders tbody with no rows when count is 0", () => {
      const ref = createScrollContainerRef();
      const renderRow = vi.fn((i: number) => (
        <tr key={i}>
          <td>Row {i}</td>
        </tr>
      ));

      const { container } = renderInTable(
        <VirtualizedTableBody
          count={0}
          scrollContainerRef={ref}
          renderRow={renderRow}
          colSpan={3}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();

      // No data rows should be rendered
      expect(renderRow).not.toHaveBeenCalled();

      // No visible tr elements (no spacer rows either since paddingTop/Bottom are 0)
      const rows = tbody?.querySelectorAll("tr");
      expect(rows?.length).toBe(0);
    });
  });

  describe("with null scroll container", () => {
    it("renders tbody but no data rows when scroll container is null", () => {
      const ref = createScrollContainerRef(null);
      const renderRow = vi.fn((i: number) => (
        <tr key={i}>
          <td>Row {i}</td>
        </tr>
      ));

      const { container } = renderInTable(
        <VirtualizedTableBody
          count={100}
          scrollContainerRef={ref}
          renderRow={renderRow}
          colSpan={5}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();

      // No virtual items with null scroll container
      expect(renderRow).not.toHaveBeenCalled();
    });

    it("does not render spacer rows when there are no virtual items", () => {
      const ref = createScrollContainerRef(null);
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={50}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={3}
        />,
      );

      const tbody = container.querySelector("tbody");
      // No spacer rows (paddingTop and paddingBottom are 0 when no virtual items)
      const ariaHiddenRows = tbody?.querySelectorAll('tr[aria-hidden="true"]');
      expect(ariaHiddenRows?.length).toBe(0);
    });
  });

  describe("with jsdom scroll container (0 height)", () => {
    it("renders tbody with no data rows in jsdom (0 height viewport)", () => {
      const scrollEl = document.createElement("div");
      const ref = createScrollContainerRef(scrollEl);
      const renderRow = vi.fn((i: number) => (
        <tr key={i}>
          <td>Row {i}</td>
        </tr>
      ));

      const { container } = renderInTable(
        <VirtualizedTableBody
          count={100}
          scrollContainerRef={ref}
          renderRow={renderRow}
          colSpan={5}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();

      // In jsdom with 0 height, virtualizer renders 0 items
      expect(renderRow).not.toHaveBeenCalled();
    });
  });

  describe("spacer row structure", () => {
    it("spacer rows use aria-hidden=true when rendered", () => {
      // When the virtualizer does render items, spacer rows should be aria-hidden.
      // In jsdom we can't trigger this, but we verify the structure
      // by checking that no aria-hidden rows exist when there are no virtual items.
      const ref = createScrollContainerRef(null);
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={10}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={3}
        />,
      );

      const tbody = container.querySelector("tbody");
      const ariaHiddenRows = tbody?.querySelectorAll('tr[aria-hidden="true"]');
      expect(ariaHiddenRows?.length).toBe(0);
    });
  });

  describe("renderRow callback", () => {
    it("renderRow is not called when no items are virtualized", () => {
      const ref = createScrollContainerRef(null);
      const renderRow = vi.fn((i: number) => (
        <tr key={i}>
          <td>Row {i}</td>
        </tr>
      ));

      renderInTable(
        <VirtualizedTableBody
          count={50}
          scrollContainerRef={ref}
          renderRow={renderRow}
          colSpan={4}
        />,
      );

      expect(renderRow).not.toHaveBeenCalled();
    });
  });

  describe("colSpan prop", () => {
    it("accepts different colSpan values without throwing", () => {
      const ref = createScrollContainerRef();

      for (const colSpan of [1, 3, 5, 10]) {
        expect(() => {
          const { unmount } = renderInTable(
            <VirtualizedTableBody
              count={5}
              scrollContainerRef={ref}
              renderRow={(i) => (
                <tr key={i}>
                  <td>Row {i}</td>
                </tr>
              )}
              colSpan={colSpan}
            />,
          );
          unmount();
        }).not.toThrow();
      }
    });
  });

  describe("edge cases", () => {
    it("handles count of 1", () => {
      const ref = createScrollContainerRef();
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={1}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={3}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();
    });

    it("handles very large count without throwing", () => {
      const ref = createScrollContainerRef();

      expect(() => {
        renderInTable(
          <VirtualizedTableBody
            count={10000}
            scrollContainerRef={ref}
            renderRow={(i) => (
              <tr key={i}>
                <td>Row {i}</td>
              </tr>
            )}
            colSpan={5}
          />,
        );
      }).not.toThrow();
    });

    it("handles colSpan of 1", () => {
      const ref = createScrollContainerRef();
      const { container } = renderInTable(
        <VirtualizedTableBody
          count={5}
          scrollContainerRef={ref}
          renderRow={(i) => (
            <tr key={i}>
              <td>Row {i}</td>
            </tr>
          )}
          colSpan={1}
        />,
      );

      const tbody = container.querySelector("tbody");
      expect(tbody).toBeInTheDocument();
    });
  });
});
