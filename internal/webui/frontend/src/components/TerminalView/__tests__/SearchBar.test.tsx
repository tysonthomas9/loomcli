/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { SearchBar } from "../SearchBar";

vi.mock("../TerminalView.module.css", () => ({
  default: {
    searchOverlay: "searchOverlay",
    searchInput: "searchInput",
    searchButton: "searchButton",
    searchToggle: "searchToggle",
    searchToggleActive: "searchToggleActive",
    searchCounter: "searchCounter",
    noResults: "noResults",
  },
}));

const defaultProps = {
  value: "",
  onSearch: vi.fn(),
  onFindNext: vi.fn(),
  onFindPrevious: vi.fn(),
  onClose: vi.fn(),
  matchIndex: null as number | null,
  matchCount: null as number | null,
  caseSensitive: false,
  regex: false,
  onToggleCaseSensitive: vi.fn(),
  onToggleRegex: vi.fn(),
};

describe("SearchBar", () => {
  it("renders toggle buttons", () => {
    render(<SearchBar {...defaultProps} />);

    expect(screen.getByTestId("search-toggle-case")).toBeInTheDocument();
    expect(screen.getByTestId("search-toggle-regex")).toBeInTheDocument();
  });

  it("case-sensitive toggle shows active style when enabled", () => {
    render(<SearchBar {...defaultProps} caseSensitive={true} />);

    const toggle = screen.getByTestId("search-toggle-case");
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(toggle.className).toContain("searchToggleActive");
  });

  it("regex toggle shows active style when enabled", () => {
    render(<SearchBar {...defaultProps} regex={true} />);

    const toggle = screen.getByTestId("search-toggle-regex");
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(toggle.className).toContain("searchToggleActive");
  });

  it("clicking case-sensitive toggle calls onToggleCaseSensitive", () => {
    const onToggle = vi.fn();
    render(<SearchBar {...defaultProps} onToggleCaseSensitive={onToggle} />);

    fireEvent.click(screen.getByTestId("search-toggle-case"));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("clicking regex toggle calls onToggleRegex", () => {
    const onToggle = vi.fn();
    render(<SearchBar {...defaultProps} onToggleRegex={onToggle} />);

    fireEvent.click(screen.getByTestId("search-toggle-regex"));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("shows no counter when value is empty", () => {
    render(
      <SearchBar {...defaultProps} value="" matchIndex={0} matchCount={5} />,
    );

    expect(screen.queryByText(/of/)).not.toBeInTheDocument();
  });

  it("shows 'N of M' when matches exist", () => {
    render(
      <SearchBar
        {...defaultProps}
        value="test"
        matchIndex={2}
        matchCount={10}
      />,
    );

    expect(screen.getByText("3 of 10")).toBeInTheDocument();
  });

  it("shows 'No results' when matchCount is 0", () => {
    render(
      <SearchBar
        {...defaultProps}
        value="test"
        matchIndex={0}
        matchCount={0}
      />,
    );

    expect(screen.getByText("No results")).toBeInTheDocument();
  });

  it("shows match count with '+' when matchIndex is -1 (exceeds highlightLimit)", () => {
    render(
      <SearchBar
        {...defaultProps}
        value="test"
        matchIndex={-1}
        matchCount={1500}
      />,
    );

    expect(screen.getByText("1500+ matches")).toBeInTheDocument();
  });

  it("shows nothing when matchCount is null", () => {
    render(
      <SearchBar
        {...defaultProps}
        value="test"
        matchIndex={null}
        matchCount={null}
      />,
    );

    expect(screen.queryByText(/of/)).not.toBeInTheDocument();
    expect(screen.queryByText("No results")).not.toBeInTheDocument();
  });

  it("Enter calls onFindNext", () => {
    const onFindNext = vi.fn();
    render(
      <SearchBar {...defaultProps} value="test" onFindNext={onFindNext} />,
    );

    fireEvent.keyDown(screen.getByTestId("terminal-search-input"), {
      key: "Enter",
    });

    expect(onFindNext).toHaveBeenCalledTimes(1);
  });

  it("Shift+Enter calls onFindPrevious", () => {
    const onFindPrevious = vi.fn();
    render(
      <SearchBar
        {...defaultProps}
        value="test"
        onFindPrevious={onFindPrevious}
      />,
    );

    fireEvent.keyDown(screen.getByTestId("terminal-search-input"), {
      key: "Enter",
      shiftKey: true,
    });

    expect(onFindPrevious).toHaveBeenCalledTimes(1);
  });

  it("close button calls onClose", () => {
    const onClose = vi.fn();
    render(<SearchBar {...defaultProps} onClose={onClose} />);

    fireEvent.click(screen.getByLabelText("Close search"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
