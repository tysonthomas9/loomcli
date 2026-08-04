/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SearchScopeIndicator component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { SearchScopeIndicator } from "../SearchScopeIndicator";

describe("SearchScopeIndicator", () => {
  describe("rendering", () => {
    it("renders nothing when scopeName is undefined", () => {
      const { container } = render(
        <SearchScopeIndicator scopeName={undefined} onClear={() => {}} />,
      );

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("search-scope-indicator"),
      ).not.toBeInTheDocument();
    });

    it("renders nothing when scopeName is empty string", () => {
      const { container } = render(
        <SearchScopeIndicator scopeName="" onClear={() => {}} />,
      );

      expect(container.innerHTML).toBe("");
      expect(
        screen.queryByTestId("search-scope-indicator"),
      ).not.toBeInTheDocument();
    });

    it("renders chip with scope name when scopeName is set", () => {
      render(
        <SearchScopeIndicator scopeName="my-workspace" onClear={() => {}} />,
      );

      const chip = screen.getByTestId("search-scope-indicator");
      expect(chip).toBeInTheDocument();
      expect(chip).toHaveTextContent("in: my-workspace");
    });
  });

  describe("clear button", () => {
    it("calls onClear when clear button is clicked", () => {
      const handleClear = vi.fn();
      render(
        <SearchScopeIndicator scopeName="my-workspace" onClear={handleClear} />,
      );

      const clearButton = screen.getByRole("button", {
        name: "Clear scope: my-workspace",
      });
      fireEvent.click(clearButton);

      expect(handleClear).toHaveBeenCalledTimes(1);
    });

    it("does not call onClear without interaction", () => {
      const handleClear = vi.fn();
      render(
        <SearchScopeIndicator scopeName="my-workspace" onClear={handleClear} />,
      );

      expect(handleClear).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it("clear button has correct aria-label with scope name", () => {
      render(<SearchScopeIndicator scopeName="api-repo" onClear={() => {}} />);

      const clearButton = screen.getByRole("button");
      expect(clearButton).toHaveAttribute(
        "aria-label",
        "Clear scope: api-repo",
      );
    });

    it("clear button has type=button", () => {
      render(<SearchScopeIndicator scopeName="api-repo" onClear={() => {}} />);

      const clearButton = screen.getByRole("button");
      expect(clearButton).toHaveAttribute("type", "button");
    });

    it("aria-label reflects the current scope name", () => {
      const { rerender } = render(
        <SearchScopeIndicator scopeName="workspace-a" onClear={() => {}} />,
      );

      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-label",
        "Clear scope: workspace-a",
      );

      rerender(
        <SearchScopeIndicator scopeName="workspace-b" onClear={() => {}} />,
      );

      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-label",
        "Clear scope: workspace-b",
      );
    });
  });
});
