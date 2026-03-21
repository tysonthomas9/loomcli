/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for NoBackendsEmptyState component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { NoBackendsEmptyState } from "../NoBackendsEmptyState";

// Mock CSS module
vi.mock("../NoBackendsEmptyState.module.css", () => ({
  default: {
    container: "container",
    icon: "icon",
    heading: "heading",
    description: "description",
    settingsButton: "settingsButton",
  },
}));

describe("NoBackendsEmptyState", () => {
  describe("rendering", () => {
    it("renders the container with correct testid", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.getByTestId("no-backends-empty-state"),
      ).toBeInTheDocument();
    });

    it("renders the heading text", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.getByText("No backends configured"),
      ).toBeInTheDocument();
    });

    it("renders the description text", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.getByText(
          "Configure at least one AI backend to start using Talk to Lead.",
        ),
      ).toBeInTheDocument();
    });

    it("renders the icon", () => {
      render(<NoBackendsEmptyState />);

      expect(screen.getByText("\u2B1A")).toBeInTheDocument();
    });
  });

  describe("settings button", () => {
    it("renders 'Go to Settings' button when onGoToSettings is provided", () => {
      render(<NoBackendsEmptyState onGoToSettings={vi.fn()} />);

      expect(
        screen.getByTestId("go-to-settings-button"),
      ).toBeInTheDocument();
      expect(screen.getByText("Go to Settings")).toBeInTheDocument();
    });

    it("does NOT render settings button when onGoToSettings is undefined", () => {
      render(<NoBackendsEmptyState />);

      expect(
        screen.queryByTestId("go-to-settings-button"),
      ).not.toBeInTheDocument();
    });

    it("calls onGoToSettings when button is clicked", () => {
      const onGoToSettings = vi.fn();
      render(<NoBackendsEmptyState onGoToSettings={onGoToSettings} />);

      fireEvent.click(screen.getByTestId("go-to-settings-button"));

      expect(onGoToSettings).toHaveBeenCalledTimes(1);
    });
  });

  describe("accessibility", () => {
    it('has role="status"', () => {
      render(<NoBackendsEmptyState />);

      expect(screen.getByRole("status")).toBeInTheDocument();
    });
  });
});
