/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MoreFiltersMenu component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import "@testing-library/jest-dom";

import type { GroupByOption } from "@/components/FilterBar";

import { MoreFiltersMenu } from "../MoreFiltersMenu";

/**
 * Default props for rendering MoreFiltersMenu.
 */
function renderMenu(
  groupBy: GroupByOption = "none",
  onGroupByChange = vi.fn(),
) {
  return {
    onGroupByChange,
    ...render(
      <MoreFiltersMenu groupBy={groupBy} onGroupByChange={onGroupByChange} />,
    ),
  };
}

describe("MoreFiltersMenu", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("rendering", () => {
    it("renders the trigger button", () => {
      renderMenu();

      expect(screen.getByTestId("more-filters-trigger")).toBeInTheDocument();
    });

    it("trigger button displays ellipsis text", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");
      // The component renders &#x2026; which is the horizontal ellipsis character
      expect(trigger).toHaveTextContent("\u2026");
    });

    it("does not render menu popover by default", () => {
      renderMenu();

      expect(
        screen.queryByTestId("more-filters-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("popover open/close", () => {
    it("opens popover on click showing GroupBy dropdown", () => {
      renderMenu();

      fireEvent.click(screen.getByTestId("more-filters-trigger"));

      expect(screen.getByTestId("more-filters-menu")).toBeInTheDocument();
      expect(screen.getByTestId("more-filters-groupby")).toBeInTheDocument();
      expect(screen.getByText("Group by")).toBeInTheDocument();
    });

    it("closes popover when clicking trigger again", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");

      fireEvent.click(trigger);
      expect(screen.getByTestId("more-filters-menu")).toBeInTheDocument();

      fireEvent.click(trigger);
      expect(
        screen.queryByTestId("more-filters-menu"),
      ).not.toBeInTheDocument();
    });

    it("closes popover on outside click", () => {
      renderMenu();

      fireEvent.click(screen.getByTestId("more-filters-trigger"));
      expect(screen.getByTestId("more-filters-menu")).toBeInTheDocument();

      // Click outside
      fireEvent.mouseDown(document.body);

      expect(
        screen.queryByTestId("more-filters-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("GroupBy interactions", () => {
    it("calls onGroupByChange with correct value when option selected", () => {
      const onGroupByChange = vi.fn();
      renderMenu("none", onGroupByChange);

      fireEvent.click(screen.getByTestId("more-filters-trigger"));

      const select = screen.getByTestId("more-filters-groupby");
      fireEvent.change(select, { target: { value: "epic" } });

      expect(onGroupByChange).toHaveBeenCalledWith("epic");
    });

    it("calls onGroupByChange for each groupBy value correctly", () => {
      const groupByValues = [
        "none",
        "epic",
        "assignee",
        "priority",
        "type",
        "label",
      ] as const;

      groupByValues.forEach((groupByValue) => {
        const onGroupByChange = vi.fn();
        const { unmount } = render(
          <MoreFiltersMenu
            groupBy="none"
            onGroupByChange={onGroupByChange}
          />,
        );

        fireEvent.click(screen.getByTestId("more-filters-trigger"));

        const select = screen.getByTestId("more-filters-groupby");
        fireEvent.change(select, { target: { value: groupByValue } });

        expect(onGroupByChange).toHaveBeenCalledWith(groupByValue);

        unmount();
      });
    });

    it("shows current groupBy value in the select", () => {
      renderMenu("assignee");

      fireEvent.click(screen.getByTestId("more-filters-trigger"));

      const select = screen.getByTestId("more-filters-groupby");
      expect(select).toHaveValue("assignee");
    });

    it("renders all 6 groupBy options", () => {
      renderMenu();

      fireEvent.click(screen.getByTestId("more-filters-trigger"));

      const select = screen.getByTestId("more-filters-groupby");
      const options = select.querySelectorAll("option");

      expect(options).toHaveLength(6);
      expect(options[0]).toHaveTextContent("All");
      expect(options[1]).toHaveTextContent("Epic");
      expect(options[2]).toHaveTextContent("Assignee");
      expect(options[3]).toHaveTextContent("Priority");
      expect(options[4]).toHaveTextContent("Type");
      expect(options[5]).toHaveTextContent("Label");
    });
  });

  describe("active indicator", () => {
    it("shows active indicator (dot) when groupBy is not none", () => {
      const { container } = render(
        <MoreFiltersMenu groupBy="epic" onGroupByChange={vi.fn()} />,
      );

      const indicator = container.querySelector("span");
      expect(indicator).toBeInTheDocument();
    });

    it("shows active indicator for each non-none groupBy value", () => {
      const activeValues = [
        "epic",
        "assignee",
        "priority",
        "type",
        "label",
      ] as const;

      activeValues.forEach((groupByValue) => {
        const { container, unmount } = render(
          <MoreFiltersMenu
            groupBy={groupByValue}
            onGroupByChange={vi.fn()}
          />,
        );

        const indicator = container.querySelector("span");
        expect(indicator).toBeInTheDocument();

        unmount();
      });
    });

    it("does NOT show active indicator when groupBy is none", () => {
      const { container } = render(
        <MoreFiltersMenu groupBy="none" onGroupByChange={vi.fn()} />,
      );

      const indicator = container.querySelector("span");
      expect(indicator).not.toBeInTheDocument();
    });
  });

  describe("trigger active styling", () => {
    it("trigger gets active styling when menu is open", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");
      fireEvent.click(trigger);

      expect(trigger.className).toMatch(/active/);
    });

    it("trigger does not have active styling when menu is closed", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");

      expect(trigger.className).not.toMatch(/active/);
    });

    it("trigger loses active styling when menu is closed via outside click", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");

      fireEvent.click(trigger);
      expect(trigger.className).toMatch(/active/);

      fireEvent.mouseDown(document.body);
      expect(trigger.className).not.toMatch(/active/);
    });
  });

  describe("accessibility", () => {
    it("trigger has aria-label", () => {
      renderMenu();

      const trigger = screen.getByRole("button", { name: /more filters/i });
      expect(trigger).toBeInTheDocument();
    });

    it("trigger has aria-expanded attribute reflecting open state", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");
      expect(trigger).toHaveAttribute("aria-expanded", "false");

      fireEvent.click(trigger);
      expect(trigger).toHaveAttribute("aria-expanded", "true");
    });

    it("trigger has aria-haspopup attribute", () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");
      expect(trigger).toHaveAttribute("aria-haspopup", "dialog");
    });

    it('trigger is type="button" to prevent form submission', () => {
      renderMenu();

      const trigger = screen.getByTestId("more-filters-trigger");
      expect(trigger).toHaveAttribute("type", "button");
    });

    it("groupBy select has accessible label", () => {
      renderMenu();

      fireEvent.click(screen.getByTestId("more-filters-trigger"));

      const select = screen.getByRole("combobox", {
        name: /group issues by/i,
      });
      expect(select).toBeInTheDocument();
    });

    it("groupBy select has associated label element", () => {
      renderMenu();

      fireEvent.click(screen.getByTestId("more-filters-trigger"));

      const label = screen.getByText("Group by");
      expect(label).toBeInTheDocument();
      expect(label).toHaveAttribute("for", "more-filters-groupby");
    });
  });
});
