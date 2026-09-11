/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TabMarkers — the per-tab marker slot.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { TabMarkers } from "../TabMarkers";

describe("TabMarkers", () => {
  it("renders the restart marker when replacedAt is set", () => {
    render(<TabMarkers tabId="tab-1" replacedAt="2026-08-15T10:00:00Z" />);

    expect(screen.getByTestId("tab-marker-restart-tab-1")).toBeInTheDocument();
  });

  it("renders nothing when replacedAt is undefined", () => {
    const { container } = render(<TabMarkers tabId="tab-1" />);

    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when replacedAt was dismissed to an empty string", () => {
    const { container } = render(<TabMarkers tabId="tab-1" replacedAt="" />);

    expect(container).toBeEmptyDOMElement();
  });

  it("names the restart time in the accessible label", () => {
    const replacedAt = "2026-08-15T10:00:00Z";
    render(<TabMarkers tabId="tab-1" replacedAt={replacedAt} />);

    const marker = screen.getByTestId("tab-marker-restart-tab-1");
    const label = marker.getAttribute("aria-label") ?? "";
    expect(label).toContain("Session restarted");
    expect(label).toContain(new Date(replacedAt).toLocaleString());
  });

  it("falls back to the raw value when the timestamp is unparseable", () => {
    render(<TabMarkers tabId="tab-1" replacedAt="not-a-date" />);

    expect(
      screen.getByTestId("tab-marker-restart-tab-1").getAttribute("aria-label"),
    ).toContain("not-a-date");
  });

  it("fires the dismiss callback on click and the marker then disappears", () => {
    const onDismissRestartNotice = vi.fn();
    const { rerender } = render(
      <TabMarkers
        tabId="tab-1"
        replacedAt="2026-08-15T10:00:00Z"
        onDismissRestartNotice={onDismissRestartNotice}
      />,
    );

    fireEvent.click(screen.getByTestId("tab-marker-restart-tab-1"));
    expect(onDismissRestartNotice).toHaveBeenCalledTimes(1);

    // The dismiss is persisted as an empty string; re-render as the parent would.
    rerender(
      <TabMarkers
        tabId="tab-1"
        replacedAt=""
        onDismissRestartNotice={onDismissRestartNotice}
      />,
    );
    expect(screen.queryByTestId("tab-marker-restart-tab-1")).toBeNull();
  });

  it("does not bubble the click to the tab underneath", () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <TabMarkers
          tabId="tab-1"
          replacedAt="2026-08-15T10:00:00Z"
          onDismissRestartNotice={vi.fn()}
        />
      </div>,
    );

    fireEvent.click(screen.getByTestId("tab-marker-restart-tab-1"));
    expect(onParentClick).not.toHaveBeenCalled();
  });

  it("renders a non-interactive marker in static mode", () => {
    const onDismissRestartNotice = vi.fn();
    render(
      <TabMarkers
        tabId="tab-1"
        replacedAt="2026-08-15T10:00:00Z"
        onDismissRestartNotice={onDismissRestartNotice}
        static
      />,
    );

    const marker = screen.getByTestId("tab-marker-restart-tab-1");
    expect(marker.tagName).toBe("SPAN");
    fireEvent.click(marker);
    expect(onDismissRestartNotice).not.toHaveBeenCalled();
  });
});
