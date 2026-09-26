/** @vitest-environment jsdom */

import "@testing-library/jest-dom";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  focusRootToggle,
  handleRootToggleKeyDown,
  ROOT_KEY_ATTR,
  TREE_SCROLL_ATTR,
} from "../rootRowKeyboard";

function RootToggle({
  rootKey,
  expanded = false,
  disabled = false,
  onToggle = vi.fn(),
}: {
  rootKey: string;
  expanded?: boolean;
  disabled?: boolean;
  onToggle?: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      {...{ [ROOT_KEY_ATTR]: rootKey }}
      onKeyDown={(event) =>
        handleRootToggleKeyDown(event, { expanded, onToggle })
      }
    >
      {rootKey}
    </button>
  );
}

describe("root row keyboard navigation", () => {
  it("focuses a root by key within the active tree scroll", () => {
    render(
      <>
        <div {...{ [TREE_SCROLL_ATTR]: "" }}>
          <RootToggle rootKey="shared" />
        </div>
        <div {...{ [TREE_SCROLL_ATTR]: "" }}>
          <RootToggle rootKey="shared" />
          <div role="tree" aria-label="Active tree" tabIndex={0} />
        </div>
      </>,
    );
    screen.getByRole("tree").focus();

    focusRootToggle("shared");

    expect(screen.getAllByRole("button", { name: "shared" })[1]).toHaveFocus();
  });

  it("moves between root toggles in DOM order and clamps at both ends", () => {
    render(
      <>
        <RootToggle rootKey="outside" />
        <div {...{ [TREE_SCROLL_ATTR]: "" }}>
          <RootToggle rootKey="first" />
          <div>Other content</div>
          <RootToggle rootKey="second" />
          <RootToggle rootKey="last" />
        </div>
      </>,
    );
    const first = screen.getByRole("button", { name: "first" });
    const second = screen.getByRole("button", { name: "second" });
    const last = screen.getByRole("button", { name: "last" });

    first.focus();
    fireEvent.keyDown(first, { key: "ArrowUp" });
    expect(first).toHaveFocus();

    fireEvent.keyDown(first, { key: "ArrowDown" });
    expect(second).toHaveFocus();
    fireEvent.keyDown(second, { key: "ArrowDown" });
    expect(last).toHaveFocus();
    fireEvent.keyDown(last, { key: "ArrowDown" });
    expect(last).toHaveFocus();

    fireEvent.keyDown(last, { key: "ArrowUp" });
    expect(second).toHaveFocus();
  });

  // An unavailable checkout renders its root row `disabled` (RootRow's
  // `disabled={!exists}`). A disabled button cannot take focus, so stepping
  // onto one would strand the user with no way forward — skip it instead.
  it("skips a disabled root rather than trapping focus on it", () => {
    render(
      <div {...{ [TREE_SCROLL_ATTR]: "" }}>
        <RootToggle rootKey="first" />
        <RootToggle rootKey="unavailable" disabled />
        <RootToggle rootKey="last" />
      </div>,
    );
    const first = screen.getByRole("button", { name: "first" });
    const last = screen.getByRole("button", { name: "last" });

    first.focus();
    fireEvent.keyDown(first, { key: "ArrowDown" });
    expect(last).toHaveFocus();

    fireEvent.keyDown(last, { key: "ArrowUp" });
    expect(first).toHaveFocus();
  });

  it("moves to the first and last root with Home and End", () => {
    render(
      <div {...{ [TREE_SCROLL_ATTR]: "" }}>
        <RootToggle rootKey="first" />
        <RootToggle rootKey="middle" />
        <RootToggle rootKey="last" />
      </div>,
    );
    const first = screen.getByRole("button", { name: "first" });
    const middle = screen.getByRole("button", { name: "middle" });
    const last = screen.getByRole("button", { name: "last" });

    middle.focus();
    fireEvent.keyDown(middle, { key: "End" });
    expect(last).toHaveFocus();
    fireEvent.keyDown(last, { key: "Home" });
    expect(first).toHaveFocus();
  });

  it("expands a collapsed root with ArrowRight", () => {
    const onToggle = vi.fn();
    render(<RootToggle rootKey="root" onToggle={onToggle} />);

    fireEvent.keyDown(screen.getByRole("button"), { key: "ArrowRight" });

    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("moves from an expanded root into its tree without toggling", () => {
    const onToggle = vi.fn();
    render(
      <div {...{ [TREE_SCROLL_ATTR]: "" }}>
        <RootToggle rootKey="root" expanded onToggle={onToggle} />
        <div role="tree" aria-label="Root files" tabIndex={0} />
        <RootToggle rootKey="next" />
        <div role="tree" aria-label="Next files" tabIndex={0} />
      </div>,
    );

    fireEvent.keyDown(screen.getByRole("button", { name: "root" }), {
      key: "ArrowRight",
    });

    expect(screen.getByRole("tree", { name: "Root files" })).toHaveFocus();
    expect(onToggle).not.toHaveBeenCalled();
  });

  it("does nothing when an expanded root has no tree before the next root", () => {
    const onToggle = vi.fn();
    render(
      <div {...{ [TREE_SCROLL_ATTR]: "" }}>
        <RootToggle rootKey="empty" expanded onToggle={onToggle} />
        <RootToggle rootKey="next" />
        <div role="tree" aria-label="Next files" tabIndex={0} />
      </div>,
    );
    const empty = screen.getByRole("button", { name: "empty" });
    empty.focus();

    fireEvent.keyDown(empty, { key: "ArrowRight" });

    expect(empty).toHaveFocus();
    expect(onToggle).not.toHaveBeenCalled();
  });

  it("collapses an expanded root with ArrowLeft but ignores a collapsed root", () => {
    const expandedToggle = vi.fn();
    const collapsedToggle = vi.fn();
    render(
      <>
        <RootToggle rootKey="expanded" expanded onToggle={expandedToggle} />
        <RootToggle rootKey="collapsed" onToggle={collapsedToggle} />
      </>,
    );

    fireEvent.keyDown(screen.getByRole("button", { name: "expanded" }), {
      key: "ArrowLeft",
    });
    fireEvent.keyDown(screen.getByRole("button", { name: "collapsed" }), {
      key: "ArrowLeft",
    });

    expect(expandedToggle).toHaveBeenCalledOnce();
    expect(collapsedToggle).not.toHaveBeenCalled();
  });
});
