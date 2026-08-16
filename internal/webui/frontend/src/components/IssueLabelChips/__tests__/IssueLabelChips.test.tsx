/** @vitest-environment jsdom */

import { render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { describe, expect, it } from "vitest";

import { IssueLabelChips } from "../IssueLabelChips";

describe("IssueLabelChips", () => {
  it("renders plain labels, excludes repo labels, and accents Recommended", () => {
    render(
      <IssueLabelChips
        labels={["frontend", "repo:loomcli", "recommended", "ux"]}
      />,
    );

    const labels = screen.getByLabelText("Issue labels");
    expect(within(labels).getByText("frontend")).toHaveAttribute(
      "data-variant",
      "default",
    );
    expect(within(labels).getByText("Recommended")).toHaveAttribute(
      "data-variant",
      "recommended",
    );
    expect(within(labels).queryByText("repo:loomcli")).not.toBeInTheDocument();
  });

  it("prioritizes Recommended and caps visible chips with an overflow count", () => {
    render(
      <IssueLabelChips
        labels={["one", "two", "three", "recommended", "four"]}
        maxVisible={3}
      />,
    );

    const labels = screen.getByLabelText("Issue labels");
    expect(within(labels).getByText("Recommended")).toBeInTheDocument();
    expect(within(labels).getByText("one")).toBeInTheDocument();
    expect(within(labels).getByText("two")).toBeInTheDocument();
    expect(within(labels).queryByText("three")).not.toBeInTheDocument();
    expect(within(labels).getByText("+2")).toHaveAccessibleName(
      "2 more labels",
    );
  });
});
