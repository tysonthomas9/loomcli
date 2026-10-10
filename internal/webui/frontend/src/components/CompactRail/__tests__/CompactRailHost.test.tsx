/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { CompactRailHost } from "../CompactRailHost";

describe("CompactRailHost", () => {
  it("renders a hover tooltip label in a portal", () => {
    render(<CompactRailHost label="hello-world">H</CompactRailHost>);
    const host = screen.getByLabelText("hello-world");
    expect(host).toHaveTextContent("H");
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    fireEvent.mouseEnter(host);
    expect(screen.getByRole("tooltip")).toHaveTextContent("hello-world");
  });
});

// jsdom has no layout: the anchor and the tooltip report the given boxes.
function layout(
  anchor: { left: number; top: number },
  tip: { width: number; height: number },
) {
  Object.defineProperty(window, "innerWidth", {
    value: 390,
    configurable: true,
  });
  return vi
    .spyOn(HTMLElement.prototype, "getBoundingClientRect")
    .mockImplementation(function (this: HTMLElement) {
      const r =
        this.getAttribute("role") === "tooltip"
          ? { left: 0, top: 0, ...tip }
          : { ...anchor, width: 30, height: 30 };
      return {
        ...r,
        x: r.left,
        y: r.top,
        right: r.left + r.width,
        bottom: r.top + r.height,
        toJSON: () => r,
      } as DOMRect;
    });
}

describe("CompactRailHost tooltip near the viewport edges (MB1)", () => {
  afterEach(() => vi.restoreAllMocks());

  it("goes below its anchor when there is no room above", () => {
    layout({ left: 340, top: 4 }, { width: 100, height: 20 });
    render(<CompactRailHost label="Add workspace">+</CompactRailHost>);
    fireEvent.mouseEnter(screen.getByLabelText("Add workspace"));
    const tip = screen.getByRole("tooltip");
    expect(tip.dataset.placement).toBe("bottom");
    expect(tip.style.top).toBe("42px");
    expect(tip.style.left).toBe("286px");
  });

  it("caps a label wider than the viewport and keeps it on screen", () => {
    layout({ left: 340, top: 800 }, { width: 500, height: 20 });
    render(<CompactRailHost label={"x".repeat(80)}>+</CompactRailHost>);
    fireEvent.mouseEnter(screen.getByLabelText("x".repeat(80)));
    const tip = screen.getByRole("tooltip");
    expect(tip.style.maxWidth).toBe("calc(100vw - 8px)");
    expect(tip.style.left).toBe("4px");
    expect(tip.dataset.placement).toBe("top");
  });
});
